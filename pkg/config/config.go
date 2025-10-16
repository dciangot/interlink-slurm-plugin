package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/containerd"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/docker"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/podman"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/slurm"
)

// BackendType represents the type of batch system backend
type BackendType string

const (
	BackendTypeSLURM       BackendType = "slurm"
	BackendTypeDocker      BackendType = "docker"
	BackendTypeContainerd  BackendType = "containerd"
	BackendTypePodman      BackendType = "podman"
)

// UnifiedConfig holds configuration for any backend type
type UnifiedConfig struct {
	// BackendType specifies which batch system to use ("slurm", "docker", "containerd", or "podman")
	BackendType BackendType `yaml:"BackendType"`

	// SLURM-specific configuration (used when BackendType is "slurm")
	SLURM *slurm.SlurmConfig `yaml:"SLURM,omitempty"`

	// Docker-specific configuration (used when BackendType is "docker")
	Docker *docker.DockerConfig `yaml:"Docker,omitempty"`

	// Containerd-specific configuration (used when BackendType is "containerd")
	Containerd *containerd.ContainerdConfig `yaml:"Containerd,omitempty"`

	// Podman-specific configuration (used when BackendType is "podman")
	Podman *podman.PodmanConfig `yaml:"Podman,omitempty"`
}

// LoadConfig loads configuration from the specified path or default location
// It supports both the new unified format and legacy SLURM-only format for backward compatibility
func LoadConfig() (*UnifiedConfig, error) {
	configPath := os.Getenv("INTERLINK_CONFIG_PATH")
	if configPath == "" {
		// Check for legacy SLURM config path
		configPath = os.Getenv("SLURMCONFIGPATH")
		if configPath == "" {
			configPath = "/etc/interlink/InterLinkConfig.yaml"
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	var config UnifiedConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// If BackendType is not specified, try to infer from config structure
	if config.BackendType == "" {
		// If Docker config exists, use Docker backend
		if config.Docker != nil {
			config.BackendType = BackendTypeDocker
		} else if config.SLURM != nil {
			config.BackendType = BackendTypeSLURM
		} else {
			// Legacy format: assume it's a SLURM config
			var slurmConfig slurm.SlurmConfig
			if err := yaml.Unmarshal(data, &slurmConfig); err == nil {
				config.BackendType = BackendTypeSLURM
				config.SLURM = &slurmConfig
			} else {
				return nil, fmt.Errorf("could not determine backend type from config")
			}
		}
	}

	// Validate that required config section exists
	switch config.BackendType {
	case BackendTypeSLURM:
		if config.SLURM == nil {
			return nil, fmt.Errorf("SLURM backend selected but no SLURM configuration provided")
		}
	case BackendTypeDocker:
		if config.Docker == nil {
			return nil, fmt.Errorf("Docker backend selected but no Docker configuration provided")
		}
		// Set defaults for Docker config
		if config.Docker.DataRootFolder == "" {
			config.Docker.DataRootFolder = "/var/interlink"
		}
		if config.Docker.Endpoint == "" {
			config.Docker.Endpoint = "unix:///var/run/docker.sock"
		}
	default:
		return nil, fmt.Errorf("unknown backend type: %s (must be 'slurm' or 'docker')", config.BackendType)
	}

	return &config, nil
}
