//go:build !htcondor
// +build !htcondor

package main

import (
	"context"
	"fmt"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/config"
)

func initHTCondorBackend(ctx context.Context, cfg *config.UnifiedConfig) (backend.BatchSystem, error) {
	return nil, fmt.Errorf("HTCondor backend not compiled in this build")
}

func getHTCondorLogging(cfg *config.UnifiedConfig) (verboseLogging, errorsOnlyLogging bool) {
	return false, false
}
