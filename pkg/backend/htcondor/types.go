package htcondor

import (
	"context"
	"time"
)

// HTCondorConfig holds HTCondor-specific configuration
type HTCondorConfig struct {
	// HTCondor command paths
	CondorSubmitPath  string `yaml:"CondorSubmitPath"`
	CondorQPath       string `yaml:"CondorQPath"`
	CondorRmPath      string `yaml:"CondorRmPath"`
	CondorHistoryPath string `yaml:"CondorHistoryPath"`

	// Container runtime
	ContainerRuntime   string   `yaml:"ContainerRuntime"` // "singularity" or "apptainer"
	SingularityPath    string   `yaml:"SingularityPath"`
	SingularityOptions []string `yaml:"SingularityOptions"`

	// Spool directory for file staging (on submit node)
	// This is where ConfigMaps, Secrets, and setup scripts are staged
	SpoolDirectory string `yaml:"SpoolDirectory"`

	// Directory for storing submit files
	SubmitFileDir string `yaml:"SubmitFileDir"`

	// HTCondor-specific settings
	Universe           string `yaml:"Universe"`           // "vanilla", "docker", etc.
	Requirements       string `yaml:"Requirements"`       // ClassAd requirements expression
	TransferExecutable bool   `yaml:"TransferExecutable"` // Whether to transfer singularity executable

	// Image handling
	ImagePrefix string `yaml:"ImagePrefix"` // e.g., "docker://"

	// Features
	ExportPodData      bool `yaml:"ExportPodData"`      // Use spool for ConfigMaps/Secrets
	EnableLogStreaming bool `yaml:"EnableLogStreaming"` // Always false for HTCondor

	// Logging
	VerboseLogging    bool `yaml:"VerboseLogging"`
	ErrorsOnlyLogging bool `yaml:"ErrorsOnlyLogging"`
}

// HTCondorBackend implements the BatchSystem interface using HTCondor
type HTCondorBackend struct {
	Config *HTCondorConfig
	Jobs   map[string]*JobInfo
	Ctx    context.Context
}

// JobInfo tracks metadata for an HTCondor job
type JobInfo struct {
	// PodUID is the Kubernetes pod UID
	PodUID string

	// ClusterID is the HTCondor cluster ID (primary job identifier)
	ClusterID string

	// JobIDs maps container names to HTCondor job IDs (ClusterID.ProcID)
	JobIDs map[string]string

	// PodName is the Kubernetes pod name
	PodName string

	// Namespace is the Kubernetes namespace
	Namespace string

	// SpoolDir is the directory where files are staged
	SpoolDir string

	// SubmitFiles maps container names to submit file paths
	SubmitFiles map[string]string

	// StartTime when the job was submitted
	StartTime time.Time

	// EndTime when the job finished (zero if still running)
	EndTime time.Time
}

// HTCondorJobState represents HTCondor job status codes
type HTCondorJobState int

const (
	// HTCondor job status codes from condor_q
	JobStateUnexplained HTCondorJobState = 0 // Unexplained or no status available
	JobStateIdle        HTCondorJobState = 1 // Idle (waiting in queue)
	JobStateRunning     HTCondorJobState = 2 // Running
	JobStateRemoved     HTCondorJobState = 3 // Removed
	JobStateCompleted   HTCondorJobState = 4 // Completed
	JobStateHeld        HTCondorJobState = 5 // Held
	JobStateTransferOut HTCondorJobState = 6 // Transferring output
	JobStateSuspended   HTCondorJobState = 7 // Suspended
)

// String returns the string representation of job state
func (s HTCondorJobState) String() string {
	switch s {
	case JobStateIdle:
		return "Idle"
	case JobStateRunning:
		return "Running"
	case JobStateRemoved:
		return "Removed"
	case JobStateCompleted:
		return "Completed"
	case JobStateHeld:
		return "Held"
	case JobStateTransferOut:
		return "TransferringOutput"
	case JobStateSuspended:
		return "Suspended"
	default:
		return "Unknown"
	}
}

// HTCondorJob represents a job's information from condor_q or condor_history
type HTCondorJob struct {
	ClusterID  string
	ProcID     string
	JobID      string // ClusterID.ProcID
	Status     HTCondorJobState
	ExitCode   int
	RemoteHost string
	StartTime  time.Time
	CompletionTime time.Time
}

// SubmitFileData holds all data needed to generate an HTCondor submit file
type SubmitFileData struct {
	Universe           string
	Executable         string
	Arguments          string
	TransferInputFiles []string
	OutputFile         string
	ErrorFile          string
	LogFile            string
	RequestCPUs        int64
	RequestMemory      int64 // In MB
	RequestDisk        int64 // In KB
	Requirements       string
	PodUID             string
	PodName            string
	Namespace          string
	ContainerName      string
	Environment        map[string]string
}
