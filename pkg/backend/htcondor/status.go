//go:build htcondor
// +build htcondor

package htcondor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	v1 "k8s.io/api/core/v1"
)

// queryJobStatus queries HTCondor for the status of a specific job
func (h *HTCondorBackend) queryJobStatus(ctx context.Context, jobID string) (*HTCondorJob, error) {
	// First try condor_q for active jobs
	cmd := fmt.Sprintf("%s -json %s", h.config.CondorQPath, jobID)
	
	log.G(ctx).Debugf("Querying job status: %s", cmd)

	output, err := h.executeCommand(ctx, cmd)
	if err == nil && output != "" {
		// Parse condor_q JSON output
		jobInfo, err := h.parseCondorQJSON(output)
		if err == nil {
			return jobInfo, nil
		}
		log.G(ctx).Debugf("Failed to parse condor_q output: %v", err)
	}

	// If not found in queue, try condor_history for completed jobs
	cmd = fmt.Sprintf("%s -json %s", h.config.CondorHistoryPath, jobID)
	
	log.G(ctx).Debugf("Querying job history: %s", cmd)

	output, err = h.executeCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("job not found in queue or history: %s", jobID)
	}

	// Parse condor_history JSON output
	jobInfo, err := h.parseCondorHistoryJSON(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse condor_history output: %w", err)
	}

	return jobInfo, nil
}

// parseCondorQJSON parses JSON output from condor_q
func (h *HTCondorBackend) parseCondorQJSON(output string) (*HTCondorJob, error) {
	// Simple JSON parsing - looking for JobStatus field
	// Full implementation would use encoding/json
	
	jobInfo := &HTCondorJob{}
	
	// Extract ClusterId
	if clusterID := extractJSONField(output, "ClusterId"); clusterID != "" {
		jobInfo.ClusterID = clusterID
	}
	
	// Extract ProcId
	if procID := extractJSONField(output, "ProcId"); procID != "" {
		jobInfo.ProcID = procID
	}
	
	// Extract JobStatus
	if statusStr := extractJSONField(output, "JobStatus"); statusStr != "" {
		status, _ := strconv.Atoi(statusStr)
		jobInfo.Status = HTCondorJobState(status)
	}
	
	// Extract ExitCode if present
	if exitCode := extractJSONField(output, "ExitCode"); exitCode != "" {
		code, _ := strconv.Atoi(exitCode)
		jobInfo.ExitCode = code
	}
	
	// Extract resource usage
	if cpuUsage := extractJSONField(output, "RemoteUserCpu"); cpuUsage != "" {
		usage, _ := strconv.ParseFloat(cpuUsage, 64)
		jobInfo.CPUUsage = usage
	}
	
	if memUsage := extractJSONField(output, "ResidentSetSize"); memUsage != "" {
		usage, _ := strconv.ParseInt(memUsage, 10, 64)
		jobInfo.MemoryUsage = usage
	}
	
	jobInfo.LastUpdate = time.Now()
	
	return jobInfo, nil
}

// parseCondorHistoryJSON parses JSON output from condor_history
func (h *HTCondorBackend) parseCondorHistoryJSON(output string) (*HTCondorJob, error) {
	// Same parsing as condor_q, but marks job as historical
	jobInfo, err := h.parseCondorQJSON(output)
	if err != nil {
		return nil, err
	}
	
	// Jobs in history are completed
	if jobInfo.Status == JobStateIdle || jobInfo.Status == JobStateRunning {
		jobInfo.Status = JobStateCompleted
	}
	
	return jobInfo, nil
}

// extractJSONField extracts a field value from simple JSON output
// This is a basic implementation - for production, use encoding/json
func extractJSONField(jsonStr, fieldName string) string {
	// Look for "FieldName": value pattern
	searchStr := fmt.Sprintf("\"%s\":", fieldName)
	idx := strings.Index(jsonStr, searchStr)
	if idx == -1 {
		return ""
	}
	
	// Find the value after the colon
	start := idx + len(searchStr)
	remaining := jsonStr[start:]
	
	// Skip whitespace
	remaining = strings.TrimLeft(remaining, " 	
")
	
	// Extract value (up to comma or closing brace)
	var value string
	if strings.HasPrefix(remaining, "\"") {
		// String value
		endIdx := strings.Index(remaining[1:], "\"")
		if endIdx == -1 {
			return ""
		}
		value = remaining[1 : endIdx+1]
	} else {
		// Numeric value
		endIdx := strings.IndexAny(remaining, ",
}")
		if endIdx == -1 {
			endIdx = len(remaining)
		}
		value = strings.TrimSpace(remaining[:endIdx])
	}
	
	return value
}

// translateHTCondorStateToPodPhase converts HTCondor job state to Kubernetes pod phase
func (h *HTCondorBackend) translateHTCondorStateToPodPhase(state HTCondorJobState) v1.PodPhase {
	switch state {
	case JobStateIdle:
		return v1.PodPending
	case JobStateRunning:
		return v1.PodRunning
	case JobStateRemoved:
		return v1.PodFailed
	case JobStateCompleted:
		return v1.PodSucceeded
	case JobStateHeld:
		return v1.PodPending
	case JobStateSuspended:
		return v1.PodPending
	default:
		return v1.PodUnknown
	}
}

// translateHTCondorStateToContainerState converts HTCondor job state to container state
func (h *HTCondorBackend) translateHTCondorStateToContainerState(jobInfo *HTCondorJob) v1.ContainerState {
	switch jobInfo.Status {
	case JobStateIdle, JobStateHeld, JobStateSuspended:
		return v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason:  "HTCondorJobPending",
				Message: fmt.Sprintf("Job %s.%s is in %s state", jobInfo.ClusterID, jobInfo.ProcID, jobInfo.Status.String()),
			},
		}
	case JobStateRunning:
		return v1.ContainerState{
			Running: &v1.ContainerStateRunning{
				StartedAt: jobInfo.StartTime,
			},
		}
	case JobStateCompleted:
		if jobInfo.ExitCode == 0 {
			return v1.ContainerState{
				Terminated: &v1.ContainerStateTerminated{
					ExitCode:   int32(jobInfo.ExitCode),
					Reason:     "Completed",
					StartedAt:  jobInfo.StartTime,
					FinishedAt: jobInfo.CompletionTime,
				},
			}
		}
		return v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode:   int32(jobInfo.ExitCode),
				Reason:     "Error",
				Message:    fmt.Sprintf("Container exited with code %d", jobInfo.ExitCode),
				StartedAt:  jobInfo.StartTime,
				FinishedAt: jobInfo.CompletionTime,
			},
		}
	case JobStateRemoved:
		return v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode:   137, // SIGKILL
				Reason:     "Removed",
				Message:    "Job was removed from HTCondor queue",
				StartedAt:  jobInfo.StartTime,
				FinishedAt: jobInfo.CompletionTime,
			},
		}
	default:
		return v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason:  "Unknown",
				Message: fmt.Sprintf("Unknown HTCondor state: %s", jobInfo.Status.String()),
			},
		}
	}
}

