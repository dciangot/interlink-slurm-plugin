package slurm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/slurm"
)

// SlurmBackend wraps the existing SLURM implementation to implement the BatchSystem interface
type SlurmBackend struct {
	handler *slurm.SidecarHandler
}

// NewSlurmBackend creates a new SLURM backend adapter
func NewSlurmBackend(ctx context.Context, config *slurm.SlurmConfig, jids *map[string]*slurm.JidStruct) *SlurmBackend {
	return &SlurmBackend{
		handler: &slurm.SidecarHandler{
			Config: *config,
			JIDs:   jids,
			Ctx:    ctx,
		},
	}
}

// Submit implements the BatchSystem interface for SLURM
func (s *SlurmBackend) Submit(ctx context.Context, podData *commonIL.RetrievedPodData) (string, error) {
	body, err := json.Marshal(podData)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pod data: %w", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/create", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handler.SubmitHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("submit failed with status %d and failed to read body: %w", resp.StatusCode, err)
		}
		return "", fmt.Errorf("submit failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result slurm.CreateStruct
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result.PodJID, nil
}

// Status implements the BatchSystem interface for SLURM
func (s *SlurmBackend) Status(ctx context.Context, pods []*v1.Pod) ([]commonIL.PodStatus, error) {
	body, err := json.Marshal(pods)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pods: %w", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/status", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handler.StatusHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("status request failed with status %d and failed to read body: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("status request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var statuses []commonIL.PodStatus
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if err := json.Unmarshal(bodyBytes, &statuses); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return statuses, nil
}

// Cancel implements the BatchSystem interface for SLURM
func (s *SlurmBackend) Cancel(ctx context.Context, podUID string) error {
	// Create a minimal pod structure with just the UID
	pod := v1.Pod{}
	pod.UID = types.UID(podUID)

	body, err := json.Marshal(pod)
	if err != nil {
		return fmt.Errorf("failed to marshal pod: %w", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/delete", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handler.StopHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cancel failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// GetLogs implements the BatchSystem interface for SLURM
func (s *SlurmBackend) GetLogs(ctx context.Context, podUID, containerName string, follow bool, tailLines int) (io.Reader, error) {
	logsRequest := commonIL.LogStruct{
		PodUID:        podUID,
		ContainerName: containerName,
		Opts: commonIL.ContainerLogOpts{
			Follow: follow,
			Tail:   tailLines,
		},
	}

	body, err := json.Marshal(logsRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal log request: %w", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/getLogs", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handler.GetLogsHandler(w, req)

	resp := w.Result()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("get logs failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return resp.Body, nil
}

// SystemInfo implements the BatchSystem interface for SLURM
func (s *SlurmBackend) SystemInfo(ctx context.Context) (string, error) {
	req := httptest.NewRequest(http.MethodGet, "/system-info", nil)
	w := httptest.NewRecorder()

	s.handler.SystemInfoHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("system info request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(bodyBytes), nil
}

// CreateDirectories implements the BatchSystem interface for SLURM
func (s *SlurmBackend) CreateDirectories() error {
	return s.handler.CreateDirectories()
}

// LoadJobs implements the BatchSystem interface for SLURM
func (s *SlurmBackend) LoadJobs() error {
	return s.handler.LoadJIDs()
}

// GetJobID implements the BatchSystem interface for SLURM
func (s *SlurmBackend) GetJobID(podUID string) string {
	if jid, exists := (*s.handler.JIDs)[podUID]; exists {
		return jid.JID
	}
	return ""
}

// Ensure SlurmBackend implements BatchSystem interface
var _ backend.BatchSystem = (*SlurmBackend)(nil)
