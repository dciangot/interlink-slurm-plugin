package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/podman"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/slurm"
)

// BackendType represents the type of batch system backend
type BackendType string

const (
	BackendTypeSLURM      BackendType = "slurm"
	BackendTypeDocker     BackendType = "docker"
	BackendTypeContainerd BackendType = "containerd"
	BackendTypePodman     BackendType = "podman"
	BackendTypeHTCondor   BackendType = "htcondor"
)

// UnifiedConfig holds configuration for any backend type
type UnifiedConfig struct {
	// BackendType specifies which batch system to use ("slurm", "docker", "containerd", or "podman")
	BackendType BackendType `yaml:"BackendType"`

	// SLURM-specific configuration (used when BackendType is "slurm")
	SLURM *slurm.SlurmConfig `yaml:"SLURM,omitempty"`

	// Docker-specific configuration (used when BackendType is "docker")
	Docker *DockerConfig `yaml:"Docker,omitempty"`

	// Containerd-specific configuration (used when BackendType is "containerd")
	Containerd *ContainerdConfig `yaml:"Containerd,omitempty"`

	// Podman-specific configuration (used when BackendType is "podman")
	Podman *podman.PodmanConfig `yaml:"Podman,omitempty"`

	// HTCondor-specific configuration (used when BackendType is "htcondor")
	HTCondor *HTCondorConfig `yaml:"HTCondor,omitempty"`
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
			return nil, fmt.Errorf("docker backend selected but no Docker configuration provided")
		}
	case BackendTypeContainerd:
		if config.Containerd == nil {
			return nil, fmt.Errorf("containerd backend selected but no Containerd configuration provided")
		}
	case BackendTypePodman:
		if config.Podman == nil {
			return nil, fmt.Errorf("podman backend selected but no Podman configuration provided")
		}
		// Set defaults for Podman config
		if config.Podman.DataRootFolder == "" {
			config.Podman.DataRootFolder = "/var/interlink/podman"
		}
		if config.Podman.Namespace == "" {
			config.Podman.Namespace = "default"
		}
	case BackendTypeHTCondor:
		if config.HTCondor == nil {
			return nil, fmt.Errorf("HTCondor backend selected but no HTCondor configuration provided")
		}
	default:
		return nil, fmt.Errorf("unknown backend type: %s (must be 'slurm', 'docker', 'containerd', 'podman', or 'htcondor')", config.BackendType)
	}

	return &config, nil
}
