# Backend Architecture

This document describes the pluggable backend architecture introduced in InterLink SLURM Plugin v2.0.

## Overview

The InterLink sidecar has been refactored to support multiple batch system backends through a common interface. This allows the same codebase to work with different execution environments:

- **SLURM Backend**: Traditional HPC batch queue system
- **Docker Backend**: Single-node container execution
- **Future Backends**: PBS, Torque, HTCondor, Kubernetes, etc.

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                    InterLink API Server                      │
└─────────────────────────┬───────────────────────────────────┘
                          │ HTTP/gRPC
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                  Sidecar HTTP Server                         │
│                    (cmd/main.go)                             │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│              Generic HTTP Handlers                           │
│              (pkg/handlers/)                                 │
│  • SubmitHandler    • StatusHandler   • StopHandler         │
│  • GetLogsHandler   • SystemInfoHandler                      │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                BatchSystem Interface                         │
│               (pkg/backend/interface.go)                     │
│                                                              │
│  • Submit(pod) -> jobID                                      │
│  • Status(pods) -> statuses                                  │
│  • Cancel(podUID) -> error                                   │
│  • GetLogs(podUID, container) -> logs                        │
│  • SystemInfo() -> info                                      │
└─────────────────┬───────────────────────┬───────────────────┘
                  │                       │
         ┌────────▼────────┐    ┌────────▼────────┐
         │  SLURM Backend  │    │ Docker Backend  │
         │  (pkg/backend/  │    │  (pkg/backend/  │
         │     slurm/)     │    │    docker/)     │
         └────────┬────────┘    └────────┬────────┘
                  │                      │
         ┌────────▼────────┐    ┌────────▼────────┐
         │ SLURM Commands  │    │  Docker API     │
         │ sbatch/scancel  │    │   Client        │
         └─────────────────┘    └─────────────────┘
```

## Core Components

### 1. BatchSystem Interface (`pkg/backend/interface.go`)

The central abstraction that all backends must implement:

```go
type BatchSystem interface {
    Submit(ctx context.Context, pod *commonIL.RetrievedPodData) (string, error)
    Status(ctx context.Context, pods []*v1.Pod) ([]commonIL.PodStatus, error)
    Cancel(ctx context.Context, podUID string) error
    GetLogs(ctx context.Context, podUID, containerName string, follow bool, tailLines int) (io.Reader, error)
    SystemInfo(ctx context.Context) (string, error)
    CreateDirectories() error
    LoadJobs() error
    GetJobID(podUID string) string
}
```

This interface defines the contract that any batch system backend must fulfill.

### 2. Generic HTTP Handlers (`pkg/handlers/`)

Backend-agnostic HTTP request handlers that:

- Parse incoming HTTP requests
- Call the appropriate `BatchSystem` interface method
- Serialize and return responses
- Handle errors uniformly across backends

Example:

```go
func (h *GenericHandler) SubmitHandler(w http.ResponseWriter, r *http.Request) {
    var podData commonIL.RetrievedPodData
    json.Unmarshal(bodyBytes, &podData)
    
    jobID, err := h.Backend.Submit(ctx, &podData)
    // ... handle response
}
```

### 3. Backend Implementations

#### SLURM Backend (`pkg/backend/slurm/`)

Adapter that wraps the existing SLURM handler implementation:

- Maintains backward compatibility with existing SLURM code
- Uses HTTP test recorder pattern to call existing handlers
- Translates interface calls to SLURM operations

```go
type SlurmBackend struct {
    handler *slurm.SidecarHandler
}

func (s *SlurmBackend) Submit(ctx context.Context, podData *commonIL.RetrievedPodData) (string, error) {
    // Marshals pod data and calls existing SubmitHandler
}
```

#### Docker Backend (`pkg/backend/docker/`)

New implementation for direct Docker execution:

- Uses Docker SDK for Go
- Manages containers directly without job scripts
- Stores metadata for job tracking
- Implements immediate execution (no queueing)

```go
type DockerBackend struct {
    Config *DockerConfig
    Client *client.Client
    Jobs   map[string]*JobInfo
}

