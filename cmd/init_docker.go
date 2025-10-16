//go:build docker
// +build docker

package main

import (
	"context"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/docker"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/config"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

func initDockerBackend(ctx context.Context, cfg *config.UnifiedConfig) (backend.BatchSystem, error) {
	log.G(ctx).Info("Initializing Docker backend")
	return docker.NewDockerBackend(ctx, cfg.Docker)
}

func getDockerLogging(cfg *config.UnifiedConfig) (verboseLogging, errorsOnlyLogging bool) {
	return cfg.Docker.VerboseLogging, cfg.Docker.ErrorsOnlyLogging
}
