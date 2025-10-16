//go:build containerd
// +build containerd

package main

import (
	"context"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/containerd"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/config"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

func initContainerdBackend(ctx context.Context, cfg *config.UnifiedConfig) (backend.BatchSystem, error) {
	log.G(ctx).Info("Initializing Containerd backend")
	return containerd.NewContainerdBackend(ctx, cfg.Containerd)
}

func getContainerdLogging(cfg *config.UnifiedConfig) (verboseLogging, errorsOnlyLogging bool) {
	return cfg.Containerd.VerboseLogging, cfg.Containerd.ErrorsOnlyLogging
}