func (d *DockerBackend) Submit(ctx context.Context, podData *commonIL.RetrievedPodData) (string, error) {
    // Creates and starts Docker containers
}
```

### 4. Unified Configuration (`pkg/config/`)

Supports both backends with a single configuration file:

```go
type UnifiedConfig struct {
    BackendType BackendType
    SLURM       *slurm.SlurmConfig
    Docker      *docker.DockerConfig
}
```

Configuration loading handles:

- New unified format with `BackendType` field
- Legacy SLURM-only format for backward compatibility
- Environment variable overrides

### 5. Main Entry Point (`cmd/main.go`)

Orchestrates backend initialization:

```go
func main() {
    cfg := config.LoadConfig()
    
    var backend backend.BatchSystem
    switch cfg.BackendType {
    case "slurm":
        backend = slurm.NewSlurmBackend(...)
    case "docker":
        backend = docker.NewDockerBackend(...)
    }
    
    handler := &handlers.GenericHandler{Backend: backend}
    // ... set up HTTP routes
}
```

## Request Flow

### Pod Creation Flow

```
1. InterLink API → POST /create → Sidecar
2. GenericHandler.SubmitHandler()
3. BatchSystem.Submit(pod)
4. Backend-specific implementation:
   
   SLURM:                        Docker:
   • Generate SBATCH script      • Create Docker container config
   • Call sbatch command          • Call Docker API
   • Parse job ID                 • Get container ID
   • Track job metadata           • Track job metadata
   
5. Return job ID to InterLink
```

### Status Polling Flow

```
1. InterLink API → GET /status → Sidecar
2. GenericHandler.StatusHandler()
3. BatchSystem.Status(pods)
4. Backend-specific implementation:
   
   SLURM:                        Docker:
   • Call squeue command          • Inspect containers
   • Parse job states             • Map container states
   • Map to K8s states            • Build K8s statuses
   
5. Return pod statuses to InterLink
```

## Adding a New Backend

To add support for a new batch system (e.g., PBS, Torque):

### Step 1: Create Backend Package

```bash
mkdir -p pkg/backend/pbs
```

### Step 2: Implement BatchSystem Interface

```go
// pkg/backend/pbs/pbs.go
package pbs

type PBSBackend struct {
    Config *PBSConfig
    Jobs   map[string]*JobInfo
}

func (p *PBSBackend) Submit(ctx context.Context, pod *commonIL.RetrievedPodData) (string, error) {
    // Generate PBS script
    // Call qsub
    // Return job ID
}

func (p *PBSBackend) Status(ctx context.Context, pods []*v1.Pod) ([]commonIL.PodStatus, error) {
    // Call qstat
    // Parse states
    // Return statuses
}

// ... implement other interface methods
```

### Step 3: Define Configuration

```go
// pkg/backend/pbs/types.go
type PBSConfig struct {
    QsubPath       string
    QdelPath       string
    QstatPath      string
    DataRootFolder string
    // ... other PBS-specific settings
}
```

### Step 4: Update Configuration Loader

```go
// pkg/config/config.go
type UnifiedConfig struct {
    BackendType BackendType
    SLURM       *slurm.SlurmConfig
    Docker      *docker.DockerConfig
    PBS         *pbs.PBSConfig  // Add new backend
}
```

### Step 5: Update Main Entry Point

```go
// cmd/main.go
switch cfg.BackendType {
case "slurm":
    backend = slurm.NewSlurmBackend(...)
case "docker":
    backend = docker.NewDockerBackend(...)
case "pbs":
    backend = pbs.NewPBSBackend(...)  // Add new case
}
```

### Step 6: Create Example Configuration

```yaml
# examples/PBSConfig.yaml
BackendType: pbs

