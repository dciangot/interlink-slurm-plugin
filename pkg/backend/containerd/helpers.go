package containerd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/opencontainers/runtime-spec/specs-go"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// runContainer creates and starts a Containerd container for a Kubernetes container spec
func (c *ContainerdBackend) runContainer(ctx context.Context, pod *v1.Pod, container *v1.Container, filesPath string, isInit bool) (string, error) {
	ctx = namespaces.WithNamespace(ctx, c.Config.Namespace)
	image := c.prepareImage(container.Image)
	
	// Pull image if needed
	log.G(ctx).Debug("Ensuring image is available: ", image)
	if err := c.pullImageIfNeeded(ctx, image); err != nil {
		log.G(ctx).Warning("Failed to pull image (will try to use local): ", err)
	}

	img, err := c.Client.GetImage(ctx, image)
	if err != nil {
		return "", fmt.Errorf("failed to get image: %w", err)
	}

	containerID := fmt.Sprintf("interlink-%s-%s-%s", pod.Namespace, pod.Name, container.Name)
	
	// Prepare OCI spec options
	opts := []oci.SpecOpts{
		oci.WithImageConfig(img),
		oci.WithEnv(c.prepareEnvVars(container)),
	}

	// Add command and args if specified
	if len(container.Command) > 0 || len(container.Args) > 0 {
		cmd := container.Command
		if len(container.Args) > 0 {
			cmd = append(cmd, container.Args...)
		}
		opts = append(opts, oci.WithProcessArgs(cmd...))
	}

	// Add working directory if specified
	if container.WorkingDir != "" {
		opts = append(opts, oci.WithProcessCwd(container.WorkingDir))
	}

	// Add mounts
	mounts := c.prepareMounts(pod, container, filesPath)
	if len(mounts) > 0 {
		opts = append(opts, oci.WithMounts(mounts))
	}

	// Add resource limits
	resources := c.prepareResources(container)
	if resources != nil {
		opts = append(opts, oci.WithResources(resources))
	}

	// Create log file
	logPath := filepath.Join(filesPath, container.Name+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return "", fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFile.Close()

	// Create container
	ctr, err := c.Client.NewContainer(
		ctx,
		containerID,
		containerd.WithImage(img),
		containerd.WithNewSnapshot(containerID+"-snapshot", img),
		containerd.WithNewSpec(opts...),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	// Create and start task
	task, err := ctr.NewTask(ctx, cio.NewCreator(cio.WithStreams(nil, logFile, logFile)))
	if err != nil {
		ctr.Delete(ctx, containerd.WithSnapshotCleanup)
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	if err := task.Start(ctx); err != nil {
		task.Delete(ctx)
		ctr.Delete(ctx, containerd.WithSnapshotCleanup)
		return "", fmt.Errorf("failed to start task: %w", err)
	}

	log.G(ctx).Info("Started container ", container.Name, " with ID: ", containerID)
	return containerID, nil
}

// getContainerStatus retrieves the current status of a Containerd container
func (c *ContainerdBackend) getContainerStatus(ctx context.Context, containerID, containerName string, jobInfo *JobInfo) (v1.ContainerStatus, error) {
	ctx = namespaces.WithNamespace(ctx, c.Config.Namespace)
	
	ctr, err := c.Client.LoadContainer(ctx, containerID)
	if err != nil {
		return v1.ContainerStatus{}, fmt.Errorf("failed to load container: %w", err)
	}

	task, err := ctr.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			// Task not found means container was created but never started, or already cleaned up
			return v1.ContainerStatus{
				Name:  containerName,
				State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "ContainerCreating"}},
				Ready: false,
			}, nil
		}
		return v1.ContainerStatus{}, fmt.Errorf("failed to get task: %w", err)
	}

	status, err := task.Status(ctx)
	if err != nil {
		return v1.ContainerStatus{}, fmt.Errorf("failed to get task status: %w", err)
	}

	containerStatus := v1.ContainerStatus{
		Name:        containerName,
		ContainerID: "containerd://" + containerID,
	}

	// Map Containerd status to Kubernetes status
	switch status.Status {
	case containerd.Running:
		containerStatus.State = v1.ContainerState{
			Running: &v1.ContainerStateRunning{
				StartedAt: metav1.Time{Time: jobInfo.StartTime},
			},
		}
		containerStatus.Ready = true

	case containerd.Created, containerd.Paused:
		containerStatus.State = v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason: string(status.Status),
			},
		}
		containerStatus.Ready = false

	case containerd.Stopped:
		exitStatus, err := task.Wait(ctx)
		var exitCode int32
		if err == nil {
			select {
			case status := <-exitStatus:
				exitCode = int32(status.ExitCode())
			default:
				exitCode = 0
			}
		}

		finishedAt := time.Now()
		if jobInfo.EndTime.IsZero() {
			jobInfo.EndTime = finishedAt
			c.saveJobMetadata(jobInfo)
		} else {
			finishedAt = jobInfo.EndTime
		}

		containerStatus.State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode:   exitCode,
				StartedAt:  metav1.Time{Time: jobInfo.StartTime},
				FinishedAt: metav1.Time{Time: finishedAt},
				Reason:     "Completed",
			},
		}
		containerStatus.Ready = false

	default:
		containerStatus.State = v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason: string(status.Status),
			},
		}
		containerStatus.Ready = false
	}

	return containerStatus, nil
}

// waitForContainer waits for a container to finish (used for init containers)
func (c *ContainerdBackend) waitForContainer(ctx context.Context, containerID string) error {
	ctx = namespaces.WithNamespace(ctx, c.Config.Namespace)
	
	ctr, err := c.Client.LoadContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to load container: %w", err)
	}

	task, err := ctr.Task(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}

	exitStatusC, err := task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for task: %w", err)
	}

	exitStatus := <-exitStatusC
	if exitStatus.Error() != nil {
		return fmt.Errorf("task failed: %w", exitStatus.Error())
	}

	if exitStatus.ExitCode() != 0 {
		return fmt.Errorf("container exited with non-zero status: %d", exitStatus.ExitCode())
	}

	return nil
}

