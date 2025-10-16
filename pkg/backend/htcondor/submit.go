package htcondor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/log"
	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	v1 "k8s.io/api/core/v1"
)

// submitContainer generates and submits an HTCondor submit file for a single container
func (h *HTCondorBackend) submitContainer(
	ctx context.Context,
	podData *commonIL.RetrievedPodData,
	container v1.Container,
	isInit bool,
	spoolDir string,
) (string, error) {
	podUID := string(podData.Pod.UID)
	containerName := container.Name
	submitFileName := fmt.Sprintf("%s-%s.sub", podUID, containerName)
	submitFilePath := filepath.Join(spoolDir, submitFileName)

	log.G(ctx).Infof("Generating HTCondor submit file: %s", submitFilePath)

	// Build Singularity command
	singularityCmd, err := h.buildSingularityCommand(ctx, podData, container, spoolDir)
	if err != nil {
		return "", fmt.Errorf("failed to build Singularity command: %w", err)
	}

	// Generate submit file content
	submitContent, err := h.generateSubmitFile(ctx, podData, container, singularityCmd, spoolDir, isInit)
	if err != nil {
		return "", fmt.Errorf("failed to generate submit file: %w", err)
	}

	// Write submit file
	if err := os.WriteFile(submitFilePath, []byte(submitContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write submit file: %w", err)
	}

	// Submit to HTCondor
	jobID, err := h.executeCondorSubmit(ctx, submitFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to submit job: %w", err)
	}

	log.G(ctx).Infof("Successfully submitted container %s with HTCondor job ID: %s", containerName, jobID)
	return jobID, nil
}

// buildSingularityCommand constructs the Singularity command with all bind mounts and options
func (h *HTCondorBackend) buildSingularityCommand(
	ctx context.Context,
	podData *commonIL.RetrievedPodData,
	container v1.Container,
	spoolDir string,
) (string, error) {
	var args []string

	// Start with Singularity exec command
	args = append(args, h.config.SingularityPath, "exec")

	// Add Singularity options from config
	args = append(args, h.config.SingularityOptions...)

	// Add bind mounts for volumes
	bindMounts, err := h.buildBindMounts(ctx, podData, container, spoolDir)
	if err != nil {
		return "", err
	}
	args = append(args, bindMounts...)

	// Add environment variables
	for _, env := range container.Env {
		args = append(args, "--env", fmt.Sprintf("%s=%s", env.Name, env.Value))
	}

	// Add container image
	imagePath := h.resolveImagePath(container.Image)
	args = append(args, imagePath)

	// Add container command and args
	if len(container.Command) > 0 {
		args = append(args, container.Command...)
	}
	if len(container.Args) > 0 {
		args = append(args, container.Args...)
	}

	return strings.Join(args, " "), nil
}

// buildBindMounts generates Singularity bind mount arguments for all volume types
func (h *HTCondorBackend) buildBindMounts(
	ctx context.Context,
	podData *commonIL.RetrievedPodData,
	container v1.Container,
	spoolDir string,
) ([]string, error) {
	var bindArgs []string

	for _, volumeMount := range container.VolumeMounts {
		// Find the corresponding volume in the pod spec
		volume := findVolume(podData.Pod.Spec.Volumes, volumeMount.Name)
		if volume == nil {
			log.G(ctx).Warnf("Volume %s not found in pod spec", volumeMount.Name)
			continue
		}

		bindMount, err := h.buildVolumeBindMount(ctx, volume, volumeMount, spoolDir)
		if err != nil {
			return nil, fmt.Errorf("failed to build bind mount for volume %s: %w", volumeMount.Name, err)
		}

		if bindMount != "" {
			bindArgs = append(bindArgs, "--bind", bindMount)
		}
	}

	return bindArgs, nil
}

// buildVolumeBindMount creates a bind mount string for a specific volume type
func (h *HTCondorBackend) buildVolumeBindMount(
	ctx context.Context,
	volume *v1.Volume,
	volumeMount v1.VolumeMount,
	spoolDir string,
) (string, error) {
	switch {
	case volume.HostPath != nil:
		// HostPath: direct bind mount from execute node
		// Format: /host/path:/container/path[:ro]
		bindMount := fmt.Sprintf("%s:%s", volume.HostPath.Path, volumeMount.MountPath)
		if volumeMount.ReadOnly {
			bindMount += ":ro"
		}
		log.G(ctx).Debugf("HostPath bind mount: %s", bindMount)
		return bindMount, nil

	case volume.ConfigMap != nil:
		// ConfigMap: mounted from transferred spool directory
		// The tar.gz will be extracted to _condor_scratch_dir/configmaps/<name>
		configMapDir := fmt.Sprintf("configmaps/%s", volume.ConfigMap.Name)
		bindMount := fmt.Sprintf("%s:%s", configMapDir, volumeMount.MountPath)
		if volumeMount.ReadOnly {
			bindMount += ":ro"
		}
		log.G(ctx).Debugf("ConfigMap bind mount: %s", bindMount)
		return bindMount, nil

	case volume.Secret != nil:
		// Secret: mounted from transferred spool directory
		// The tar.gz will be extracted to _condor_scratch_dir/secrets/<name>
		secretDir := fmt.Sprintf("secrets/%s", volume.Secret.SecretName)
		bindMount := fmt.Sprintf("%s:%s", secretDir, volumeMount.MountPath)
		if volumeMount.ReadOnly {
			bindMount += ":ro"
		}
		log.G(ctx).Debugf("Secret bind mount: %s", bindMount)
		return bindMount, nil

	case volume.EmptyDir != nil:
		// EmptyDir: created locally in working directory
		// Create subdirectory in _condor_scratch_dir
		emptyDirPath := fmt.Sprintf("emptydir/%s", volume.Name)
		bindMount := fmt.Sprintf("%s:%s", emptyDirPath, volumeMount.MountPath)
		log.G(ctx).Debugf("EmptyDir bind mount: %s", bindMount)
		return bindMount, nil

	default:
		log.G(ctx).Warnf("Unsupported volume type for volume %s", volume.Name)
		return "", nil
	}
}

// generateSubmitFile creates the HTCondor submit file content
func (h *HTCondorBackend) generateSubmitFile(
	ctx context.Context,
	podData *commonIL.RetrievedPodData,
	container v1.Container,
	singularityCmd string,
	spoolDir string,
	isInit bool,
) (string, error) {
	var sb strings.Builder

	podUID := string(podData.Pod.UID)
	containerName := container.Name

	// Header
	sb.WriteString("# HTCondor Submit File\n")
	sb.WriteString(fmt.Sprintf("# Generated for pod: %s/%s\n", podData.Pod.Namespace, podData.Pod.Name))
	sb.WriteString(fmt.Sprintf("# Container: %s\n", containerName))
	sb.WriteString(fmt.Sprintf("# Is Init Container: %v\n\n", isInit))

	// Universe
	sb.WriteString("universe = vanilla\n")

	// Executable - use wrapper script
	wrapperScript := h.generateWrapperScript(ctx, singularityCmd, container, spoolDir)
	wrapperPath := filepath.Join(spoolDir, fmt.Sprintf("wrapper-%s-%s.sh", podUID, containerName))
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0755); err != nil {
		return "", fmt.Errorf("failed to write wrapper script: %w", err)
	}
	sb.WriteString(fmt.Sprintf("executable = %s\n", wrapperPath))

	// Output, Error, Log files
	sb.WriteString(fmt.Sprintf("output = %s/output-%s-%s.txt\n", spoolDir, podUID, containerName))
	sb.WriteString(fmt.Sprintf("error = %s/error-%s-%s.txt\n", spoolDir, podUID, containerName))
	sb.WriteString(fmt.Sprintf("log = %s/log-%s-%s.txt\n", spoolDir, podUID, containerName))

	// Resource requirements
	cpuLimit := container.Resources.Limits.Cpu().AsApproximateFloat64()
	memoryLimit := container.Resources.Limits.Memory().Value() / (1024 * 1024) // Convert to MB

	if cpuLimit > 0 {
		sb.WriteString(fmt.Sprintf("request_cpus = %d\n", int(cpuLimit)+1))
	} else {
		sb.WriteString("request_cpus = 1\n")
	}

	if memoryLimit > 0 {
		sb.WriteString(fmt.Sprintf("request_memory = %d MB\n", memoryLimit))
	} else {
		sb.WriteString("request_memory = 1024 MB\n")
	}

	// File transfer
	sb.WriteString("should_transfer_files = YES\n")
	sb.WriteString("when_to_transfer_output = ON_EXIT\n")

	// Build transfer_input_files list
	inputFiles := []string{wrapperPath}

	// Add ConfigMap and Secret archives
	for _, volume := range podData.Pod.Spec.Volumes {
		if volume.ConfigMap != nil {
			archivePath := filepath.Join(spoolDir, fmt.Sprintf("configmap-%s.tar.gz", volume.ConfigMap.Name))
			if _, err := os.Stat(archivePath); err == nil {
				inputFiles = append(inputFiles, archivePath)
			}
		}
		if volume.Secret != nil {
			archivePath := filepath.Join(spoolDir, fmt.Sprintf("secret-%s.tar.gz", volume.Secret.SecretName))
			if _, err := os.Stat(archivePath); err == nil {
				inputFiles = append(inputFiles, archivePath)
			}
		}
	}

	if len(inputFiles) > 0 {
		sb.WriteString(fmt.Sprintf("transfer_input_files = %s\n", strings.Join(inputFiles, ",")))
	}

	// Transfer output files (logs)
	sb.WriteString("transfer_output_files = \n") // Empty for now, logs are in output/error files

	// Job requirements and preferences
	if h.config.Requirements != "" {
		sb.WriteString(fmt.Sprintf("requirements = %s\n", h.config.Requirements))
	}

	// Custom ClassAds from pod annotations
	if classAds, ok := podData.Pod.Annotations["htcondor.interlink.io/classads"]; ok {
		sb.WriteString("\n# Custom ClassAds\n")
		sb.WriteString(classAds)
		sb.WriteString("\n")
	}

	// Job notification
	sb.WriteString("notification = Never\n")

	// Queue the job
	sb.WriteString("\nqueue 1\n")

	return sb.String(), nil
}

