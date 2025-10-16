package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/podman"
	slurmbackend "github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/slurm"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/config"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/handlers"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/slurm"

	"github.com/virtual-kubelet/virtual-kubelet/trace"
	"github.com/virtual-kubelet/virtual-kubelet/trace/opentelemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

func initProvider(ctx context.Context) (func(context.Context) error, error) {
	log.G(ctx).Info("Tracing is enabled, setting up the TracerProvider")

	// Get the TELEMETRY_UNIQUE_ID from the environment, if it is not set, use the hostname
	uniqueID := os.Getenv("TELEMETRY_UNIQUE_ID")
	if uniqueID == "" {
		log.G(ctx).Info("No TELEMETRY_UNIQUE_ID set, generating a new one")
		newUUID := uuid.New()
		uniqueID = newUUID.String()
		log.G(ctx).Info("Generated unique ID: ", uniqueID, " use Plugin-"+uniqueID+" as service name from Grafana")
	}

	serviceName := "Plugin-" + uniqueID

	res, err := resource.New(ctx,
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	otlpEndpoint := os.Getenv("TELEMETRY_ENDPOINT")

	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4317"
	}

	log.G(ctx).Info("TELEMETRY_ENDPOINT: ", otlpEndpoint)

	caCrtFilePath := os.Getenv("TELEMETRY_CA_CRT_FILEPATH")

	conn := &grpc.ClientConn{}
	if caCrtFilePath != "" {

		// if the CA certificate is provided, set up mutual TLS

		log.G(ctx).Info("CA certificate provided, setting up mutual TLS")

		caCert, err := os.ReadFile(caCrtFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load CA certificate: %w", err)
		}

		clientKeyFilePath := os.Getenv("TELEMETRY_CLIENT_KEY_FILEPATH")
		if clientKeyFilePath == "" {
			return nil, fmt.Errorf("client key file path not provided. Since a CA certificate is provided, a client key is required for mutual TLS")
		}

		clientCrtFilePath := os.Getenv("TELEMETRY_CLIENT_CRT_FILEPATH")
		if clientCrtFilePath == "" {
			return nil, fmt.Errorf("client certificate file path not provided. Since a CA certificate is provided, a client certificate is required for mutual TLS")
		}

		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append CA certificate")
		}

		cert, err := tls.LoadX509KeyPair(clientCrtFilePath, clientKeyFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}

		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{cert},
			RootCAs:            certPool,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
		}
		creds := credentials.NewTLS(tlsConfig)
		conn, err = grpc.NewClient(otlpEndpoint, grpc.WithTransportCredentials(creds), grpc.WithBlock())

	} else {
		conn, err = grpc.NewClient(otlpEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn.WaitForStateChange(ctx, connectivity.Ready)

	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	// Set up a trace exporter
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// Register the trace exporter with a TracerProvider, using a batch
	// span processor to aggregate spans before export.
	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tracerProvider)

	// set global propagator to tracecontext (the default is no-op).
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tracerProvider.Shutdown, nil
}

