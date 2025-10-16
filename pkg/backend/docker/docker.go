package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
)

// NewDockerBackend creates a new Docker backend instance
func NewDockerBackend(ctx context.Context, config *DockerConfig) (*DockerBackend, error) {
	var cli *client.Client
	var err error

	if config.Endpoint != "" {
		cli, err = client.NewClientWithOpts(
			client.WithHost(config.Endpoint),
			client.WithAPIVersionNegotiation(),
		)
	} else {
		cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	backend := &DockerBackend{
		Config: config,
		Client: cli,
		Jobs:   make(map[string]*JobInfo),
		Ctx:    ctx,
	}

	return backend, nil
}

// Submit implements the BatchSystem interface for Docker
func (d *DockerBackend) Submit(ctx context.Context, podData *commonIL.RetrievedPodData) (string, error) {
	pod := podData.Pod
	filesPath := filepath.Join(d.Config.DataRootFolder, pod.Namespace+"-"+string(pod.UID))

	// Create working directory
	if err := os.MkdirAll(filesPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create working directory: %w", err)
	}

	// Initialize job info
	jobInfo := &JobInfo{
		PodUID:       string(pod.UID),
		PodName:      pod.Name,
		Namespace:    pod.Namespace,
		ContainerIDs: make(map[string]string),
		FilesPath:    filesPath,
	}

	// If a custom job script is provided, execute it directly
	if podData.JobScript != "" {
		containerID, err := d.executeJobScript(ctx, &pod, podData.JobScript, filesPath)
		if err != nil {
			os.RemoveAll(filesPath)
			return "", err
		}
		jobInfo.JobID = containerID
		jobInfo.ContainerIDs["jobscript"] = containerID
		d.Jobs[string(pod.UID)] = jobInfo
		
		// Save job metadata
		if err := d.saveJobMetadata(jobInfo); err != nil {
			log.G(ctx).Warning("Failed to save job metadata: ", err)
		}
		
		return containerID, nil
	}

	// Process init containers first
	for _, container := range pod.Spec.InitContainers {
		containerID, err := d.runContainer(ctx, &pod, &container, filesPath, true)
		if err != nil {
			d.cleanup(ctx, jobInfo)
			os.RemoveAll(filesPath)
			return "", fmt.Errorf("failed to run init container %s: %w", container.Name, err)
		}
		jobInfo.ContainerIDs[container.Name] = containerID

		// Wait for init container to complete
		if err := d.waitForContainer(ctx, containerID); err != nil {
			d.cleanup(ctx, jobInfo)
			os.RemoveAll(filesPath)
			return "", fmt.Errorf("init container %s failed: %w", container.Name, err)
		}
	}

	// Process regular containers
	for _, container := range pod.Spec.Containers {
		containerID, err := d.runContainer(ctx, &pod, &container, filesPath, false)
		if err != nil {
			d.cleanup(ctx, jobInfo)
			os.RemoveAll(filesPath)
			return "", fmt.Errorf("failed to run container %s: %w", container.Name, err)
		}
		jobInfo.ContainerIDs[container.Name] = containerID
		
		// Set primary job ID to first regular container
		if jobInfo.JobID == "" {
			jobInfo.JobID = containerID
		}
	}

	jobInfo.StartTime = time.Now()
	d.Jobs[string(pod.UID)] = jobInfo

	// Save job metadata
	if err := d.saveJobMetadata(jobInfo); err != nil {
		log.G(ctx).Warning("Failed to save job metadata: ", err)
	}

	log.G(ctx).Info("Successfully created Docker containers for pod ", pod.Name, " with primary job ID ", jobInfo.JobID)
	return jobInfo.JobID, nil
}

// executeJobScript runs a custom job script in a bash container
func (d *DockerBackend) executeJobScript(ctx context.Context, pod *v1.Pod, script string, filesPath string) (string, error) {
	scriptPath := filepath.Join(filesPath, "jobScript.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("failed to write job script: %w", err)
	}

	// Use bash image to execute the script
	config := &container.Config{
		Image:      "bash:latest",
		Cmd:        []string{"bash", "/job/jobScript.sh"},
		WorkingDir: "/job",
		Labels: map[string]string{
			"interlink.pod.uid":       string(pod.UID),
			"interlink.pod.name":      pod.Name,
			"interlink.pod.namespace": pod.Namespace,
			"interlink.container":     "jobscript",
		},
	}

	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: filesPath,
				Target: "/job",
			},
		},
		AutoRemove: false,
	}

	resp, err := d.Client.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("failed to create job script container: %w", err)
	}

	if err := d.Client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start job script container: %w", err)
	}

	log.G(ctx).Info("Started job script container: ", resp.ID)
	return resp.ID, nil
}