// generateWrapperScript creates a wrapper script that sets up the environment and runs Singularity
func (h *HTCondorBackend) generateWrapperScript(
	ctx context.Context,
	singularityCmd string,
	container v1.Container,
	spoolDir string,
) string {
	var sb strings.Builder

	sb.WriteString("#!/bin/bash\n\n")
	sb.WriteString("set -e\n\n")

	sb.WriteString("# HTCondor Job Wrapper Script\n")
	sb.WriteString(fmt.Sprintf("# Container: %s\n\n", container.Name))

	// Extract ConfigMaps and Secrets if present
	sb.WriteString("# Extract ConfigMaps and Secrets\n")
	sb.WriteString("mkdir -p configmaps secrets emptydir\n\n")

	sb.WriteString("for archive in configmap-*.tar.gz; do\n")
	sb.WriteString("  if [ -f \"$archive\" ]; then\n")
	sb.WriteString("    name=$(echo $archive | sed 's/configmap-\\(.*\\)\\.tar\\.gz/\\1/')\n")
	sb.WriteString("    mkdir -p configmaps/$name\n")
	sb.WriteString("    tar -xzf $archive -C configmaps/$name\n")
	sb.WriteString("    echo \"Extracted ConfigMap: $name\"\n")
	sb.WriteString("  fi\n")
	sb.WriteString("done\n\n")

	sb.WriteString("for archive in secret-*.tar.gz; do\n")
	sb.WriteString("  if [ -f \"$archive\" ]; then\n")
	sb.WriteString("    name=$(echo $archive | sed 's/secret-\\(.*\\)\\.tar\\.gz/\\1/')\n")
	sb.WriteString("    mkdir -p secrets/$name\n")
	sb.WriteString("    tar -xzf $archive -C secrets/$name\n")
	sb.WriteString("    echo \"Extracted Secret: $name\"\n")
	sb.WriteString("  fi\n")
	sb.WriteString("done\n\n")

	// Create EmptyDir directories
	sb.WriteString("# Create EmptyDir directories\n")
	sb.WriteString("# (Will be created based on bind mounts)\n\n")

	// Environment variables
	sb.WriteString("# Set environment variables\n")
	for _, env := range container.Env {
		sb.WriteString(fmt.Sprintf("export %s=\"%s\"\n", env.Name, env.Value))
	}
	sb.WriteString("\n")

	// Run Singularity
	sb.WriteString("# Execute Singularity container\n")
	sb.WriteString("echo \"Starting container execution...\"\n")
	sb.WriteString(singularityCmd)
	sb.WriteString("\n\n")

	sb.WriteString("echo \"Container execution completed\"\n")

	return sb.String()
}