func main() {
	logger := logrus.StandardLogger()

	// Load unified configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		panic(fmt.Errorf("failed to load configuration: %w", err))
	}

	// Set up logging based on backend configuration
	var verboseLogging, errorsOnlyLogging bool
	switch cfg.BackendType {
	case config.BackendTypeSLURM:
		verboseLogging = cfg.SLURM.VerboseLogging
		errorsOnlyLogging = cfg.SLURM.ErrorsOnlyLogging
	case config.BackendTypeDocker:
		verboseLogging, errorsOnlyLogging = getDockerLogging(cfg)
	case config.BackendTypeContainerd:
		verboseLogging, errorsOnlyLogging = getContainerdLogging(cfg)
	case config.BackendTypePodman:
		verboseLogging = cfg.Podman.VerboseLogging
		errorsOnlyLogging = cfg.Podman.ErrorsOnlyLogging
	case config.BackendTypeHTCondor:
		verboseLogging, errorsOnlyLogging = getHTCondorLogging(cfg)
	}

	if verboseLogging {
		logger.SetLevel(logrus.DebugLevel)
	} else if errorsOnlyLogging {
		logger.SetLevel(logrus.ErrorLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up tracing if enabled
	if os.Getenv("ENABLE_TRACING") == "1" {
		shutdown, err := initProvider(ctx)
		if err != nil {
			log.G(ctx).Fatal(err)
		}
		defer func() {
			if err = shutdown(ctx); err != nil {
				log.G(ctx).Fatal("failed to shutdown TracerProvider: %w", err)
			}
		}()

		log.G(ctx).Info("Tracer setup succeeded")

		// TODO: disable this through options
		trace.T = opentelemetry.Adapter{}
	}

	log.G(ctx).Info("Backend type: ", cfg.BackendType)
	log.G(ctx).Debug("Debug level: " + strconv.FormatBool(verboseLogging))

	// Initialize the appropriate backend
	var batchSystem backend.BatchSystem

	switch cfg.BackendType {
	case config.BackendTypeSLURM:
		log.G(ctx).Info("Initializing SLURM backend")
		JobIDs := make(map[string]*slurm.JidStruct)
		batchSystem = slurmbackend.NewSlurmBackend(ctx, cfg.SLURM, &JobIDs)

	case config.BackendTypeDocker:
		batchSystem, err = initDockerBackend(ctx, cfg)
		if err != nil {
			log.G(ctx).Fatal("Failed to initialize Docker backend: ", err)
		}

	case config.BackendTypeContainerd:
		batchSystem, err = initContainerdBackend(ctx, cfg)
		if err != nil {
			log.G(ctx).Fatal("Failed to initialize Containerd backend: ", err)
		}

	case config.BackendTypePodman:
		log.G(ctx).Info("Initializing Podman backend")
		podmanBackend, err := podman.NewPodmanBackend(ctx, cfg.Podman)
		if err != nil {
			log.G(ctx).Fatal("Failed to initialize Podman backend: ", err)
		}
		batchSystem = podmanBackend

	case config.BackendTypeHTCondor:
		batchSystem, err = initHTCondorBackend(ctx, cfg)
		if err != nil {
			log.G(ctx).Fatal("Failed to initialize HTCondor backend: ", err)
		}

	default:
		log.G(ctx).Fatal("Unknown backend type: ", cfg.BackendType)
	}

	// Create generic handlers
	genericHandler := &handlers.GenericHandler{
		Backend: batchSystem,
		Ctx:     ctx,
	}

	// Set up HTTP routes
	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", genericHandler.StatusHandler)
	mutex.HandleFunc("/create", genericHandler.SubmitHandler)
	mutex.HandleFunc("/delete", genericHandler.StopHandler)
	mutex.HandleFunc("/getLogs", genericHandler.GetLogsHandler)
	mutex.HandleFunc("/system-info", genericHandler.SystemInfoHandler)

	// Initialize backend storage and load existing jobs
	if err := batchSystem.CreateDirectories(); err != nil {
		log.G(ctx).Warn("Failed to create directories: ", err)
	}
	if err := batchSystem.LoadJobs(); err != nil {
		log.G(ctx).Warn("Failed to load existing jobs: ", err)
	}

	// Determine socket/port from configuration
	var socket, sidecarPort string
	switch cfg.BackendType {
	case config.BackendTypeSLURM:
		socket = cfg.SLURM.Socket
		sidecarPort = cfg.SLURM.Sidecarport
	case config.BackendTypeDocker:
		// Docker backend uses port by default
		sidecarPort = "4000"
	case config.BackendTypeContainerd:
		// Containerd backend uses port by default
		sidecarPort = "4000"
	case config.BackendTypePodman:
		// Podman backend uses port by default
		sidecarPort = "4000"
	case config.BackendTypeHTCondor:
		// HTCondor backend uses port by default
		sidecarPort = "4000"
	}

	// Start HTTP server
	if strings.HasPrefix(socket, "unix://") {
		// Create a Unix domain socket and listen for incoming connections.
		socketPath := strings.ReplaceAll(socket, "unix://", "")
		unixSocket, err := net.Listen("unix", socketPath)
		if err != nil {
			panic(err)
		}

		// Cleanup the sockfile.
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-c
			os.Remove(socketPath)
			os.Exit(1)
		}()
		server := http.Server{
			Handler: mutex,
		}

		log.G(ctx).Info("Listening on Unix socket: ", socketPath)

		if err := server.Serve(unixSocket); err != nil {
			log.G(ctx).Fatal(err)
		}
	} else {
		log.G(ctx).Info("Listening on TCP port: ", sidecarPort)
		err = http.ListenAndServe(":"+sidecarPort, mutex)
		if err != nil {
			log.G(ctx).Fatal(err)
		}
	}
}