// buildPodStatus constructs a Kubernetes PodStatus from HTCondor job information
func (h *HTCondorBackend) buildPodStatus(ctx context.Context, pod *v1.Pod, jobIDs map[string]string) v1.PodStatus {
	status := v1.PodStatus{
		Phase: v1.PodPending,
		Conditions: []v1.PodCondition{
			{
				Type:               v1.PodScheduled,
				Status:             v1.ConditionTrue,
				LastTransitionTime: pod.CreationTimestamp,
			},
		},
	}

	// Query status for all containers
	containerStatuses := []v1.ContainerStatus{}
	initContainerStatuses := []v1.ContainerStatus{}
	
	allContainers := append(pod.Spec.InitContainers, pod.Spec.Containers...)
	
	overallPhase := v1.PodSucceeded
	anyRunning := false
	anyFailed := false
	
	for _, container := range allContainers {
		jobID, exists := jobIDs[container.Name]
		if !exists {
			log.G(ctx).Warnf("No job ID found for container %s", container.Name)
			continue
		}
		
		jobInfo, err := h.queryJobStatus(ctx, jobID)
		if err != nil {
			log.G(ctx).Warnf("Failed to query status for job %s: %v", jobID, err)
			continue
		}
		
		containerStatus := v1.ContainerStatus{
			Name:  container.Name,
			Image: container.Image,
			State: h.translateHTCondorStateToContainerState(jobInfo),
			Ready: jobInfo.Status == JobStateRunning || jobInfo.Status == JobStateCompleted,
		}
		
		// Track overall pod phase
		if jobInfo.Status == JobStateRunning {
			anyRunning = true
		}
		if jobInfo.Status == JobStateRemoved || (jobInfo.Status == JobStateCompleted && jobInfo.ExitCode != 0) {
			anyFailed = true
		}
		
		// Add to appropriate list
		isInit := false
		for _, initContainer := range pod.Spec.InitContainers {
			if initContainer.Name == container.Name {
				isInit = true
				break
			}
		}
		
		if isInit {
			initContainerStatuses = append(initContainerStatuses, containerStatus)
		} else {
			containerStatuses = append(containerStatuses, containerStatus)
		}
	}
	
	// Determine overall pod phase
	if anyRunning {
		overallPhase = v1.PodRunning
	} else if anyFailed {
		overallPhase = v1.PodFailed
	}
	
	status.Phase = overallPhase
	status.ContainerStatuses = containerStatuses
	status.InitContainerStatuses = initContainerStatuses
	
	// Update pod conditions
	if overallPhase == v1.PodRunning {
		status.Conditions = append(status.Conditions, v1.PodCondition{
			Type:               v1.PodInitialized,
			Status:             v1.ConditionTrue,
			LastTransitionTime: pod.CreationTimestamp,
		})
		status.Conditions = append(status.Conditions, v1.PodCondition{
			Type:               v1.PodReady,
			Status:             v1.ConditionTrue,
			LastTransitionTime: pod.CreationTimestamp,
		})
	}
	
	return status
}

// monitorJobStatus monitors job status in the background
func (h *HTCondorBackend) monitorJobStatus(ctx context.Context, jobID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jobInfo, err := h.queryJobStatus(ctx, jobID)
			if err != nil {
				log.G(ctx).Warnf("Failed to query job status for %s: %v", jobID, err)
				continue
			}
			
			log.G(ctx).Debugf("Job %s status: %s (exit code: %d)", jobID, jobInfo.Status.String(), jobInfo.ExitCode)
			
			// Stop monitoring if job is completed or removed
			if jobInfo.Status == JobStateCompleted || jobInfo.Status == JobStateRemoved {
				log.G(ctx).Infof("Job %s finished with status %s", jobID, jobInfo.Status.String())
				return
			}
		}
	}
}

// String returns the string representation of HTCondor job state
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
	case JobStateSuspended:
		return "Suspended"
	case JobStateTransferringOutput:
		return "TransferringOutput"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}
