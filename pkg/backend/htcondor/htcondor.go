package htcondor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
)

// NewHTCondorBackend creates a new HTCondor backend instance
func NewHTCondorBackend(ctx context.Context, config *HTCondorConfig) (*HTCondorBackend, error) {
	// Validate configuration
	if config.CondorSubmitPath == "" {
		config.CondorSubmitPath = "condor_submit"
	}
	if config.CondorQPath == "" {
		config.CondorQPath = "condor_q"
	}
	if config.CondorRmPath == "" {
		config.CondorRmPath = "condor_rm"
	}
	if config.CondorHistoryPath == "" {
		config.CondorHistoryPath = "condor_history"
	}

	if config.ContainerRuntime == "" {
		config.ContainerRuntime = "singularity"
	}
	if config.SingularityPath == "" {
		config.SingularityPath = "/usr/bin/singularity"
	}

	if config.Universe == "" {
		config.Universe = "vanilla"
	}

	if config.ImagePrefix == "" {
		config.ImagePrefix = "docker://"
	}

	// Log streaming is not supported in HTCondor without shared filesystem
	config.EnableLogStreaming = false

	backend := &HTCondorBackend{
		Config: config,
		Jobs:   make(map[string]*JobInfo),
		Ctx:    ctx,
	}

	log.G(ctx).Info("Initialized HTCondor backend with container runtime: ", config.ContainerRuntime)
	return backend, nil
}

// Submit implements the BatchSystem interface for HTCondor
func (h *HTCondorBackend) Submit(ctx context.Context, podData *commonIL.RetrievedPodData) (string, error) {
	pod := podData.Pod
	
	log.G(ctx).Info("HTCondor: Submitting pod ", pod.Name, " in namespace ", pod.Namespace)

	// 1. Create spool directory for this pod
	spoolDir, err := h.createJobSpool(pod)
	if err != nil {
		return "", fmt.Errorf("failed to create spool directory: %w", err)
	}

	// 2. Initialize job info
	jobInfo := &JobInfo{
		PodUID:      string(pod.UID),
		PodName:     pod.Name,
		Namespace:   pod.Namespace,
		JobIDs:      make(map[string]string),
		SubmitFiles: make(map[string]string),
		SpoolDir:    spoolDir,
		StartTime:   time.Now(),
	}

	// 3. Stage ConfigMaps and Secrets to spool directory
	if h.Config.ExportPodData {
		if err := h.stageConfigMapsAndSecrets(pod, spoolDir); err != nil {
			os.RemoveAll(spoolDir)
			return "", fmt.Errorf("failed to stage ConfigMaps/Secrets: %w", err)
		}
	}

	// 4. Generate setup script
	if err := h.generateSetupScript(spoolDir); err != nil {
		os.RemoveAll(spoolDir)
		return "", fmt.Errorf("failed to generate setup script: %w", err)
	}

	// 5. Handle init containers (sequential execution)
	for _, initContainer := range pod.Spec.InitContainers {
		log.G(ctx).Info("HTCondor: Submitting init container: ", initContainer.Name)
		
		jobID, submitFile, err := h.submitContainer(ctx, pod, &initContainer, spoolDir, true)
		if err != nil {
			h.cleanup(ctx, jobInfo)
			return "", fmt.Errorf("failed to submit init container %s: %w", initContainer.Name, err)
		}

		jobInfo.JobIDs[initContainer.Name] = jobID
		jobInfo.SubmitFiles[initContainer.Name] = submitFile

		// Wait for init container to complete
		log.G(ctx).Info("HTCondor: Waiting for init container ", initContainer.Name, " to complete")
		if err := h.waitForJobCompletion(ctx, jobID); err != nil {
			h.cleanup(ctx, jobInfo)
			return "", fmt.Errorf("init container %s failed: %w", initContainer.Name, err)
		}
	}

	// 6. Handle regular containers (parallel execution)
	var primaryJobID string
	for _, container := range pod.Spec.Containers {
		log.G(ctx).Info("HTCondor: Submitting container: ", container.Name)
		
		jobID, submitFile, err := h.submitContainer(ctx, pod, &container, spoolDir, false)
		if err != nil {
			h.cleanup(ctx, jobInfo)
			return "", fmt.Errorf("failed to submit container %s: %w", container.Name, err)
		}

		jobInfo.JobIDs[container.Name] = jobID
		jobInfo.SubmitFiles[container.Name] = submitFile

		// Use first container's job ID as primary
		if primaryJobID == "" {
			primaryJobID = jobID
			jobInfo.ClusterID = extractClusterID(jobID)
		}
	}

	// 7. Store job info
	h.Jobs[string(pod.UID)] = jobInfo

	// 8. Save job metadata
	if err := h.saveJobMetadata(jobInfo); err != nil {
		log.G(ctx).Warning("Failed to save job metadata: ", err)
	}

	log.G(ctx).Info("HTCondor: Successfully submitted pod ", pod.Name, " with cluster ID ", jobInfo.ClusterID)
	return primaryJobID, nil
}

