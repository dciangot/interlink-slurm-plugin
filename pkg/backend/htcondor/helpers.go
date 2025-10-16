package htcondor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// executeCommand executes a shell command and returns the output
func (h *HTCondorBackend) executeCommand(ctx context.Context, cmdStr string) (string, error) {
	log.G(ctx).Debugf("Executing command: %s", cmdStr)
	
	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		log.G(ctx).Errorf("Command failed: %s\nOutput: %s\nError: %v", cmdStr, string(output), err)
		return string(output), fmt.Errorf("command execution failed: %w", err)
	}
	
	log.G(ctx).Debugf("Command output: %s", string(output))
	return string(output), nil
}

// validateConfig validates the HTCondor configuration
func (h *HTCondorBackend) validateConfig() error {
	if h.Config.CondorSubmitPath == "" {
		return fmt.Errorf("CondorSubmitPath is required")
	}
	
	if h.Config.CondorQPath == "" {
		return fmt.Errorf("CondorQPath is required")
	}
	
	if h.Config.CondorRmPath == "" {
		return fmt.Errorf("CondorRmPath is required")
	}
	
	if h.Config.CondorHistoryPath == "" {
		return fmt.Errorf("CondorHistoryPath is required")
	}
	
	if h.Config.SingularityPath == "" {
		return fmt.Errorf("SingularityPath is required")
	}
	
	if h.Config.SpoolDirectory == "" {
		return fmt.Errorf("SpoolDirectory is required")
	}
	
	return nil
}

// generateJobID generates a unique job identifier
func (h *HTCondorBackend) generateJobID(podUID, containerName string) string {
	return fmt.Sprintf("%s-%s", podUID, containerName)
}

// parseJobID extracts pod UID and container name from a job ID
func (h *HTCondorBackend) parseJobID(jobID string) (podUID, containerName string, err error) {
	parts := strings.SplitN(jobID, "-", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid job ID format: %s", jobID)
	}
	return parts[0], parts[1], nil
}

// isJobCompleted checks if a job has completed (successfully or not)
func (h *HTCondorBackend) isJobCompleted(state HTCondorJobState) bool {
	return state == JobStateCompleted || state == JobStateRemoved
}

// isJobRunning checks if a job is currently running
func (h *HTCondorBackend) isJobRunning(state HTCondorJobState) bool {
	return state == JobStateRunning
}

// isJobPending checks if a job is pending
func (h *HTCondorBackend) isJobPending(state HTCondorJobState) bool {
	return state == JobStateIdle || state == JobStateHeld || state == JobStateSuspended
}

// buildEnvironmentVariables builds environment variable strings for a container
func (h *HTCondorBackend) buildEnvironmentVariables(container v1.Container) []string {
	var envVars []string
	
	for _, env := range container.Env {
		envVars = append(envVars, fmt.Sprintf("%s=%s", env.Name, env.Value))
	}
	
	return envVars
}

// sanitizeJobName sanitizes a job name for use in HTCondor
func (h *HTCondorBackend) sanitizeJobName(name string) string {
	// Replace invalid characters with underscores
	replacer := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	
	return replacer.Replace(name)
}

// formatResourceLimits formats resource limits for HTCondor submit file
func (h *HTCondorBackend) formatResourceLimits(container v1.Container) (cpu string, memory string) {
	cpuLimit := container.Resources.Limits.Cpu().AsApproximateFloat64()
	memoryLimit := container.Resources.Limits.Memory().Value() / (1024 * 1024) // Convert to MB
	
	if cpuLimit > 0 {
		cpu = fmt.Sprintf("%d", int(cpuLimit)+1)
	} else {
		cpu = "1"
	}
	
	if memoryLimit > 0 {
		memory = fmt.Sprintf("%d MB", memoryLimit)
	} else {
		memory = "1024 MB"
	}
	
	return cpu, memory
}

// createPodCondition creates a new pod condition
func createPodCondition(conditionType v1.PodConditionType, status v1.ConditionStatus, reason, message string) v1.PodCondition {
	return v1.PodCondition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// createContainerStatus creates a container status with the given state
func createContainerStatus(name, image string, state v1.ContainerState, ready bool) v1.ContainerStatus {
	return v1.ContainerStatus{
		Name:  name,
		Image: image,
		State: state,
		Ready: ready,
	}
}

// retryOperation retries an operation with exponential backoff
func (h *HTCondorBackend) retryOperation(
	ctx context.Context,
	operation func() error,
	maxRetries int,
	initialDelay time.Duration,
) error {
	delay := initialDelay
	
	for i := 0; i < maxRetries; i++ {
		err := operation()
		if err == nil {
			return nil
		}
		
		log.G(ctx).Warnf("Operation failed (attempt %d/%d): %v", i+1, maxRetries, err)
		
		if i < maxRetries-1 {
			log.G(ctx).Debugf("Retrying in %v...", delay)
			time.Sleep(delay)
			delay *= 2 // Exponential backoff
		}
	}
	
	return fmt.Errorf("operation failed after %d attempts", maxRetries)
}

// validatePodSpec validates that the pod spec is compatible with HTCondor backend
func (h *HTCondorBackend) validatePodSpec(pod *v1.Pod) error {
	// Check for unsupported volume types
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			return fmt.Errorf("PersistentVolumeClaim volumes are not supported in HTCondor backend")
		}
		
		if volume.NFS != nil {
			return fmt.Errorf("NFS volumes are not supported in HTCondor backend")
		}
		
		if volume.CSI != nil {
			return fmt.Errorf("CSI volumes are not supported in HTCondor backend")
		}
	}
	
	return nil
}

