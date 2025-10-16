package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"

	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// GenericHandler wraps a BatchSystem backend and provides HTTP handlers
type GenericHandler struct {
	Backend backend.BatchSystem
	Ctx     context.Context
}

// SubmitHandler handles pod creation requests
func (h *GenericHandler) SubmitHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	spanCtx, span := tracer.Start(h.Ctx, "Create", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer commonIL.SetDurationSpan(start, span)

	log.G(h.Ctx).Info("Sidecar: received Submit call")
	statusCode := http.StatusOK

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	var podData commonIL.RetrievedPodData
	if err := json.Unmarshal(bodyBytes, &podData); err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	jobID, err := h.Backend.Submit(spanCtx, &podData)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	response := struct {
		PodUID string `json:"PodUID"`
		PodJID string `json:"PodJID"`
	}{
		PodUID: string(podData.Pod.UID),
		PodJID: jobID,
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	w.WriteHeader(statusCode)
	commonIL.SetDurationSpan(start, span, commonIL.WithHTTPReturnCode(statusCode))
	w.Write(responseBytes)
}

// StatusHandler handles pod status requests
func (h *GenericHandler) StatusHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	spanCtx, span := tracer.Start(h.Ctx, "Status", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer commonIL.SetDurationSpan(start, span)

	log.G(h.Ctx).Info("Sidecar: received GetStatus call")
	statusCode := http.StatusOK

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	var pods []*v1.Pod
	if err := json.Unmarshal(bodyBytes, &pods); err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	// If no pods requested, return system info
	if len(pods) == 0 {
		info, err := h.Backend.SystemInfo(spanCtx)
		if err != nil {
			statusCode = http.StatusInternalServerError
			h.handleError(spanCtx, w, statusCode, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(info))
		return
	}

	statuses, err := h.Backend.Status(spanCtx, pods)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	responseBytes, err := json.Marshal(statuses)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	w.WriteHeader(statusCode)
	w.Write(responseBytes)
}

// StopHandler handles pod deletion requests
func (h *GenericHandler) StopHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	spanCtx, span := tracer.Start(h.Ctx, "Delete", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer commonIL.SetDurationSpan(start, span)

	log.G(h.Ctx).Info("Sidecar: received Delete call")
	statusCode := http.StatusOK

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	var pod v1.Pod
	if err := json.Unmarshal(bodyBytes, &pod); err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	if err := h.Backend.Cancel(spanCtx, string(pod.UID)); err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	w.WriteHeader(statusCode)
	w.Write([]byte("Pod deleted successfully"))
}

// GetLogsHandler handles log retrieval requests
func (h *GenericHandler) GetLogsHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	spanCtx, span := tracer.Start(h.Ctx, "GetLogs", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer commonIL.SetDurationSpan(start, span)

	log.G(h.Ctx).Info("Sidecar: received GetLogs call")
	statusCode := http.StatusOK

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	var logRequest commonIL.LogStruct
	if err := json.Unmarshal(bodyBytes, &logRequest); err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	tailLines := logRequest.Opts.Tail

	reader, err := h.Backend.GetLogs(spanCtx, logRequest.PodUID, logRequest.ContainerName, logRequest.Opts.Follow, tailLines)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}
	defer func() {
		if closer, ok := reader.(io.Closer); ok {
			closer.Close()
		}
	}()

	w.WriteHeader(statusCode)
	io.Copy(w, reader)
}

// SystemInfoHandler handles system info requests
func (h *GenericHandler) SystemInfoHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	spanCtx, span := tracer.Start(h.Ctx, "SystemInfo", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer commonIL.SetDurationSpan(start, span)

	log.G(h.Ctx).Info("Sidecar: received SystemInfo call")

	info, err := h.Backend.SystemInfo(spanCtx)
	if err != nil {
		h.handleError(spanCtx, w, http.StatusInternalServerError, err)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(info))
}

// handleError logs an error and writes an HTTP error response
func (h *GenericHandler) handleError(ctx context.Context, w http.ResponseWriter, statusCode int, err error) {
	log.G(ctx).Error(err)
	w.WriteHeader(statusCode)
	w.Write([]byte(err.Error()))
}
