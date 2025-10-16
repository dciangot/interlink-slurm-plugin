//go:build !docker
// +build !docker

package main

import (
	"context"
	"fmt"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/config"
)

func initDockerBackend(ctx context.Context, cfg *config.UnifiedConfig) (backend.BatchSystem, error) {
	return nil, fmt.Errorf("Docker backend not compiled in this build")
}

func getDockerLogging(cfg *config.UnifiedConfig) (verboseLogging, errorsOnlyLogging bool) {
	return false, false
}