// extractPodAnnotation extracts a specific annotation from a pod
func extractPodAnnotation(pod *v1.Pod, key string) (string, bool) {
	if pod.Annotations == nil {
		return "", false
	}
	
	value, ok := pod.Annotations[key]
	return value, ok
}

// buildJobMetadata builds metadata for a job submission
func (h *HTCondorBackend) buildJobMetadata(pod *v1.Pod, container v1.Container) map[string]string {
	metadata := map[string]string{
		"pod_name":       pod.Name,
		"pod_namespace":  pod.Namespace,
		"pod_uid":        string(pod.UID),
		"container_name": container.Name,
		"image":          container.Image,
	}
	
	// Add labels as metadata
	for key, value := range pod.Labels {
		metadata[fmt.Sprintf("label_%s", key)] = value
	}
	
	return metadata
}

// formatDuration formats a duration in a human-readable format
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// parseTimeString parses a time string from HTCondor output
func parseTimeString(timeStr string) (time.Time, error) {
	// HTCondor typically uses Unix timestamps
	// This is a simplified implementation
	return time.Parse(time.RFC3339, timeStr)
}

// mergeStringMaps merges multiple string maps into one
func mergeStringMaps(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	
	return result
}

// containsString checks if a slice contains a specific string
func containsString(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

// removeString removes a string from a slice
func removeString(slice []string, str string) []string {
	result := []string{}
	for _, s := range slice {
		if s != str {
			result = append(result, s)
		}
	}
	return result
}

// escapeShellArg escapes a string for safe use in shell commands
func escapeShellArg(arg string) string {
	// Simple escaping - wrap in single quotes and escape existing single quotes
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

// buildSingularityBindString builds a bind mount string for Singularity
func buildSingularityBindString(hostPath, containerPath string, readOnly bool) string {
	bind := fmt.Sprintf("%s:%s", hostPath, containerPath)
	if readOnly {
		bind += ":ro"
	}
	return bind
}

// isContainerInPod checks if a container is part of a pod spec
func isContainerInPod(pod *v1.Pod, containerName string) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == containerName {
			return true
		}
	}
	
	for _, container := range pod.Spec.InitContainers {
		if container.Name == containerName {
			return true
		}
	}
	
	return false
}

// getContainerFromPod retrieves a container by name from a pod spec
func getContainerFromPod(pod *v1.Pod, containerName string) (*v1.Container, bool) {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == containerName {
			return &pod.Spec.Containers[i], false
		}
	}
	
	for i := range pod.Spec.InitContainers {
		if pod.Spec.InitContainers[i].Name == containerName {
			return &pod.Spec.InitContainers[i], true
		}
	}
	
	return nil, false
}

// logJobSubmission logs information about a job submission
func (h *HTCondorBackend) logJobSubmission(ctx context.Context, jobID, podName, containerName string) {
	log.G(ctx).Infof("HTCondor Job Submitted:")
	log.G(ctx).Infof("  Job ID: %s", jobID)
	log.G(ctx).Infof("  Pod: %s", podName)
	log.G(ctx).Infof("  Container: %s", containerName)
}

// logJobCompletion logs information about a job completion
func (h *HTCondorBackend) logJobCompletion(ctx context.Context, jobID string, exitCode int, duration time.Duration) {
	log.G(ctx).Infof("HTCondor Job Completed:")
	log.G(ctx).Infof("  Job ID: %s", jobID)
	log.G(ctx).Infof("  Exit Code: %d", exitCode)
	log.G(ctx).Infof("  Duration: %s", formatDuration(duration))
}

// logJobError logs information about a job error
func (h *HTCondorBackend) logJobError(ctx context.Context, jobID string, err error) {
	log.G(ctx).Errorf("HTCondor Job Error:")
	log.G(ctx).Errorf("  Job ID: %s", jobID)
	log.G(ctx).Errorf("  Error: %v", err)
}
