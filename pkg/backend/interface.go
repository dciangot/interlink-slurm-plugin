package backend

import (
	"context"
	"io"

	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	v1 "k8s.io/api/core/v1"
)

// BatchSystem defines the interface that all batch system backends must implement.
// This allows InterLink to support multiple execution backends (SLURM, Docker, PBS, etc.)
type BatchSystem interface {
	// Submit creates and submits a new job for the given pod
	// Returns the job ID assigned by the batch system
	Submit(ctx context.Context, pod *commonIL.RetrievedPodData) (string, error)

	// Status retrieves the current status of jobs for the given pods
	// Returns a slice of PodStatus containing container states
	Status(ctx context.Context, pods []*v1.Pod) ([]commonIL.PodStatus, error)

	// Cancel stops/cancels a running or pending job
	Cancel(ctx context.Context, podUID string) error

	// GetLogs retrieves logs for a specific container in a job
	// If follow is true, it should stream logs as they're written
	GetLogs(ctx context.Context, podUID, containerName string, follow bool, tailLines int) (io.Reader, error)

	// SystemInfo returns health/status information about the batch system
	// Returns a string describing system state (e.g., cluster health, available resources)
	SystemInfo(ctx context.Context) (string, error)

	// CreateDirectories ensures necessary storage directories exist
	CreateDirectories() error

	// LoadJobs restores job metadata from persistent storage (if applicable)
	LoadJobs() error

	// GetJobID returns the batch system job ID for a given pod UID
	// Returns empty string if no job exists
	GetJobID(podUID string) string
}

// JobState represents the state of a job in the batch system
type JobState string

const (
	// JobStatePending indicates the job is queued but not yet running
	JobStatePending JobState = "pending"
	// JobStateRunning indicates the job is currently executing
	JobStateRunning JobState = "running"
	// JobStateCompleted indicates the job finished successfully
	JobStateCompleted JobState = "completed"
	// JobStateFailed indicates the job finished with an error
	JobStateFailed JobState = "failed"
	// JobStateCancelled indicates the job was cancelled
	JobStateCancelled JobState = "cancelled"
	// JobStateUnknown indicates the job state cannot be determined
	JobStateUnknown JobState = "unknown"
)
