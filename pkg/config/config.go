package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/containerd"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/docker"
	"github.com/intertwin-eu/interlink-slurm-plugin/pkg/backend/htcondor"
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
	BackendTypeHTCondor    BackendType = "htcondor"
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

	// HTCondor-specific configuration (used when BackendType is "htcondor")
	HTCondor *htcondor.HTCondorConfig `yaml:"HTCondor,omitempty"`
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
	case BackendTypeContainerd:
		if config.Containerd == nil {
			return nil, fmt.Errorf("Containerd backend selected but no Containerd configuration provided")
		}
		// Set defaults for Containerd config
		if config.Containerd.DataRootFolder == "" {
			config.Containerd.DataRootFolder = "/var/interlink/containerd"
		}
		if config.Containerd.Socket == "" {
			config.Containerd.Socket = "/run/containerd/containerd.sock"
		}
		if config.Containerd.Namespace == "" {
			config.Containerd.Namespace = "interlink"
		}
		if config.Containerd.Snapshotter == "" {
			config.Containerd.Snapshotter = "overlayfs"
		}
		if config.Containerd.Runtime == "" {
			config.Containerd.Runtime = "io.containerd.runc.v2"
		}
	case BackendTypePodman:
		if config.Podman == nil {
			return nil, fmt.Errorf("Podman backend selected but no Podman configuration provided")
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
		// Set defaults for HTCondor config
		if config.HTCondor.CondorSubmitPath == "" {
			config.HTCondor.CondorSubmitPath = "/usr/bin/condor_submit"
		}
		if config.HTCondor.CondorQPath == "" {
			config.HTCondor.CondorQPath = "/usr/bin/condor_q"
		}
		if config.HTCondor.CondorRmPath == "" {
			config.HTCondor.CondorRmPath = "/usr/bin/condor_rm"
		}
		if config.HTCondor.CondorHistoryPath == "" {
			config.HTCondor.CondorHistoryPath = "/usr/bin/condor_history"
		}
		if config.HTCondor.SingularityPath == "" {
			config.HTCondor.SingularityPath = "/usr/bin/singularity"
		}
		if config.HTCondor.SpoolDirectory == "" {
			config.HTCondor.SpoolDirectory = "/var/interlink/htcondor/spool"
		}
		if config.HTCondor.DataRootFolder == "" {
			config.HTCondor.DataRootFolder = "/var/interlink/htcondor"
		}
		// Log streaming is always disabled for HTCondor (no shared filesystem)
		config.HTCondor.EnableLogStreaming = false
	default:
		return nil, fmt.Errorf("unknown backend type: %s (must be 'slurm', 'docker', 'containerd', 'podman', or 'htcondor')", config.BackendType)
	}

	return &config, nil
}
