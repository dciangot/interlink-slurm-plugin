//go:build !docker && !containerd && !htcondor
// +build !docker,!containerd,!htcondor

package config

// Stub types for excluded backends to allow config parsing

type DockerConfig struct{}
type ContainerdConfig struct{}
type HTCondorConfig struct{}

// Package-level variables to satisfy references
var (
	docker     = struct{ DockerConfig }{}
	containerd = struct{ ContainerdConfig }{}
	htcondor   = struct{ HTCondorConfig }{}
)
