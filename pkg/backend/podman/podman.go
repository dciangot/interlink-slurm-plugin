package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
)

// NewPodmanBackend creates a new Podman backend instance
func NewPodmanBackend(ctx context.Context, config *PodmanConfig) (*PodmanBackend, error) {
	endpoint := config.Endpoint
	if endpoint == "" {
		// Try rootless socket first
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeDir != "" {
			endpoint = fmt.Sprintf("unix://%s/podman/podman.sock", runtimeDir)
		} else {
			endpoint = "unix:///run/podman/podman.sock"
		}
	}

	// Parse endpoint and create HTTP client
	httpClient, baseURL, err := createHTTPClient(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	backend := &PodmanBackend{
		Config:     config,
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		Jobs:       make(map[string]*JobInfo),
		Ctx:        ctx,
	}

	// Verify connection
	if err := backend.ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to Podman: %w", err)
	}

	return backend, nil
}

// createHTTPClient creates an HTTP client based on the endpoint type
func createHTTPClient(endpoint string) (*http.Client, string, error) {
	if strings.HasPrefix(endpoint, "unix://") {
		socketPath := strings.TrimPrefix(endpoint, "unix://")
		client := &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
			Timeout: 30 * time.Second,
		}
		return client, "http://d", nil
	} else if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		client := &http.Client{Timeout: 30 * time.Second}
		return client, endpoint, nil
	}
	return nil, "", fmt.Errorf("unsupported endpoint format: %s", endpoint)
}

// ping verifies connection to Podman
func (p *PodmanBackend) ping(ctx context.Context) error {
	resp, err := p.doRequest(ctx, "GET", "/_ping", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping failed with status: %d", resp.StatusCode)
	}
	return nil
}

// Submit implements the BatchSystem interface for Podman
func (p *PodmanBackend) Submit(ctx context.Context, podData *commonIL.RetrievedPodData) (string, error) {
	pod := podData.Pod
	filesPath := filepath.Join(p.Config.DataRootFolder, pod.Namespace+"-"+string(pod.UID))

	if err := os.MkdirAll(filesPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create working directory: %w", err)
	}

	jobInfo := &JobInfo{
		PodUID:       string(pod.UID),
		PodName:      pod.Name,
		Namespace:    pod.Namespace,
		ContainerIDs: make(map[string]string),
		FilesPath:    filesPath,
	}

	// If using Podman pods, create a pod first
	if p.Config.UsePods {
		podID, err := p.createPodmanPod(ctx, &pod)
		if err != nil {
			os.RemoveAll(filesPath)
			return "", fmt.Errorf("failed to create Podman pod: %w", err)
		}
		jobInfo.PodmanPodID = podID
		jobInfo.JobID = podID
	}

	// Handle custom job script
	if podData.JobScript != "" {
		containerID, err := p.executeJobScript(ctx, &pod, podData.JobScript, filesPath, jobInfo.PodmanPodID)
		if err != nil {
			p.cleanup(ctx, jobInfo)
			os.RemoveAll(filesPath)
			return "", err
		}
		if !p.Config.UsePods {
			jobInfo.JobID = containerID
		}
		jobInfo.ContainerIDs["jobscript"] = containerID
		p.Jobs[string(pod.UID)] = jobInfo
		
		if err := p.saveJobMetadata(jobInfo); err != nil {
			log.G(ctx).Warning("Failed to save job metadata: ", err)
		}
		
		return jobInfo.JobID, nil
	}

	// Process init containers
	for _, container := range pod.Spec.InitContainers {
		containerID, err := p.runContainer(ctx, &pod, &container, filesPath, true, jobInfo.PodmanPodID)
		if err != nil {
			p.cleanup(ctx, jobInfo)
			os.RemoveAll(filesPath)
			return "", fmt.Errorf("failed to run init container %s: %w", container.Name, err)
		}
		jobInfo.ContainerIDs[container.Name] = containerID

		if err := p.waitForContainer(ctx, containerID); err != nil {
			p.cleanup(ctx, jobInfo)
			os.RemoveAll(filesPath)
			return "", fmt.Errorf("init container %s failed: %w", container.Name, err)
		}
	}

	// Process regular containers
	for _, container := range pod.Spec.Containers {
		containerID, err := p.runContainer(ctx, pod, &container, filesPath, false, jobInfo.PodmanPodID)
		if err != nil {
			p.cleanup(ctx, jobInfo)
			os.RemoveAll(filesPath)
			return "", fmt.Errorf("failed to run container %s: %w", container.Name, err)
		}
		jobInfo.ContainerIDs[container.Name] = containerID
		
		if !p.Config.UsePods && jobInfo.JobID == "" {
			jobInfo.JobID = containerID
		}
	}

	jobInfo.StartTime = time.Now()
	p.Jobs[string(pod.UID)] = jobInfo

	if err := p.saveJobMetadata(jobInfo); err != nil {
		log.G(ctx).Warning("Failed to save job metadata: ", err)
	}

	log.G(ctx).Info("Successfully created Podman containers for pod ", pod.Name, " with job ID ", jobInfo.JobID)
	return jobInfo.JobID, nil
}