// Status implements the BatchSystem interface for Docker
func (d *DockerBackend) Status(ctx context.Context, pods []*v1.Pod) ([]commonIL.PodStatus, error) {
	var statuses []commonIL.PodStatus

	for _, pod := range pods {
		podUID := string(pod.UID)
		jobInfo, exists := d.Jobs[podUID]
		
		if !exists {
			// Pod not found, return empty status
			statuses = append(statuses, commonIL.PodStatus{
				PodName:      pod.Name,
				PodUID:       podUID,
				PodNamespace: pod.Namespace,
				Containers:   []v1.ContainerStatus{},
			})
			continue
		}

		containerStatuses := []v1.ContainerStatus{}

		// Check status for each container
		for _, container := range pod.Spec.Containers {
			containerID, found := jobInfo.ContainerIDs[container.Name]
			if !found {
				containerStatuses = append(containerStatuses, v1.ContainerStatus{
					Name:  container.Name,
					State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{}},
					Ready: false,
				})
				continue
			}

			status, err := d.getContainerStatus(ctx, containerID, container.Name, jobInfo)
			if err != nil {
				log.G(ctx).Warning("Failed to get container status: ", err)
				containerStatuses = append(containerStatuses, v1.ContainerStatus{
					Name:  container.Name,
					State: v1.ContainerState{},
					Ready: false,
				})
				continue
			}

			containerStatuses = append(containerStatuses, status)
		}

		statuses = append(statuses, commonIL.PodStatus{
			PodName:      pod.Name,
			PodUID:       podUID,
			PodNamespace: pod.Namespace,
			Containers:   containerStatuses,
		})
	}

	return statuses, nil
}

// Cancel implements the BatchSystem interface for Docker
func (d *DockerBackend) Cancel(ctx context.Context, podUID string) error {
	jobInfo, exists := d.Jobs[podUID]
	if !exists {
		return fmt.Errorf("job not found for pod UID: %s", podUID)
	}

	return d.cleanup(ctx, jobInfo)
}

// GetLogs implements the BatchSystem interface for Docker
func (d *DockerBackend) GetLogs(ctx context.Context, podUID, containerName string, follow bool, tailLines int) (io.Reader, error) {
	jobInfo, exists := d.Jobs[podUID]
	if !exists {
		return nil, fmt.Errorf("job not found for pod UID: %s", podUID)
	}

	containerID, found := jobInfo.ContainerIDs[containerName]
	if !found {
		return nil, fmt.Errorf("container %s not found in job", containerName)
	}

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Timestamps: false,
	}

	if tailLines > 0 {
		tail := fmt.Sprintf("%d", tailLines)
		options.Tail = tail
	}

	return d.Client.ContainerLogs(ctx, containerID, options)
}

// SystemInfo implements the BatchSystem interface for Docker
func (d *DockerBackend) SystemInfo(ctx context.Context) (string, error) {
	info, err := d.Client.Info(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get Docker info: %w", err)
	}

	return fmt.Sprintf("Docker Info:\n  Containers: %d (Running: %d, Paused: %d, Stopped: %d)\n  Images: %d\n  Server Version: %s\n  Storage Driver: %s\n  Operating System: %s\n  Architecture: %s",
		info.Containers,
		info.ContainersRunning,
		info.ContainersPaused,
		info.ContainersStopped,
		info.Images,
		info.ServerVersion,
		info.Driver,
		info.OperatingSystem,
		info.Architecture,
	), nil
}

// CreateDirectories implements the BatchSystem interface for Docker
func (d *DockerBackend) CreateDirectories() error {
	if err := os.MkdirAll(d.Config.DataRootFolder, 0755); err != nil {
		return fmt.Errorf("failed to create data root folder: %w", err)
	}
	log.G(d.Ctx).Info("Created data root folder: ", d.Config.DataRootFolder)
	return nil
}

// LoadJobs implements the BatchSystem interface for Docker
func (d *DockerBackend) LoadJobs() error {
	metadataDir := filepath.Join(d.Config.DataRootFolder, ".metadata")
	
	if _, err := os.Stat(metadataDir); os.IsNotExist(err) {
		return nil // No metadata to load
	}

	files, err := os.ReadDir(metadataDir)
	if err != nil {
		return fmt.Errorf("failed to read metadata directory: %w", err)
	}

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(metadataDir, file.Name()))
		if err != nil {
			log.G(d.Ctx).Warning("Failed to read metadata file ", file.Name(), ": ", err)
			continue
		}

		var jobInfo JobInfo
		if err := json.Unmarshal(data, &jobInfo); err != nil {
			log.G(d.Ctx).Warning("Failed to unmarshal metadata file ", file.Name(), ": ", err)
			continue
		}

		d.Jobs[jobInfo.PodUID] = &jobInfo
		log.G(d.Ctx).Debug("Loaded job metadata for pod UID: ", jobInfo.PodUID)
	}

	log.G(d.Ctx).Info("Loaded ", len(d.Jobs), " jobs from metadata")
	return nil
}

// GetJobID implements the BatchSystem interface for Docker
func (d *DockerBackend) GetJobID(podUID string) string {
	if jobInfo, exists := d.Jobs[podUID]; exists {
		return jobInfo.JobID
	}
	return ""
}

// Ensure DockerBackend implements BatchSystem interface
var _ backend.BatchSystem = (*DockerBackend)(nil)
