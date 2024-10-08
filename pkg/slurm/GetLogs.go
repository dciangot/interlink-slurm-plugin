package slurm

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/containerd/containerd/log"

	commonIL "github.com/intertwin-eu/interlink/pkg/interlink"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// GetLogsHandler reads Jobs' output file to return what's logged inside.
// What's returned is based on the provided parameters (Tail/LimitBytes/Timestamps/etc)
func (h *SidecarHandler) GetLogsHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UnixMicro()
	tracer := otel.Tracer("interlink-API")
	spanCtx, span := tracer.Start(h.Ctx, "GetLogsSLURM", trace.WithAttributes(
		attribute.Int64("start.timestamp", start),
	))
	defer span.End()
	defer commonIL.SetDurationSpan(start, span)

	log.G(h.Ctx).Info("Docker Sidecar: received GetLogs call")
	var req commonIL.LogStruct
	statusCode := http.StatusOK
	currentTime := time.Now()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		h.handleError(spanCtx, w, statusCode, err)
		return
	}

	span.SetAttributes(
		attribute.String("pod.name", req.PodName),
		attribute.String("pod.namespace", req.Namespace),
		attribute.Int("opts.limitbytes", req.Opts.LimitBytes),
		attribute.Int("opts.since", req.Opts.SinceSeconds),
		attribute.Int64("opts.sincetime", req.Opts.SinceTime.UnixMicro()),
		attribute.Int("opts.tail", req.Opts.Tail),
		attribute.Bool("opts.follow", req.Opts.Follow),
		attribute.Bool("opts.previous", req.Opts.Previous),
		attribute.Bool("opts.timestamps", req.Opts.Timestamps),
	)

	path := h.Config.DataRootFolder + req.Namespace + "-" + req.PodUID
	var output []byte
	if req.Opts.Timestamps {
		h.handleError(spanCtx, w, statusCode, err)
		return
	} else {
		log.G(h.Ctx).Info("Reading  " + path + "/" + req.ContainerName + ".out")
		containerOutput, err1 := os.ReadFile(path + "/" + req.ContainerName + ".out")
		if err1 != nil {
			log.G(h.Ctx).Error("Failed to read container logs.")
		}
		jobOutput, err2 := os.ReadFile(path + "/" + "job.out")
		if err2 != nil {
			log.G(h.Ctx).Error("Failed to read job logs.")
		}

		if err1 != nil && err2 != nil {
			span.AddEvent("Error retrieving logs")
			h.handleError(spanCtx, w, statusCode, err)
			return
		}

		output = append(output, jobOutput...)
		output = append(output, containerOutput...)

	}

	var returnedLogs string

	if req.Opts.Tail != 0 {
		var lastLines []string

		splittedLines := strings.Split(string(output), "\n")

		if req.Opts.Tail > len(splittedLines) {
			lastLines = splittedLines
		} else {
			lastLines = splittedLines[len(splittedLines)-req.Opts.Tail-1:]
		}

		for _, line := range lastLines {
			returnedLogs += line + "\n"
		}
	} else if req.Opts.LimitBytes != 0 {
		var lastBytes []byte
		if req.Opts.LimitBytes > len(output) {
			lastBytes = output
		} else {
			lastBytes = output[len(output)-req.Opts.LimitBytes-1:]
		}

		returnedLogs = string(lastBytes)
	} else {
		returnedLogs = string(output)
	}

	if req.Opts.Timestamps && (req.Opts.SinceSeconds != 0 || !req.Opts.SinceTime.IsZero()) {
		temp := returnedLogs
		returnedLogs = ""
		splittedLogs := strings.Split(temp, "\n")
		timestampFormat := "2006-01-02T15:04:05.999999999Z"

		for _, Log := range splittedLogs {
			part := strings.SplitN(Log, " ", 2)
			timestampString := part[0]
			timestamp, err := time.Parse(timestampFormat, timestampString)
			if err != nil {
				continue
			}
			if req.Opts.SinceSeconds != 0 {
				if currentTime.Sub(timestamp).Seconds() > float64(req.Opts.SinceSeconds) {
					returnedLogs += Log + "\n"
				}
			} else {
				if timestamp.Sub(req.Opts.SinceTime).Seconds() >= 0 {
					returnedLogs += Log + "\n"
				}
			}
		}
	}

	commonIL.SetDurationSpan(start, span, commonIL.WithHTTPReturnCode(statusCode))

	if statusCode != http.StatusOK {
		w.Write([]byte("Some errors occurred while checking container status. Check Docker Sidecar's logs"))
	} else {
		w.WriteHeader(statusCode)
		w.Write([]byte(returnedLogs))
	}
}
