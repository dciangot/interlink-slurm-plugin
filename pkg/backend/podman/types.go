package podman

import (
	"context"
	"net/http"
	"time"
)

// PodmanConfig holds Podman-specific configuration
type PodmanConfig struct {
	// Podman API endpoint
	// Default: unix:///run/podman/podman.sock
	// For rootless: unix://$XDG_RUNTIME_DIR/podman/podman.sock
	// For remote: http://hostname:8080
	Endpoint string `yaml:"Endpoint"`

	// API version (v3.0.0, v4.0.0, etc.)
	// Leave empty for auto-detection
	APIVersion string `yaml:"APIVersion"`

	// Data root folder for storing job metadata and logs
	DataRootFolder string `yaml:"DataRootFolder"`

	// Default image prefix (e.g., "docker.io/")
	ImagePrefix string `yaml:"ImagePrefix"`

	// Enable verbose logging
	VerboseLogging bool `yaml:"VerboseLogging"`

	// Enable errors-only logging
	ErrorsOnlyLogging bool `yaml:"ErrorsOnlyLogging"`

	// Namespace for pod isolation (informational)
	Namespace string `yaml:"Namespace"`

	// Command prefix for all container commands
	CommandPrefix string `yaml:"CommandPrefix"`

	// Export pod data (ConfigMaps, Secrets) to files
	ExportPodData bool `yaml:"ExportPodData"`

	// Enable probe support (readiness, liveness, startup)
	EnableProbes bool `yaml:"EnableProbes"`

	// Use Podman pods (groups containers together)
	UsePods bool `yaml:"UsePods"`

	// Network mode (bridge, host, none, or network name)
	NetworkMode string `yaml:"NetworkMode"`
}

// PodmanBackend implements the BatchSystem interface using Podman
type PodmanBackend struct {
	Config     *PodmanConfig
	HTTPClient *http.Client
	BaseURL    string
	Jobs       map[string]*JobInfo
	Ctx        context.Context
}

// JobInfo tracks metadata for a Podman-based "job"
type JobInfo struct {
	// PodUID is the Kubernetes pod UID
	PodUID string

	// JobID is the Podman pod ID (if UsePods=true) or primary container ID
	JobID string

	// PodName is the Kubernetes pod name
	PodName string

	// Namespace is the Kubernetes namespace
	Namespace string

	// ContainerIDs maps container names to Podman container IDs
	ContainerIDs map[string]string

	// PodmanPodID is the Podman pod ID (if UsePods=true)
	PodmanPodID string

	// StartTime when the job started
	StartTime time.Time

	// EndTime when the job finished (zero if still running)
	EndTime time.Time

	// FilesPath is the directory storing job metadata and logs
	FilesPath string
}

// Podman API response structures
type PodmanContainer struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Created int64             `json:"Created"`
	Labels  map[string]string `json:"Labels"`
}

type PodmanContainerInspect struct {
	ID      string                `json:"Id"`
	Created string                `json:"Created"`
	State   PodmanContainerState  `json:"State"`
	Config  PodmanContainerConfig `json:"Config"`
}

type PodmanContainerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	Paused     bool   `json:"Paused"`
	StartedAt  string `json:"StartedAt"`
	FinishedAt string `json:"FinishedAt"`
	ExitCode   int    `json:"ExitCode"`
}

type PodmanContainerConfig struct {
	Image  string            `json:"Image"`
	Cmd    []string          `json:"Cmd"`
	Env    []string          `json:"Env"`
	Labels map[string]string `json:"Labels"`
}

type PodmanPod struct {
	ID      string            `json:"Id"`
	Name    string            `json:"Name"`
	Status  string            `json:"Status"`
	Created string            `json:"Created"`
	Labels  map[string]string `json:"Labels"`
}

type PodmanVersion struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
	GoVersion  string `json:"GoVersion"`
	GitCommit  string `json:"GitCommit"`
	BuiltTime  string `json:"BuiltTime"`
	OsArch     string `json:"OsArch"`
}
