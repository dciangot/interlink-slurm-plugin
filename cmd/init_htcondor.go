//go:build htcondor
// +build htcondor

package main

import (
	"context"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/htcondor"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/config"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

func initHTCondorBackend(ctx context.Context, cfg *config.UnifiedConfig) (backend.BatchSystem, error) {
	log.G(ctx).Info("Initializing HTCondor backend")
	return htcondor.NewHTCondorBackend(ctx, cfg.HTCondor)
}

func getHTCondorLogging(cfg *config.UnifiedConfig) (verboseLogging, errorsOnlyLogging bool) {
	return cfg.HTCondor.VerboseLogging, cfg.HTCondor.ErrorsOnlyLogging
}
