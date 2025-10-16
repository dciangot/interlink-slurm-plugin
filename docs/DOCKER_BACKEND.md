# Docker Backend for InterLink

The Docker backend enables InterLink to run Kubernetes pods as Docker containers on a single node, without requiring a batch queuing system like SLURM.

## Overview

Unlike the SLURM backend which submits jobs to a batch queue, the Docker backend:

- **Runs containers immediately** - No queueing, containers start as soon as the pod is created
- **Uses Docker daemon directly** - Communicates with Docker via the Docker socket or TCP
- **Single-node execution** - Designed for development, testing, or edge deployments
- **No job script generation** - Executes containers directly with Docker API

## Use Cases

The Docker backend is ideal for:

1. **Development and Testing** - Quick local testing without HPC infrastructure
2. **Edge Computing** - Running workloads on single nodes or edge devices
3. **CI/CD Pipelines** - Integration testing with containerized environments
4. **Prototyping** - Rapid prototyping before deploying to HPC clusters

## Configuration

### Basic Configuration

Create a configuration file (e.g., `DockerConfig.yaml`):

```yaml
BackendType: docker

Docker:
  Endpoint: "unix:///var/run/docker.sock"
  DataRootFolder: "/var/interlink/docker"
  Network: ""
  ImagePrefix: ""
  VerboseLogging: true
  ExportPodData: true
  EnableProbes: false
```

### Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `Endpoint` | Docker daemon endpoint | `unix:///var/run/docker.sock` |
| `Network` | Docker network for containers | `""` (default bridge) |
| `DataRootFolder` | Directory for job metadata and logs | `/var/interlink` |
| `ImagePrefix` | Prefix for container images | `""` |
| `VerboseLogging` | Enable debug logging | `false` |
| `ErrorsOnlyLogging` | Only log errors | `false` |
| `ExportPodData` | Export ConfigMaps/Secrets as files | `true` |
| `EnableProbes` | Enable readiness/liveness probes | `false` |

### Remote Docker Daemon

To connect to a remote Docker daemon:

```yaml
Docker:
  Endpoint: "tcp://remote-host:2375"
```

**Security Warning**: For production, use TLS-secured Docker endpoints.

## Running the Sidecar

### Set Configuration Path

```bash
export INTERLINK_CONFIG_PATH=/path/to/DockerConfig.yaml
```

### Start the Sidecar

```bash
./bin/slurm-sd
```

The sidecar will:
1. Load the Docker backend configuration
2. Connect to the Docker daemon
3. Start the HTTP API server on port 4000 (default)

### Verify Connection

Check that the sidecar can connect to Docker:

```bash
curl http://localhost:4000/system-info
```

Expected output:
```
Docker Info:
  Containers: 5 (Running: 2, Paused: 0, Stopped: 3)
  Images: 15
  Server Version: 24.0.7
  Storage Driver: overlay2
  Operating System: Ubuntu 22.04
  Architecture: x86_64
```

## How It Works

### Pod to Container Mapping

When InterLink creates a pod:

1. **Init Containers** (if any) run sequentially
   - Each init container must complete successfully before the next starts
   - Failure of an init container prevents regular containers from starting

2. **Regular Containers** run in parallel
   - All containers in the pod start simultaneously
   - Each container runs as a separate Docker container

### Container Lifecycle

```
Pod Created → Init Containers (sequential) → Regular Containers (parallel) → Pod Running
```

### Job Tracking

The Docker backend tracks "jobs" using:

- **Job ID**: Primary Docker container ID (first regular container)
- **Metadata Files**: JSON files in `DataRootFolder/.metadata/`
- **Container Labels**: Kubernetes metadata as Docker labels

Example metadata:
```json
{
  "PodUID": "abc123",
  "JobID": "docker-container-id",
  "PodName": "my-pod",
  "Namespace": "default",
  "ContainerIDs": {
    "nginx": "container-id-1",
    "sidecar": "container-id-2"
  },
  "StartTime": "2025-01-15T10:30:00Z",
  "FilesPath": "/var/interlink/docker/default-abc123"
}
```

### Volume Handling

Supported volume types:

- **HostPath**: Bind mounts from the host filesystem
- **EmptyDir**: Temporary directories created in `DataRootFolder/emptydir/`
- **ConfigMap/Secret**: When `ExportPodData: true`, written as files and mounted

Example pod with volumes:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: volume-test
spec:
  containers:
  - name: app
    image: nginx
    volumeMounts:
    - name: data
      mountPath: /data
    - name: config
      mountPath: /etc/config
  volumes:
  - name: data
    hostPath:
      path: /host/data
  - name: config
    emptyDir: {}
```

## Limitations

The Docker backend currently has these limitations:

1. **Single Node Only** - Cannot distribute workloads across multiple nodes
2. **No Queue Management** - Containers start immediately, no scheduling
3. **Limited Resource Enforcement** - Uses Docker resource constraints (not cgroups v2 limits)
4. **No Job Arrays** - Each pod is independent
5. **Basic Probe Support** - Probes are not fully implemented yet

## Troubleshooting

### Connection Errors

**Problem**: `failed to create Docker client: error during connect`

**Solution**: Ensure Docker daemon is running and the socket is accessible:
```bash
# Check Docker is running
docker ps

# Verify socket permissions
ls -l /var/run/docker.sock

# Add user to docker group if needed
sudo usermod -aG docker $USER
```

### Image Pull Errors

**Problem**: `failed to pull image: unauthorized`

**Solution**: Authenticate with the registry:
```bash
docker login registry.example.com
```

The Docker backend uses the same authentication as the local Docker daemon.

### Container Not Starting

**Problem**: Container created but not running

**Solution**: Check container logs:
```bash
# Find container ID from metadata
cat /var/interlink/docker/.metadata/<pod-uid>.json

# Check Docker logs
docker logs <container-id>
```

## Comparison with SLURM Backend

| Feature | SLURM Backend | Docker Backend |
|---------|---------------|----------------|
| Execution Model | Batch queue | Direct execution |
| Multi-node | Yes | No |
| Job Scheduling | SLURM scheduler | Immediate |
| Container Runtime | Singularity/Enroot | Docker |
| Resource Management | SLURM allocations | Docker limits |
| Use Case | HPC workloads | Development/Edge |

## Development

### Building with Docker Backend

```bash
make all
```

The binary supports both SLURM and Docker backends through the unified interface.

### Testing

Create a test pod:

```bash
curl -X POST http://localhost:4000/create \
  -H "Content-Type: application/json" \
  -d @test-pod.json
```

Check status:

```bash
curl -X GET http://localhost:4000/status \
  -H "Content-Type: application/json" \
  -d '[{"metadata":{"uid":"test-uid"}}]'
```

## Future Enhancements

Planned improvements for the Docker backend:

- [ ] Full probe implementation (HTTP/TCP/Exec)
- [ ] Support for Docker Compose for multi-container pods
- [ ] Network policy enforcement
- [ ] Resource quota management
- [ ] Multi-node support via Docker Swarm
- [ ] Integration with container registries for private images