PBS:
  QsubPath: "qsub"
  QdelPath: "qdel"
  QstatPath: "qstat"
  DataRootFolder: "/var/interlink/pbs"
```

## Design Principles

### 1. Single Responsibility

Each component has a clear responsibility:
- **Interface**: Defines contract
- **Handlers**: HTTP protocol handling
- **Backends**: Batch system integration
- **Config**: Configuration management

### 2. Open/Closed Principle

The system is:
- **Open for extension**: New backends can be added without modifying existing code
- **Closed for modification**: Existing backends and handlers remain unchanged

### 3. Dependency Inversion

High-level handlers depend on the `BatchSystem` interface, not concrete implementations. This allows:
- Easy testing with mock backends
- Runtime backend selection
- Independent backend development

### 4. Backward Compatibility

The SLURM backend maintains full compatibility with:
- Existing SLURM configurations
- All SLURM-specific features
- Current deployment patterns

## Testing Strategy

### Unit Tests

Each backend should have unit tests:

```go
// pkg/backend/docker/docker_test.go
func TestDockerBackend_Submit(t *testing.T) {
    backend := NewDockerBackend(ctx, config)
    jobID, err := backend.Submit(ctx, testPod)
    assert.NoError(t, err)
    assert.NotEmpty(t, jobID)
}
```

### Integration Tests

Test the full stack with each backend:

```bash
# Test with SLURM backend
INTERLINK_CONFIG_PATH=examples/SlurmConfig.yaml go test ./...

# Test with Docker backend
INTERLINK_CONFIG_PATH=examples/DockerConfig.yaml go test ./...
```

### Mock Backend for Testing

Create a mock backend for handler testing:

```go
type MockBackend struct {
    SubmitFunc func(context.Context, *commonIL.RetrievedPodData) (string, error)
    // ... other functions
}

func (m *MockBackend) Submit(ctx context.Context, pod *commonIL.RetrievedPodData) (string, error) {
    return m.SubmitFunc(ctx, pod)
}
```

## Migration Guide

### From Legacy SLURM-Only Version

Existing deployments continue to work without changes:

1. **Configuration**: No changes needed to `SlurmConfig.yaml`
2. **Deployment**: Same binary, same startup procedure
3. **APIs**: All HTTP endpoints remain compatible

### To New Unified Configuration

Optionally migrate to unified format:

```yaml
# Old format (still supported)
SbatchPath: "sbatch"
ScancelPath: "scancel"
# ...

# New format (recommended)
BackendType: slurm
SLURM:
  SbatchPath: "sbatch"
  ScancelPath: "scancel"
  # ...
```

## Performance Considerations

### SLURM Backend

Performance characteristics unchanged from original implementation:
- Command execution overhead: ~100-500ms per operation
- Status polling with 10-second cache
- Batch job submission latency: depends on SLURM queue

### Docker Backend

Performance characteristics:
- Container creation: ~1-5 seconds (includes image pull if needed)
- Status checks: <100ms (local API call)
- No queueing delay: immediate execution

## Future Enhancements

### Planned Features

1. **Multi-backend support**: Run multiple backends simultaneously
2. **Backend auto-detection**: Automatically select backend based on environment
3. **Fallback chains**: Try Docker if SLURM unavailable
4. **Metrics collection**: Unified metrics across all backends
5. **GraphQL API**: Alternative to REST for complex queries

### Additional Backends

Community contributions welcome for:
- **PBS/Torque**: Traditional HPC batch system
- **HTCondor**: High-throughput computing
- **Kubernetes**: Native K8s job execution
- **AWS Batch**: Cloud-native batch processing
- **Azure Batch**: Microsoft cloud batch service

## References

- [Docker Backend Documentation](./DOCKER_BACKEND.md)
- [BatchSystem Interface](../pkg/backend/interface.go)
- [Example Configurations](../examples/)