// cleanup stops and removes all containers for a job
func (c *ContainerdBackend) cleanup(ctx context.Context, jobInfo *JobInfo) error {
	ctx = namespaces.WithNamespace(ctx, c.Config.Namespace)
	var errors []string
	
	for containerName, containerID := range jobInfo.ContainerIDs {
		ctr, err := c.Client.LoadContainer(ctx, containerID)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				errors = append(errors, fmt.Sprintf("failed to load container %s: %v", containerName, err))
			}
			continue
		}

		task, err := ctr.Task(ctx, nil)
		if err == nil {
			// Kill the task
			if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
				log.G(ctx).Warning("Failed to kill task ", containerName, ": ", err)
			}

			// Wait for task to exit
			exitStatusC, err := task.Wait(ctx)
			if err == nil {
				select {
				case <-exitStatusC:
				case <-time.After(10 * time.Second):
					task.Kill(ctx, syscall.SIGKILL)
				}
			}

			// Delete task
			if _, err := task.Delete(ctx); err != nil {
				if !errdefs.IsNotFound(err) {
					errors = append(errors, fmt.Sprintf("failed to delete task %s: %v", containerName, err))
				}
			}
		}

		// Delete container
		if err := ctr.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
			if !errdefs.IsNotFound(err) {
				errors = append(errors, fmt.Sprintf("failed to delete container %s: %v", containerName, err))
			}
		}
	}
	
	// Remove from jobs map
	delete(c.Jobs, jobInfo.PodUID)
	
	// Remove metadata file
	metadataPath := filepath.Join(c.Config.DataRootFolder, ".metadata", jobInfo.PodUID+".json")
	if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
		log.G(ctx).Warning("Failed to remove metadata file: ", err)
	}
	
	if len(errors) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errors, "; "))
	}
	
	return nil
}

// prepareImage adds the image prefix if configured
func (c *ContainerdBackend) prepareImage(image string) string {
	if c.Config.ImagePrefix != "" && !strings.Contains(image, "/") {
		return c.Config.ImagePrefix + image
	}
	return image
}

// prepareEnvVars converts Kubernetes env vars to string slice
func (c *ContainerdBackend) prepareEnvVars(container *v1.Container) []string {
	var env []string
	for _, envVar := range container.Env {
		env = append(env, fmt.Sprintf("%s=%s", envVar.Name, envVar.Value))
	}
	return env
}

// prepareMounts converts Kubernetes volume mounts to OCI mounts
func (c *ContainerdBackend) prepareMounts(pod *v1.Pod, container *v1.Container, filesPath string) []specs.Mount {
	var mounts []specs.Mount
	
	for _, volumeMount := range container.VolumeMounts {
		var volume *v1.Volume
		for i := range pod.Spec.Volumes {
			if pod.Spec.Volumes[i].Name == volumeMount.Name {
				volume = &pod.Spec.Volumes[i]
				break
			}
		}
		
		if volume == nil {
			log.G(c.Ctx).Warning("Volume not found: ", volumeMount.Name)
			continue
		}
		
		var options []string
		if volumeMount.ReadOnly {
			options = []string{"ro", "rbind"}
		} else {
			options = []string{"rw", "rbind"}
		}
		
		if volume.HostPath != nil {
			mounts = append(mounts, specs.Mount{
				Destination: volumeMount.MountPath,
				Type:        "bind",
				Source:      volume.HostPath.Path,
				Options:     options,
			})
		} else if volume.EmptyDir != nil {
			emptyDirPath := filepath.Join(filesPath, "emptydir", volume.Name)
			os.MkdirAll(emptyDirPath, 0755)
			mounts = append(mounts, specs.Mount{
				Destination: volumeMount.MountPath,
				Type:        "bind",
				Source:      emptyDirPath,
				Options:     options,
			})
		}
	}
	
	return mounts
}

// prepareResources converts Kubernetes resource limits to OCI resources
func (c *ContainerdBackend) prepareResources(container *v1.Container) *specs.LinuxResources {
	resources := &specs.LinuxResources{}
	hasLimits := false

	cpuLimit := container.Resources.Limits.Cpu().MilliValue()
	if cpuLimit > 0 {
		quota := cpuLimit * 100 // Convert milli-CPU to quota (100000 = 1 CPU)
		period := uint64(100000)
		resources.CPU = &specs.LinuxCPU{
			Quota:  &quota,
			Period: &period,
		}
		hasLimits = true
	}

	memoryLimit := container.Resources.Limits.Memory().Value()
	if memoryLimit > 0 {
		resources.Memory = &specs.LinuxMemory{
			Limit: &memoryLimit,
		}
		hasLimits = true
	}

	if !hasLimits {
		return nil
	}
	return resources
}

// saveJobMetadata saves job information to disk
func (c *ContainerdBackend) saveJobMetadata(jobInfo *JobInfo) error {
	metadataDir := filepath.Join(c.Config.DataRootFolder, ".metadata")
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
func (c *ContainerdBackend) pullImageIfNeeded(ctx context.Context, image string) error {
	ctx = namespaces.WithNamespace(ctx, c.Config.Namespace)
	
	// Check if image exists locally
	_, err := c.Client.GetImage(ctx, image)
	if err == nil {
		return nil
	}
	
	// Image doesn't exist, pull it
	log.G(ctx).Info("Pulling image: ", image)
	_, err = c.Client.Pull(ctx, image, containerd.WithPullUnpack)
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	
	return nil
}
