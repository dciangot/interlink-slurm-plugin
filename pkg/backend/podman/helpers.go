package podman

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// createPodmanPod creates a Podman pod (when UsePods is enabled)
func (p *PodmanBackend) createPodmanPod(ctx context.Context, pod *v1.Pod) (string, error) {
	podCreate := map[string]interface{}{
		"name": fmt.Sprintf("interlink-%s-%s", pod.Namespace, pod.Name),
		"labels": map[string]string{
			"interlink.pod.uid":       string(pod.UID),
			"interlink.pod.name":      pod.Name,
			"interlink.pod.namespace": pod.Namespace,
		},
	}

	body, err := json.Marshal(podCreate)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pod create: %w", err)
	}

	resp, err := p.doRequest(ctx, "POST", "/v3.0.0/libpod/pods/create", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		return "", fmt.Errorf("failed to create pod: status %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	log.G(ctx).Info("Created Podman pod: ", result.ID)
	return result.ID, nil
}

// executeJobScript runs a custom job script in a bash container
func (p *PodmanBackend) executeJobScript(ctx context.Context, pod *v1.Pod, script string, filesPath string, podmanPodID string) (string, error) {
	scriptPath := filepath.Join(filesPath, "jobScript.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("failed to write job script: %w", err)
	}

	image := p.prepareImage("bash:latest")
	
	// Pull image if needed
	if err := p.pullImageIfNeeded(ctx, image); err != nil {
		log.G(ctx).Warning("Failed to pull image: ", err)
	}

	containerCreate := map[string]interface{}{
		"image":   image,
		"command": []string{"bash", "/job/jobScript.sh"},
		"work_dir": "/job",
		"labels": map[string]string{
			"interlink.pod.uid":       string(pod.UID),
			"interlink.pod.name":      pod.Name,
			"interlink.pod.namespace": pod.Namespace,
			"interlink.container":     "jobscript",
		},
		"mounts": []map[string]interface{}{
			{
				"type":        "bind",
				"source":      filesPath,
				"destination": "/job",
			},
		},
	}

	if podmanPodID != "" {
		containerCreate["pod"] = podmanPodID
	}

	body, err := json.Marshal(containerCreate)
	if err != nil {
		return "", fmt.Errorf("failed to marshal container create: %w", err)
	}

	resp, err := p.doRequest(ctx, "POST", "/v3.0.0/libpod/containers/create", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		return "", fmt.Errorf("failed to create container: status %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Start container
	startResp, err := p.doRequest(ctx, "POST", fmt.Sprintf("/v3.0.0/libpod/containers/%s/start", result.ID), nil)
	if err != nil {
		return "", err
	}
	defer startResp.Body.Close()

	if startResp.StatusCode != 204 {
		return "", fmt.Errorf("failed to start container: status %d", startResp.StatusCode)
	}

	log.G(ctx).Info("Started job script container: ", result.ID)
	return result.ID, nil
}

// runContainer creates and starts a Podman container for a Kubernetes container spec
func (p *PodmanBackend) runContainer(ctx context.Context, pod *v1.Pod, container *v1.Container, filesPath string, isInit bool, podmanPodID string) (string, error) {
	image := p.prepareImage(container.Image)
	
	// Pull image if needed
	if err := p.pullImageIfNeeded(ctx, image); err != nil {
		log.G(ctx).Warning("Failed to pull image: ", err)
	}

	// Prepare container creation request
	containerCreate := map[string]interface{}{
		"image":   image,
		"env":     p.prepareEnvVars(container),
		"labels": map[string]string{
			"interlink.pod.uid":       string(pod.UID),
			"interlink.pod.name":      pod.Name,
			"interlink.pod.namespace": pod.Namespace,
			"interlink.container":     container.Name,
			"interlink.is-init":       fmt.Sprintf("%t", isInit),
		},
	}

	// Add command and args
	if len(container.Command) > 0 || len(container.Args) > 0 {
		cmd := container.Command
		if len(container.Args) > 0 {
			cmd = append(cmd, container.Args...)
		}
		containerCreate["command"] = cmd
	}

	// Add working directory
	if container.WorkingDir != "" {
		containerCreate["work_dir"] = container.WorkingDir
	}

	// Add mounts
	mounts := p.prepareMounts(pod, container, filesPath)
	if len(mounts) > 0 {
		containerCreate["mounts"] = mounts
	}

	// Add resources
	resources := p.prepareResources(container)
	if resources != nil {
		containerCreate["resource_limits"] = resources
	}

	// Add to pod if using pods
	if podmanPodID != "" {
		containerCreate["pod"] = podmanPodID
	}

	// Add network mode if specified and not using pods
	if p.Config.NetworkMode != "" && podmanPodID == "" {
		containerCreate["netns"] = map[string]string{
			"nsmode": p.Config.NetworkMode,
		}
	}

	body, err := json.Marshal(containerCreate)
	if err != nil {
		return "", fmt.Errorf("failed to marshal container create: %w", err)
	}

	resp, err := p.doRequest(ctx, "POST", "/v3.0.0/libpod/containers/create", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create container: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Start container
	startResp, err := p.doRequest(ctx, "POST", fmt.Sprintf("/v3.0.0/libpod/containers/%s/start", result.ID), nil)
	if err != nil {
		return "", err
	}
	defer startResp.Body.Close()

	if startResp.StatusCode != 204 {
		return "", fmt.Errorf("failed to start container: status %d", startResp.StatusCode)
	}

	log.G(ctx).Info("Started container ", container.Name, " with ID: ", result.ID)
	return result.ID, nil
}

// getContainerStatus retrieves the current status of a Podman container
func (p *PodmanBackend) getContainerStatus(ctx context.Context, containerID, containerName string, jobInfo *JobInfo) (v1.ContainerStatus, error) {
	resp, err := p.doRequest(ctx, "GET", fmt.Sprintf("/v3.0.0/libpod/containers/%s/json", containerID), nil)
	if err != nil {
		return v1.ContainerStatus{}, fmt.Errorf("failed to inspect container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return v1.ContainerStatus{}, fmt.Errorf("inspect failed: status %d", resp.StatusCode)
	}

	var inspect PodmanContainerInspect
	if err := json.NewDecoder(resp.Body).Decode(&inspect); err != nil {
		return v1.ContainerStatus{}, fmt.Errorf("failed to decode inspect: %w", err)
	}

	status := v1.ContainerStatus{
		Name:        containerName,
		ContainerID: "podman://" + containerID,
		Image:       inspect.Config.Image,
	}

	// Map Podman state to Kubernetes state
	if inspect.State.Running {
		startedAt, _ := time.Parse(time.RFC3339Nano, inspect.State.StartedAt)
		status.State = v1.ContainerState{
			Running: &v1.ContainerStateRunning{
				StartedAt: metav1.Time{Time: startedAt},
			},
		}
		status.Ready = true
	} else if inspect.State.Status == "created" {
		status.State = v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason: "ContainerCreating",
			},
		}
		status.Ready = false
	} else {
		// Container has terminated
		startedAt, _ := time.Parse(time.RFC3339Nano, inspect.State.StartedAt)
		finishedAt, _ := time.Parse(time.RFC3339Nano, inspect.State.FinishedAt)
		
		if jobInfo.EndTime.IsZero() && !finishedAt.IsZero() {
			jobInfo.EndTime = finishedAt
			p.saveJobMetadata(jobInfo)
		}

		status.State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode:   int32(inspect.State.ExitCode),
				StartedAt:  metav1.Time{Time: startedAt},
				FinishedAt: metav1.Time{Time: finishedAt},
				Reason:     inspect.State.Status,
			},
		}
		status.Ready = false
	}

	return status, nil
}

// waitForContainer waits for a container to finish (used for init containers)
func (p *PodmanBackend) waitForContainer(ctx context.Context, containerID string) error {
	resp, err := p.doRequest(ctx, "POST", fmt.Sprintf("/v3.0.0/libpod/containers/%s/wait", containerID), nil)
	if err != nil {
		return fmt.Errorf("failed to wait for container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("wait failed: status %d", resp.StatusCode)
	}

	var result struct {
		StatusCode int    `json:"StatusCode"`
		Error      string `json:"Error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode wait result: %w", err)
	}

	if result.Error != "" {
		return fmt.Errorf("container failed: %s", result.Error)
	}

	if result.StatusCode != 0 {
		return fmt.Errorf("container exited with non-zero status: %d", result.StatusCode)
	}

	return nil
}

// cleanup stops and removes all containers for a job
func (p *PodmanBackend) cleanup(ctx context.Context, jobInfo *JobInfo) error {
	var errors []string
	
	// Stop and remove containers
	for containerName, containerID := range jobInfo.ContainerIDs {
		// Stop container
		stopResp, err := p.doRequest(ctx, "POST", fmt.Sprintf("/v3.0.0/libpod/containers/%s/stop", containerID), nil)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to stop container %s: %v", containerName, err))
		} else {
			stopResp.Body.Close()
		}

		// Remove container
		removeResp, err := p.doRequest(ctx, "DELETE", fmt.Sprintf("/v3.0.0/libpod/containers/%s?force=true", containerID), nil)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to remove container %s: %v", containerName, err))
		} else {
			removeResp.Body.Close()
		}
	}

	// Remove Podman pod if it exists
	if jobInfo.PodmanPodID != "" {
		podResp, err := p.doRequest(ctx, "DELETE", fmt.Sprintf("/v3.0.0/libpod/pods/%s?force=true", jobInfo.PodmanPodID), nil)
		if err != nil {
			errors = append(errors, fmt.Sprintf("failed to remove pod: %v", err))
		} else {
			podResp.Body.Close()
		}
	}
	
	// Remove from jobs map
	delete(p.Jobs, jobInfo.PodUID)
	
	// Remove metadata file
	metadataPath := filepath.Join(p.Config.DataRootFolder, ".metadata", jobInfo.PodUID+".json")
	if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
		log.G(ctx).Warning("Failed to remove metadata file: ", err)
	}
	
	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errors, "; "))
	}
	
	return nil
}

// prepareImage adds the image prefix if configured
func (p *PodmanBackend) prepareImage(image string) string {
	if p.Config.ImagePrefix != "" && !strings.Contains(image, "/") {
		return p.Config.ImagePrefix + image
	}
	return image
}

// prepareEnvVars converts Kubernetes env vars to string slice
func (p *PodmanBackend) prepareEnvVars(container *v1.Container) []string {
	var env []string
	for _, envVar := range container.Env {
		env = append(env, fmt.Sprintf("%s=%s", envVar.Name, envVar.Value))
	}
	return env
}

// prepareMounts converts Kubernetes volume mounts to Podman mounts
func (p *PodmanBackend) prepareMounts(pod *v1.Pod, container *v1.Container, filesPath string) []map[string]interface{} {
	var mounts []map[string]interface{}
	
	for _, volumeMount := range container.VolumeMounts {
		var volume *v1.Volume
		for i := range pod.Spec.Volumes {
			if pod.Spec.Volumes[i].Name == volumeMount.Name {
				volume = &pod.Spec.Volumes[i]
				break
			}
		}
		
		if volume == nil {
			log.G(p.Ctx).Warning("Volume not found: ", volumeMount.Name)
			continue
		}
		
		readOnly := volumeMount.ReadOnly
		
		if volume.HostPath != nil {
			mounts = append(mounts, map[string]interface{}{
				"type":        "bind",
				"source":      volume.HostPath.Path,
				"destination": volumeMount.MountPath,
				"read_only":   readOnly,
			})
		} else if volume.EmptyDir != nil {
			emptyDirPath := filepath.Join(filesPath, "emptydir", volume.Name)
			os.MkdirAll(emptyDirPath, 0755)
			mounts = append(mounts, map[string]interface{}{
				"type":        "bind",
				"source":      emptyDirPath,
				"destination": volumeMount.MountPath,
				"read_only":   readOnly,
			})
		}
	}
	
	return mounts
}

// prepareResources converts Kubernetes resource limits to Podman resource constraints
func (p *PodmanBackend) prepareResources(container *v1.Container) map[string]interface{} {
	resources := make(map[string]interface{})
	hasLimits := false

	cpuLimit := container.Resources.Limits.Cpu().MilliValue()
	if cpuLimit > 0 {
		// Convert milli-CPU to quota
		resources["cpu"] = map[string]interface{}{
			"quota":  cpuLimit * 1000, // Convert to microseconds
			"period": 100000,
		}
		hasLimits = true
	}

	memoryLimit := container.Resources.Limits.Memory().Value()
	if memoryLimit > 0 {
		resources["memory"] = map[string]interface{}{
			"limit": memoryLimit,
		}
		hasLimits = true
	}

	if !hasLimits {
		return nil
	}
	return resources
}

// saveJobMetadata saves job information to disk
func (p *PodmanBackend) saveJobMetadata(jobInfo *JobInfo) error {
	metadataDir := filepath.Join(p.Config.DataRootFolder, ".metadata")
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		return fmt.Errorf("failed to create metadata directory: %w", err)
	}
	
	data, err := json.MarshalIndent(jobInfo, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal job metadata: %w", err)
	}
	
	metadataPath := filepath.Join(metadataDir, jobInfo.PodUID+".json")
	if err := os.WriteFile(metadataPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write job metadata: %w", err)
	}
	
	return nil
}

// pullImageIfNeeded pulls a container image if it's not already present
func (p *PodmanBackend) pullImageIfNeeded(ctx context.Context, image string) error {
	// Check if image exists locally
	resp, err := p.doRequest(ctx, "GET", fmt.Sprintf("/v3.0.0/libpod/images/%s/exists", image), nil)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 204 {
			// Image exists
			return nil
		}
	}
	
	// Pull image
	log.G(ctx).Info("Pulling image: ", image)
	pullResp, err := p.doRequest(ctx, "POST", fmt.Sprintf("/v3.0.0/libpod/images/pull?reference=%s", image), nil)
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer pullResp.Body.Close()
	
	if pullResp.StatusCode != 200 {
		return fmt.Errorf("pull failed: status %d", pullResp.StatusCode)
	}
	
	return nil
}
