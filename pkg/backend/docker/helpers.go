//go:build docker
// +build docker

package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	dockerclient "github.com/moby/moby/client"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// runContainer creates and starts a Docker container for a Kubernetes container spec
func (d *DockerBackend) runContainer(ctx context.Context, pod *v1.Pod, container *v1.Container, filesPath string, isInit bool) (string, error) {
	image := d.prepareImage(container.Image)

	// Pull image if needed
	log.G(ctx).Debug("Ensuring image is available: ", image)
	if err := d.pullImageIfNeeded(ctx, image); err != nil {
		log.G(ctx).Warning("Failed to pull image (will try to use local): ", err)
	}

	// Prepare environment variables
	env := d.prepareEnvVars(container)

	// Prepare command and args
	var cmd []string
	if len(container.Command) > 0 {
		cmd = container.Command
	}
	if len(container.Args) > 0 {
		cmd = append(cmd, container.Args...)
	}

	// Prepare mounts
	mounts := d.prepareMounts(pod, container, filesPath)

	// Prepare resource limits
	resources := d.prepareResources(container)

	// Container configuration
	config := &container.Config{
		Image:      image,
		Env:        env,
		WorkingDir: container.WorkingDir,
		Labels: map[string]string{
			"interlink.pod.uid":       string(pod.UID),
			"interlink.pod.name":      pod.Name,
			"interlink.pod.namespace": pod.Namespace,
			"interlink.container":     container.Name,
			"interlink.is-init":       strconv.FormatBool(isInit),
		},
	}

	if len(cmd) > 0 {
		config.Cmd = cmd
	}

	hostConfig := &container.HostConfig{
		Mounts:     mounts,
		Resources:  resources,
		AutoRemove: false,
	}

	// Add network if specified
	if d.Config.Network != "" {
		hostConfig.NetworkMode = container.NetworkMode(d.Config.Network)
	}

	containerName := fmt.Sprintf("interlink-%s-%s-%s", pod.Namespace, pod.Name, container.Name)

	resp, err := d.Client.ContainerCreate(ctx, config, hostConfig, nil, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	if err := d.Client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	log.G(ctx).Info("Started container ", container.Name, " with ID: ", resp.ID)
	return resp.ID, nil
}

// getContainerStatus retrieves the current status of a Docker container
func (d *DockerBackend) getContainerStatus(ctx context.Context, containerID, containerName string, jobInfo *JobInfo) (v1.ContainerStatus, error) {
	inspect, err := d.Client.ContainerInspect(ctx, containerID)
	if err != nil {
		return v1.ContainerStatus{}, fmt.Errorf("failed to inspect container: %w", err)
	}

	status := v1.ContainerStatus{
		Name:        containerName,
		ContainerID: "docker://" + containerID,
		Image:       inspect.Config.Image,
		ImageID:     inspect.Image,
	}

	// Map Docker state to Kubernetes state
	if inspect.State.Running {
		status.State = v1.ContainerState{
			Running: &v1.ContainerStateRunning{
				StartedAt: metav1.Time{Time: parseDockerTime(inspect.State.StartedAt)},
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
		exitCode := int32(inspect.State.ExitCode)
		finishedAt := parseDockerTime(inspect.State.FinishedAt)

		// Update job end time if not set
		if jobInfo.EndTime.IsZero() && !finishedAt.IsZero() {
			jobInfo.EndTime = finishedAt
			d.saveJobMetadata(jobInfo)
		}

		status.State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode:   exitCode,
				StartedAt:  metav1.Time{Time: parseDockerTime(inspect.State.StartedAt)},
				FinishedAt: metav1.Time{Time: finishedAt},
				Reason:     inspect.State.Status,
			},
		}
		status.Ready = false
	}

	return status, nil
}

// waitForContainer waits for a container to finish (used for init containers)
func (d *DockerBackend) waitForContainer(ctx context.Context, containerID string) error {
	statusCh, errCh := d.Client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("error waiting for container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("container exited with non-zero status: %d", status.StatusCode)
		}
	}

	return nil
}

