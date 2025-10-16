//go:build !containerd
// +build !containerd

package main

import (
	"context"
	"fmt"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/config"
)

func initContainerdBackend(ctx context.Context, cfg *config.UnifiedConfig) (backend.BatchSystem, error) {
	return nil, fmt.Errorf("Containerd backend not compiled in this build")
}

func getContainerdLogging(_ *config.UnifiedConfig) (verboseLogging, errorsOnlyLogging bool) {
	return false, false
}
