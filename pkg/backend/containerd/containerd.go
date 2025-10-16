package containerd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
)

// NewContainerdBackend creates a new Containerd backend instance
func NewContainerdBackend(ctx context.Context, config *ContainerdConfig) (*ContainerdBackend, error) {
	socketPath := config.Socket
	if socketPath == "" {
		socketPath = "/run/containerd/containerd.sock"
	}

	client, err := containerd.New(socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create Containerd client: %w", err)
	}

	// Set namespace in context
	if config.Namespace == "" {
		config.Namespace = "interlink"
	}
	ctx = namespaces.WithNamespace(ctx, config.Namespace)

	backend := &ContainerdBackend{
		Config: config,
		Client: client,
		Jobs:   make(map[string]*JobInfo),
		Ctx:    ctx,
	}

	return backend, nil
}

// Submit implements the BatchSystem interface for Containerd
func (c *ContainerdBackend) Submit(ctx context.Context, podData *commonIL.RetrievedPodData) (string, error) {
	pod := podData.Pod
	ctx = namespaces.WithNamespace(ctx, c.Config.Namespace)
	
	filesPath := filepath.Join(c.Config.DataRootFolder, pod.Namespace+"-"+string(pod.UID))

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
		containerID, err := c.executeJobScript(ctx, &pod, podData.JobScript, filesPath)
		if err != nil {
			os.RemoveAll(filesPath)
			return "", err
		}
		jobInfo.JobID = containerID
		jobInfo.ContainerIDs["jobscript"] = containerID
		c.Jobs[string(pod.UID)] = jobInfo
		
		if err := c.saveJobMetadata(jobInfo); err != nil {
			log.G(ctx).Warning("Failed to save job metadata: ", err)
		}
		
		return containerID, nil
	}

	// Process init containers first
	for _, container := range pod.Spec.InitContainers {
		containerID, err := c.runContainer(ctx, &pod, &container, filesPath, true)
		if err != nil {
			c.cleanup(ctx, jobInfo)
			os.RemoveAll(filesPath)
			return "", fmt.Errorf("failed to run init container %s: %w", container.Name, err)
		}
		jobInfo.ContainerIDs[container.Name] = containerID

		// Wait for init container to complete
		if err := c.waitForContainer(ctx, containerID); err != nil {
			c.cleanup(ctx, jobInfo)
			os.RemoveAll(filesPath)
			return "", fmt.Errorf("init container %s failed: %w", container.Name, err)
		}
	}

	// Process regular containers
	for _, container := range pod.Spec.Containers {
		containerID, err := c.runContainer(ctx, &pod, &container, filesPath, false)
		if err != nil {
			c.cleanup(ctx, jobInfo)
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
	c.Jobs[string(pod.UID)] = jobInfo

	if err := c.saveJobMetadata(jobInfo); err != nil {
		log.G(ctx).Warning("Failed to save job metadata: ", err)
	}

	log.G(ctx).Info("Successfully created Containerd containers for pod ", pod.Name, " with primary job ID ", jobInfo.JobID)
	return jobInfo.JobID, nil
}

// executeJobScript runs a custom job script in a bash container
func (c *ContainerdBackend) executeJobScript(ctx context.Context, pod *v1.Pod, script string, filesPath string) (string, error) {
	scriptPath := filepath.Join(filesPath, "jobScript.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("failed to write job script: %w", err)
	}

	image := c.prepareImage("bash:latest")
	if err := c.pullImageIfNeeded(ctx, image); err != nil {
		log.G(ctx).Warning("Failed to pull image: ", err)
	}

	containerID := fmt.Sprintf("interlink-%s-jobscript", string(pod.UID))
	
	// Create container with the script
	container, err := c.Client.NewContainer(
		ctx,
		containerID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(containerID+"-snapshot", image),
		containerd.WithNewSpec(
			oci.WithImageConfig(image),
			oci.WithProcessArgs("bash", "/job/jobScript.sh"),
			oci.WithEnv([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}),
		),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	// Create task and start
	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio))
	if err != nil {
		container.Delete(ctx, containerd.WithSnapshotCleanup)
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	if err := task.Start(ctx); err != nil {
		task.Delete(ctx)
		container.Delete(ctx, containerd.WithSnapshotCleanup)
		return "", fmt.Errorf("failed to start task: %w", err)
	}

	log.G(ctx).Info("Started job script container: ", containerID)
	return containerID, nil
}

// Status implements the BatchSystem interface for Containerd
func (c *ContainerdBackend) Status(ctx context.Context, pods []*v1.Pod) ([]commonIL.PodStatus, error) {
	ctx = namespaces.WithNamespace(ctx, c.Config.Namespace)
	var statuses []commonIL.PodStatus

	for _, pod := range pods {
		podUID := string(pod.UID)
		jobInfo, exists := c.Jobs[podUID]
		
		if !exists {
			statuses = append(statuses, commonIL.PodStatus{
				PodName:      pod.Name,
				PodUID:       podUID,
				PodNamespace: pod.Namespace,
				Containers:   []v1.ContainerStatus{},
			})
			continue
		}

		containerStatuses := []v1.ContainerStatus{}

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

			status, err := c.getContainerStatus(ctx, containerID, container.Name, jobInfo)
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

// Cancel implements the BatchSystem interface for Containerd
func (c *ContainerdBackend) Cancel(ctx context.Context, podUID string) error {
	ctx = namespaces.WithNamespace(ctx, c.Config.Namespace)
	jobInfo, exists := c.Jobs[podUID]
	if !exists {
		return fmt.Errorf("job not found for pod UID: %s", podUID)
	}

	return c.cleanup(ctx, jobInfo)
}

// GetLogs implements the BatchSystem interface for Containerd
func (c *ContainerdBackend) GetLogs(ctx context.Context, podUID, containerName string, follow bool, tailLines int) (io.Reader, error) {
	jobInfo, exists := c.Jobs[podUID]
	if !exists {
		return nil, fmt.Errorf("job not found for pod UID: %s", podUID)
	}

	containerID, found := jobInfo.ContainerIDs[containerName]
	if !found {
		return nil, fmt.Errorf("container %s not found in job", containerName)
	}

	// Read logs from the log file in filesPath
	logPath := filepath.Join(jobInfo.FilesPath, containerName+".log")
	return os.Open(logPath)
}

// SystemInfo implements the BatchSystem interface for Containerd
func (c *ContainerdBackend) SystemInfo(ctx context.Context) (string, error) {
	ctx = namespaces.WithNamespace(ctx, c.Config.Namespace)
	
	version, err := c.Client.Version(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get Containerd version: %w", err)
	}

	containers, err := c.Client.Containers(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	running := 0
	stopped := 0
	for _, container := range containers {
		task, err := container.Task(ctx, nil)
		if err != nil {
			stopped++
			continue
		}
		status, err := task.Status(ctx)
		if err != nil || status.Status != containerd.Running {
			stopped++
		} else {
			running++
		}
	}

	return fmt.Sprintf("Containerd Info:\n  Version: %s\n  Revision: %s\n  Namespace: %s\n  Containers: %d (Running: %d, Stopped: %d)\n  Socket: %s",
		version.Version,
		version.Revision,
		c.Config.Namespace,
		len(containers),
		running,
		stopped,
		c.Config.Socket,
	), nil
}

// CreateDirectories implements the BatchSystem interface for Containerd
func (c *ContainerdBackend) CreateDirectories() error {
	if err := os.MkdirAll(c.Config.DataRootFolder, 0755); err != nil {
		return fmt.Errorf("failed to create data root folder: %w", err)
	}
	log.G(c.Ctx).Info("Created data root folder: ", c.Config.DataRootFolder)
	return nil
}

// LoadJobs implements the BatchSystem interface for Containerd
func (c *ContainerdBackend) LoadJobs() error {
	metadataDir := filepath.Join(c.Config.DataRootFolder, ".metadata")
	
	if _, err := os.Stat(metadataDir); os.IsNotExist(err) {
		return nil
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
			log.G(c.Ctx).Warning("Failed to read metadata file ", file.Name(), ": ", err)
			continue
		}

		var jobInfo JobInfo
		if err := json.Unmarshal(data, &jobInfo); err != nil {
			log.G(c.Ctx).Warning("Failed to unmarshal metadata file ", file.Name(), ": ", err)
			continue
		}

		c.Jobs[jobInfo.PodUID] = &jobInfo
		log.G(c.Ctx).Debug("Loaded job metadata for pod UID: ", jobInfo.PodUID)
	}

	log.G(c.Ctx).Info("Loaded ", len(c.Jobs), " jobs from metadata")
	return nil
}

// GetJobID implements the BatchSystem interface for Containerd
func (c *ContainerdBackend) GetJobID(podUID string) string {
	if jobInfo, exists := c.Jobs[podUID]; exists {
		return jobInfo.JobID
	}
	return ""
}

// Ensure ContainerdBackend implements BatchSystem interface
var _ backend.BatchSystem = (*ContainerdBackend)(nil)