// cleanup stops and removes all containers for a job
func (d *DockerBackend) cleanup(ctx context.Context, jobInfo *JobInfo) error {
	var errors []string

	for containerName, containerID := range jobInfo.ContainerIDs {
		timeout := 10
		stopOptions := container.StopOptions{
			Timeout: &timeout,
		}

		if err := d.Client.ContainerStop(ctx, containerID, stopOptions); err != nil {
			if !dockerclient.IsErrNotFound(err) {
				errors = append(errors, fmt.Sprintf("failed to stop container %s: %v", containerName, err))
				log.G(ctx).Warning("Failed to stop container ", containerName, ": ", err)
			}
		}

		if err := d.Client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
			if !dockerclient.IsErrNotFound(err) {
				errors = append(errors, fmt.Sprintf("failed to remove container %s: %v", containerName, err))
				log.G(ctx).Warning("Failed to remove container ", containerName, ": ", err)
			}
		}
	}

	// Remove from jobs map
	delete(d.Jobs, jobInfo.PodUID)

	// Remove metadata file
	metadataPath := filepath.Join(d.Config.DataRootFolder, ".metadata", jobInfo.PodUID+".json")
	if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
		log.G(ctx).Warning("Failed to remove metadata file: ", err)
	}

	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errors, "; "))
	}

	return nil
}

// prepareImage adds the image prefix if configured
func (d *DockerBackend) prepareImage(image string) string {
	if d.Config.ImagePrefix != "" && !strings.Contains(image, "/") {
		return d.Config.ImagePrefix + image
	}
	return image
}

// prepareEnvVars converts Kubernetes env vars to Docker format
func (d *DockerBackend) prepareEnvVars(container *v1.Container) []string {
	var env []string
	for _, envVar := range container.Env {
		env = append(env, fmt.Sprintf("%s=%s", envVar.Name, envVar.Value))
	}
	return env
}

// prepareMounts converts Kubernetes volume mounts to Docker mounts
func (d *DockerBackend) prepareMounts(pod *v1.Pod, container *v1.Container, filesPath string) []mount.Mount {
	var mounts []mount.Mount

	// Always mount the working directory for logs and metadata
	mounts = append(mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: filesPath,
		Target: "/interlink",
	})

	for _, volumeMount := range container.VolumeMounts {
		// Find the corresponding volume in the pod spec
		var volume *v1.Volume
		for i := range pod.Spec.Volumes {
			if pod.Spec.Volumes[i].Name == volumeMount.Name {
				volume = &pod.Spec.Volumes[i]
				break
			}
		}

		if volume == nil {
			log.G(d.Ctx).Warning("Volume not found: ", volumeMount.Name)
			continue
		}

		// Handle different volume types
		if volume.HostPath != nil {
			readOnly := volumeMount.ReadOnly
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeBind,
				Source:   volume.HostPath.Path,
				Target:   volumeMount.MountPath,
				ReadOnly: readOnly,
			})
		} else if volume.EmptyDir != nil {
			// Create empty dir in filesPath
			emptyDirPath := filepath.Join(filesPath, "emptydir", volume.Name)
			os.MkdirAll(emptyDirPath, 0755)
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeBind,
				Source: emptyDirPath,
				Target: volumeMount.MountPath,
			})
		}
		// ConfigMaps and Secrets would need special handling if ExportPodData is enabled
	}

	return mounts
}

// prepareResources converts Kubernetes resource limits to Docker resource constraints
func (d *DockerBackend) prepareResources(container *v1.Container) container.Resources {
	resources := container.Resources{}

	cpuLimit := resources.Limits.Cpu().MilliValue()
	memoryLimit := resources.Limits.Memory().Value()

	return container.Resources{
		NanoCPUs: cpuLimit * 1000000, // Convert milli-CPU to nano-CPU
		Memory:   memoryLimit,
	}
}

// saveJobMetadata saves job information to disk
func (d *DockerBackend) saveJobMetadata(jobInfo *JobInfo) error {
	metadataDir := filepath.Join(d.Config.DataRootFolder, ".metadata")
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

// pullImageIfNeeded pulls a Docker image if it's not already present
func (d *DockerBackend) pullImageIfNeeded(ctx context.Context, image string) error {
	// Check if image exists locally
	_, _, err := d.Client.ImageInspectWithRaw(ctx, image)
	if err == nil {
		// Image exists, no need to pull
		return nil
	}

	// Image doesn't exist, try to pull it
	log.G(ctx).Info("Pulling image: ", image)
	reader, err := d.Client.ImagePull(ctx, image, dockerclient.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()

	// Consume the pull output (required for pull to complete)
	buf := make([]byte, 1024)
	for {
		_, err := reader.Read(buf)
		if err != nil {
			break
		}
	}

	return nil
}

// parseDockerTime parses Docker's timestamp format
func parseDockerTime(timeStr string) time.Time {
	if timeStr == "" || timeStr == "0001-01-01T00:00:00Z" {
		return time.Time{}
	}

	t, err := time.Parse(time.RFC3339Nano, timeStr)
	if err != nil {
		return time.Time{}
	}

	return t
}
