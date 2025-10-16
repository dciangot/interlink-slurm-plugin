package containerd

import (
	"context"
	"time"

	"github.com/containerd/containerd"
)

// ContainerdConfig holds Containerd-specific configuration
type ContainerdConfig struct {
	// Containerd socket path
	// Default: /run/containerd/containerd.sock
	Socket string `yaml:"Socket"`
	
	// Namespace for container isolation
	Namespace string `yaml:"Namespace"`
	
	// Data root folder for storing job metadata and logs
	DataRootFolder string `yaml:"DataRootFolder"`
	
	// Default image prefix (e.g., "docker.io/")
	ImagePrefix string `yaml:"ImagePrefix"`
	
	// Enable verbose logging
	VerboseLogging bool `yaml:"VerboseLogging"`
	
	// Enable errors-only logging
	ErrorsOnlyLogging bool `yaml:"ErrorsOnlyLogging"`
	
	// Command prefix for all container commands
	CommandPrefix string `yaml:"CommandPrefix"`
	
	// Export pod data (ConfigMaps, Secrets) to files
	ExportPodData bool `yaml:"ExportPodData"`
	
	// Enable probe support (readiness, liveness, startup)
	EnableProbes bool `yaml:"EnableProbes"`
	
	// Snapshotter to use (overlayfs, native, etc.)
	Snapshotter string `yaml:"Snapshotter"`
	
	// Runtime to use (io.containerd.runc.v2, io.containerd.runtime.v1.linux, etc.)
	Runtime string `yaml:"Runtime"`
}

// ContainerdBackend implements the BatchSystem interface using Containerd
type ContainerdBackend struct {
	Config *ContainerdConfig
	Client *containerd.Client
	Jobs   map[string]*JobInfo
	Ctx    context.Context
}

// JobInfo tracks metadata for a Containerd-based "job"
type JobInfo struct {
	// PodUID is the Kubernetes pod UID
	PodUID string
	
	// JobID is the primary container ID
	JobID string
	
	// PodName is the Kubernetes pod name
	PodName string
	
	// Namespace is the Kubernetes namespace
	Namespace string
	
	// ContainerIDs maps container names to Containerd container IDs
	ContainerIDs map[string]string
	
	// StartTime when the job started
	StartTime time.Time
	
	// EndTime when the job finished (zero if still running)
	EndTime time.Time
	
	// FilesPath is the directory storing job metadata and logs
	FilesPath string
}
