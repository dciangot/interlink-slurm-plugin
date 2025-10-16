//go:build docker
// +build docker

package docker

import (
	"context"
	"time"

	"github.com/docker/docker/client"
)

// DockerConfig holds Docker-specific configuration
type DockerConfig struct {
	// Docker daemon endpoint (e.g., "unix:///var/run/docker.sock" or "tcp://host:2375")
	Endpoint string `yaml:"Endpoint"`
	
	// Network to attach containers to
	Network string `yaml:"Network"`
	
	// Data root folder for storing job metadata and logs
	DataRootFolder string `yaml:"DataRootFolder"`
	
	// Default image prefix (e.g., "docker.io/")
	ImagePrefix string `yaml:"ImagePrefix"`
	
	// Enable verbose logging
	VerboseLogging bool `yaml:"VerboseLogging"`
	
	// Enable errors-only logging
	ErrorsOnlyLogging bool `yaml:"ErrorsOnlyLogging"`
	
	// Namespace for isolation
	Namespace string `yaml:"Namespace"`
	
	// Command prefix for all container commands
	CommandPrefix string `yaml:"CommandPrefix"`
	
	// Export pod data (ConfigMaps, Secrets) to files
	ExportPodData bool `yaml:"ExportPodData"`
	
	// Enable probe support (readiness, liveness, startup)
	EnableProbes bool `yaml:"EnableProbes"`
}

// DockerBackend implements the BatchSystem interface using Docker
type DockerBackend struct {
	Config *DockerConfig
	Client *client.Client
	Jobs   map[string]*JobInfo
	Ctx    context.Context
}

// JobInfo tracks metadata for a Docker-based "job"
type JobInfo struct {
	// PodUID is the Kubernetes pod UID
	PodUID string
	
	// JobID is the Docker container ID
	JobID string
	
	// PodName is the Kubernetes pod name
	PodName string
	
	// Namespace is the Kubernetes namespace
	Namespace string
	
	// ContainerIDs maps container names to Docker container IDs
	ContainerIDs map[string]string
	
	// StartTime when the job started
	StartTime time.Time
	
	// EndTime when the job finished (zero if still running)
	EndTime time.Time
	
	// FilesPath is the directory storing job metadata and logs
	FilesPath string
}