// Status implements the BatchSystem interface for Podman
func (p *PodmanBackend) Status(ctx context.Context, pods []*v1.Pod) ([]commonIL.PodStatus, error) {
	var statuses []commonIL.PodStatus

	for _, pod := range pods {
		podUID := string(pod.UID)
		jobInfo, exists := p.Jobs[podUID]
		
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

			status, err := p.getContainerStatus(ctx, containerID, container.Name, jobInfo)
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

// Cancel implements the BatchSystem interface for Podman
func (p *PodmanBackend) Cancel(ctx context.Context, podUID string) error {
	jobInfo, exists := p.Jobs[podUID]
	if !exists {
		return fmt.Errorf("job not found for pod UID: %s", podUID)
	}

	return p.cleanup(ctx, jobInfo)
}

// GetLogs implements the BatchSystem interface for Podman
func (p *PodmanBackend) GetLogs(ctx context.Context, podUID, containerName string, follow bool, tailLines int) (io.Reader, error) {
	jobInfo, exists := p.Jobs[podUID]
	if !exists {
		return nil, fmt.Errorf("job not found for pod UID: %s", podUID)
	}

	containerID, found := jobInfo.ContainerIDs[containerName]
	if !found {
		return nil, fmt.Errorf("container %s not found in job", containerName)
	}

	params := url.Values{}
	params.Set("stdout", "true")
	params.Set("stderr", "true")
	params.Set("follow", fmt.Sprintf("%t", follow))
	if tailLines > 0 {
		params.Set("tail", fmt.Sprintf("%d", tailLines))
	}

	endpoint := fmt.Sprintf("/v3.0.0/libpod/containers/%s/logs?%s", containerID, params.Encode())
	resp, err := p.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to get logs: status %d", resp.StatusCode)
	}

	return resp.Body, nil
}

// SystemInfo implements the BatchSystem interface for Podman
func (p *PodmanBackend) SystemInfo(ctx context.Context) (string, error) {
	resp, err := p.doRequest(ctx, "GET", "/v3.0.0/libpod/version", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var version PodmanVersion
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return "", fmt.Errorf("failed to decode version: %w", err)
	}

	// Get container count
	resp, err = p.doRequest(ctx, "GET", "/v3.0.0/libpod/containers/json?all=true", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var containers []PodmanContainer
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return "", fmt.Errorf("failed to decode containers: %w", err)
	}

	running := 0
	stopped := 0
	for _, c := range containers {
		if c.State == "running" {
			running++
		} else {
			stopped++
		}
	}

	return fmt.Sprintf("Podman Info:\n  Version: %s\n  API Version: %s\n  Go Version: %s\n  Containers: %d (Running: %d, Stopped: %d)\n  Endpoint: %s",
		version.Version,
		version.APIVersion,
		version.GoVersion,
		len(containers),
		running,
		stopped,
		p.Config.Endpoint,
	), nil
}

// CreateDirectories implements the BatchSystem interface for Podman
func (p *PodmanBackend) CreateDirectories() error {
	if err := os.MkdirAll(p.Config.DataRootFolder, 0755); err != nil {
		return fmt.Errorf("failed to create data root folder: %w", err)
	}
	log.G(p.Ctx).Info("Created data root folder: ", p.Config.DataRootFolder)
	return nil
}

// LoadJobs implements the BatchSystem interface for Podman
func (p *PodmanBackend) LoadJobs() error {
	metadataDir := filepath.Join(p.Config.DataRootFolder, ".metadata")
	
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
			log.G(p.Ctx).Warning("Failed to read metadata file ", file.Name(), ": ", err)
			continue
		}

		var jobInfo JobInfo
		if err := json.Unmarshal(data, &jobInfo); err != nil {
			log.G(p.Ctx).Warning("Failed to unmarshal metadata file ", file.Name(), ": ", err)
			continue
		}

		p.Jobs[jobInfo.PodUID] = &jobInfo
		log.G(p.Ctx).Debug("Loaded job metadata for pod UID: ", jobInfo.PodUID)
	}

	log.G(p.Ctx).Info("Loaded ", len(p.Jobs), " jobs from metadata")
	return nil
}

// GetJobID implements the BatchSystem interface for Podman
func (p *PodmanBackend) GetJobID(podUID string) string {
	if jobInfo, exists := p.Jobs[podUID]; exists {
		return jobInfo.JobID
	}
	return ""
}

// doRequest performs an HTTP request to the Podman API
func (p *PodmanBackend) doRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	reqURL := p.BaseURL + endpoint
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	return resp, nil
}

// Ensure PodmanBackend implements BatchSystem interface
var _ backend.BatchSystem = (*PodmanBackend)(nil)