// executeCondorSubmit submits the job and returns the job ID
func (h *HTCondorBackend) executeCondorSubmit(ctx context.Context, submitFilePath string) (string, error) {
	cmd := fmt.Sprintf("%s %s", h.config.CondorSubmitPath, submitFilePath)
	
	log.G(ctx).Debugf("Executing: %s", cmd)

	output, err := h.executeCommand(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("condor_submit failed: %w", err)
	}

	// Parse job ID from output
	// Expected format: "Submitting job(s).\n1 job(s) submitted to cluster 12345."
	jobID, err := h.parseCondorSubmitOutput(output)
	if err != nil {
		return "", fmt.Errorf("failed to parse job ID from condor_submit output: %w", err)
	}

	return jobID, nil
}

// parseCondorSubmitOutput extracts the cluster ID from condor_submit output
func (h *HTCondorBackend) parseCondorSubmitOutput(output string) (string, error) {
	// Look for pattern: "submitted to cluster XXXX"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "submitted to cluster") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "cluster" && i+1 < len(fields) {
					clusterID := strings.TrimSuffix(fields[i+1], ".")
					return clusterID, nil
				}
			}
		}
	}
	return "", fmt.Errorf("could not find cluster ID in output: %s", output)
}

// resolveImagePath converts a container image reference to a Singularity image path
func (h *HTCondorBackend) resolveImagePath(imageRef string) string {
	// If it's already a local path, use it directly
	if strings.HasPrefix(imageRef, "/") || strings.HasSuffix(imageRef, ".sif") {
		return imageRef
	}

	// Convert Docker image to Singularity format
	// docker://ubuntu:20.04 or docker.io/library/ubuntu:20.04
	if !strings.HasPrefix(imageRef, "docker://") {
		imageRef = "docker://" + imageRef
	}

	return imageRef
}