// Status implements the BatchSystem interface for HTCondor
func (h *HTCondorBackend) Status(ctx context.Context, pods []*v1.Pod) ([]commonIL.PodStatus, error) {
	var statuses []commonIL.PodStatus

	for _, pod := range pods {
		podUID := string(pod.UID)
		jobInfo, exists := h.Jobs[podUID]
		
		if !exists {
			// Pod not tracked
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
			jobID, found := jobInfo.JobIDs[container.Name]
			if !found {
				containerStatuses = append(containerStatuses, v1.ContainerStatus{
					Name:  container.Name,
					State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{}},
					Ready: false,
				})
				continue
			}

			status, err := h.getContainerStatus(ctx, jobID, container.Name, jobInfo)
			if err != nil {
				log.G(ctx).Warning("Failed to get container status for ", container.Name, ": ", err)
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

// Cancel implements the BatchSystem interface for HTCondor
func (h *HTCondorBackend) Cancel(ctx context.Context, podUID string) error {
	jobInfo, exists := h.Jobs[podUID]
	if !exists {
		return fmt.Errorf("job not found for pod UID: %s", podUID)
	}

	log.G(ctx).Info("HTCondor: Canceling pod ", jobInfo.PodName)
	return h.cleanup(ctx, jobInfo)
}

// GetLogs implements the BatchSystem interface for HTCondor
// Note: Real-time log streaming is not supported without shared filesystem
func (h *HTCondorBackend) GetLogs(ctx context.Context, podUID, containerName string, follow bool, tailLines int) (io.Reader, error) {
	jobInfo, exists := h.Jobs[podUID]
	if !exists {
		return nil, fmt.Errorf("job not found for pod UID: %s", podUID)
	}

	// HTCondor without shared filesystem cannot stream logs in real-time
	// Logs are only available after job completion via file transfer
	if follow {
		return nil, fmt.Errorf("real-time log streaming (follow=true) not supported in HTCondor backend: " +
			"HTCondor execute nodes do not share filesystem with submit node. " +
			"Logs are available after job completion via HTCondor file transfer")
	}

	// For completed jobs, try to read the output file
	jobID, found := jobInfo.JobIDs[containerName]
	if !found {
		return nil, fmt.Errorf("container %s not found in job", containerName)
	}

	// Check if job is completed
	job, err := h.queryJob(ctx, jobID)
	if err != nil || job.Status != JobStateCompleted {
		return nil, fmt.Errorf("logs not yet available: job %s is not completed (status: %s)", 
			jobID, job.Status.String())
	}

	// Read output file from spool directory
	outputFile := filepath.Join(jobInfo.SpoolDir, fmt.Sprintf("job.%s.out", jobID))
	file, err := os.Open(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return file, nil
}

// SystemInfo implements the BatchSystem interface for HTCondor
func (h *HTCondorBackend) SystemInfo(ctx context.Context) (string, error) {
	// Query HTCondor status using condor_q
	info, err := h.getHTCondorInfo(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get HTCondor info: %w", err)
	}

	return info, nil
}

// CreateDirectories implements the BatchSystem interface for HTCondor
func (h *HTCondorBackend) CreateDirectories() error {
	// Create spool directory
	if err := os.MkdirAll(h.Config.SpoolDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create spool directory: %w", err)
	}

	// Create submit file directory
	if err := os.MkdirAll(h.Config.SubmitFileDir, 0755); err != nil {
		return fmt.Errorf("failed to create submit file directory: %w", err)
	}

	log.G(h.Ctx).Info("Created HTCondor directories: spool=", h.Config.SpoolDirectory, 
		" submit=", h.Config.SubmitFileDir)
	return nil
}

// LoadJobs implements the BatchSystem interface for HTCondor
func (h *HTCondorBackend) LoadJobs() error {
	metadataDir := filepath.Join(h.Config.SpoolDirectory, ".metadata")
	
	if _, err := os.Stat(metadataDir); os.IsNotExist(err) {
		return nil
	}

	files, err := os.ReadDir(metadataDir)
	if err != nil {
		return fmt.Errorf("failed to read metadata directory: %w", err)
	}

	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			jobInfo, err := h.loadJobMetadata(filepath.Join(metadataDir, file.Name()))
			if err != nil {
				log.G(h.Ctx).Warning("Failed to load job metadata from ", file.Name(), ": ", err)
				continue
			}

			h.Jobs[jobInfo.PodUID] = jobInfo
			log.G(h.Ctx).Debug("Loaded job metadata for pod UID: ", jobInfo.PodUID)
		}
	}

	log.G(h.Ctx).Info("Loaded ", len(h.Jobs), " jobs from metadata")
	return nil
}

// GetJobID implements the BatchSystem interface for HTCondor
func (h *HTCondorBackend) GetJobID(podUID string) string {
	if jobInfo, exists := h.Jobs[podUID]; exists {
		return jobInfo.ClusterID
	}
	return ""
}

// Ensure HTCondorBackend implements BatchSystem interface
var _ backend.BatchSystem = (*HTCondorBackend)(nil)