// findVolume finds a volume by name in the volume list
func findVolume(volumes []v1.Volume, name string) *v1.Volume {
	for i := range volumes {
		if volumes[i].Name == name {
			return &volumes[i]
		}
	}
	return nil
}

// submitContainerDependency submits a container that depends on another job completing
func (h *HTCondorBackend) submitContainerDependency(
	ctx context.Context,
	podData *commonIL.RetrievedPodData,
	container v1.Container,
	isInit bool,
	spoolDir string,
	dependsOnJobID string,
) (string, error) {
	podUID := string(podData.Pod.UID)
	containerName := container.Name
	submitFileName := fmt.Sprintf("%s-%s.sub", podUID, containerName)
	submitFilePath := filepath.Join(spoolDir, submitFileName)

	log.G(ctx).Infof("Generating HTCondor submit file with dependency: %s", submitFilePath)

	// Build Singularity command
	singularityCmd, err := h.buildSingularityCommand(ctx, podData, container, spoolDir)
	if err != nil {
		return "", fmt.Errorf("failed to build Singularity command: %w", err)
	}

	// Generate base submit file content
	submitContent, err := h.generateSubmitFile(ctx, podData, container, singularityCmd, spoolDir, isInit)
	if err != nil {
		return "", fmt.Errorf("failed to generate submit file: %w", err)
	}

	// Add dependency (job must wait for previous job to complete)
	// Insert before "queue 1"
	submitContent = strings.Replace(
		submitContent,
		"\nqueue 1\n",
		fmt.Sprintf("\n# Job dependency\nDAGMan_status = %s\n\nqueue 1\n", dependsOnJobID),
		1,
	)

	// Write submit file
	if err := os.WriteFile(submitFilePath, []byte(submitContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write submit file: %w", err)
	}

	// Submit to HTCondor
	jobID, err := h.executeCondorSubmit(ctx, submitFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to submit job: %w", err)
	}

	log.G(ctx).Infof("Successfully submitted container %s with HTCondor job ID: %s (depends on %s)", containerName, jobID, dependsOnJobID)
	return jobID, nil
}
