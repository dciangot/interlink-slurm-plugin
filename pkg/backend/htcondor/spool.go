//go:build htcondor
// +build htcondor

package htcondor

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/log"
	commonIL "github.com/interlink-hq/interlink/pkg/interlink"
	v1 "k8s.io/api/core/v1"
)

// createSpoolDirectory creates the spool directory for a pod
func (h *HTCondorBackend) createSpoolDirectory(ctx context.Context, podUID string) (string, error) {
	spoolDir := filepath.Join(h.config.SpoolDirectory, podUID)
	
	log.G(ctx).Infof("Creating spool directory: %s", spoolDir)
	
	if err := os.MkdirAll(spoolDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create spool directory: %w", err)
	}
	
	return spoolDir, nil
}

// stageConfigMapsAndSecrets creates tar.gz archives of ConfigMaps and Secrets
func (h *HTCondorBackend) stageConfigMapsAndSecrets(
	ctx context.Context,
	podData *commonIL.RetrievedPodData,
	spoolDir string,
) error {
	log.G(ctx).Info("Staging ConfigMaps and Secrets")
	
	// Process each volume
	for _, volume := range podData.Pod.Spec.Volumes {
		if volume.ConfigMap != nil {
			if err := h.stageConfigMap(ctx, podData, volume.ConfigMap, spoolDir); err != nil {
				return fmt.Errorf("failed to stage ConfigMap %s: %w", volume.ConfigMap.Name, err)
			}
		}
		
		if volume.Secret != nil {
			if err := h.stageSecret(ctx, podData, volume.Secret, spoolDir); err != nil {
				return fmt.Errorf("failed to stage Secret %s: %w", volume.Secret.SecretName, err)
			}
		}
	}
	
	return nil
}

// stageConfigMap creates a tar.gz archive of a ConfigMap
func (h *HTCondorBackend) stageConfigMap(
	ctx context.Context,
	podData *commonIL.RetrievedPodData,
	configMap *v1.ConfigMapVolumeSource,
	spoolDir string,
) error {
	configMapName := configMap.Name
	log.G(ctx).Infof("Staging ConfigMap: %s", configMapName)
	
	// Find the ConfigMap data in the retrieved pod data
	var configMapData map[string]string
	for _, cm := range podData.ConfigMaps {
		if cm.Name == configMapName {
			configMapData = cm.Data
			break
		}
	}
	
	if configMapData == nil {
		return fmt.Errorf("ConfigMap %s not found in pod data", configMapName)
	}
	
	// Create tar.gz archive
	archivePath := filepath.Join(spoolDir, fmt.Sprintf("configmap-%s.tar.gz", configMapName))
	
	if err := h.createTarGz(ctx, archivePath, configMapData); err != nil {
		return fmt.Errorf("failed to create ConfigMap archive: %w", err)
	}
	
	log.G(ctx).Infof("ConfigMap %s staged to %s", configMapName, archivePath)
	return nil
}

// stageSecret creates a tar.gz archive of a Secret
func (h *HTCondorBackend) stageSecret(
	ctx context.Context,
	podData *commonIL.RetrievedPodData,
	secret *v1.SecretVolumeSource,
	spoolDir string,
) error {
	secretName := secret.SecretName
	log.G(ctx).Infof("Staging Secret: %s", secretName)
	
	// Find the Secret data in the retrieved pod data
	var secretData map[string][]byte
	for _, s := range podData.Secrets {
		if s.Name == secretName {
			secretData = s.Data
			break
		}
	}
	
	if secretData == nil {
		return fmt.Errorf("Secret %s not found in pod data", secretName)
	}
	
	// Convert []byte values to string for archiving
	stringData := make(map[string]string)
	for key, value := range secretData {
		stringData[key] = string(value)
	}
	
	// Create tar.gz archive
	archivePath := filepath.Join(spoolDir, fmt.Sprintf("secret-%s.tar.gz", secretName))
	
	if err := h.createTarGz(ctx, archivePath, stringData); err != nil {
		return fmt.Errorf("failed to create Secret archive: %w", err)
	}
	
	log.G(ctx).Infof("Secret %s staged to %s", secretName, archivePath)
	return nil
}

// createTarGz creates a tar.gz archive from a map of file contents
func (h *HTCondorBackend) createTarGz(ctx context.Context, archivePath string, files map[string]string) error {
	// Create the archive file
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}
	defer archiveFile.Close()
	
	// Create gzip writer
	gzipWriter := gzip.NewWriter(archiveFile)
	defer gzipWriter.Close()
	
	// Create tar writer
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()
	
	// Add each file to the archive
	for filename, content := range files {
		if err := h.addFileToTar(tarWriter, filename, []byte(content)); err != nil {
			return fmt.Errorf("failed to add file %s to archive: %w", filename, err)
		}
	}
	
	log.G(ctx).Debugf("Created archive: %s with %d files", archivePath, len(files))
	return nil
}

// addFileToTar adds a single file to a tar archive
func (h *HTCondorBackend) addFileToTar(tarWriter *tar.Writer, filename string, content []byte) error {
	// Create tar header
	header := &tar.Header{
		Name: filename,
		Mode: 0644,
		Size: int64(len(content)),
	}
	
	// Write header
	if err := tarWriter.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header: %w", err)
	}
	
	// Write content
	if _, err := tarWriter.Write(content); err != nil {
		return fmt.Errorf("failed to write tar content: %w", err)
	}
	
	return nil
}

// cleanupSpoolDirectory removes the spool directory for a pod
func (h *HTCondorBackend) cleanupSpoolDirectory(ctx context.Context, podUID string) error {
	spoolDir := filepath.Join(h.config.SpoolDirectory, podUID)
	
	log.G(ctx).Infof("Cleaning up spool directory: %s", spoolDir)
	
	if err := os.RemoveAll(spoolDir); err != nil {
		return fmt.Errorf("failed to remove spool directory: %w", err)
	}
	
	return nil
}

// extractTarGz extracts a tar.gz archive to a directory
func (h *HTCondorBackend) extractTarGz(ctx context.Context, archivePath, destDir string) error {
	log.G(ctx).Debugf("Extracting archive %s to %s", archivePath, destDir)
	
	// Open the archive file
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer archiveFile.Close()
	
	// Create gzip reader
	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()
	
	// Create tar reader
	tarReader := tar.NewReader(gzipReader)
	
	// Extract each file
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}
		
		// Create destination path
		destPath := filepath.Join(destDir, header.Name)
		
		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %w", err)
		}
		
		// Create the file
		destFile, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", destPath, err)
		}
		
		// Copy content
		if _, err := io.Copy(destFile, tarReader); err != nil {
			destFile.Close()
			return fmt.Errorf("failed to extract file %s: %w", destPath, err)
		}
		
		destFile.Close()
		
		// Set file permissions
		if err := os.Chmod(destPath, os.FileMode(header.Mode)); err != nil {
			return fmt.Errorf("failed to set permissions on %s: %w", destPath, err)
		}
	}
	
	log.G(ctx).Debugf("Archive extracted successfully")
	return nil
}

// getSpoolDirectory returns the spool directory path for a pod
func (h *HTCondorBackend) getSpoolDirectory(podUID string) string {
	return filepath.Join(h.config.SpoolDirectory, podUID)
}

// ensureSpoolBaseDirectory ensures the base spool directory exists
func (h *HTCondorBackend) ensureSpoolBaseDirectory(ctx context.Context) error {
	log.G(ctx).Infof("Ensuring base spool directory exists: %s", h.config.SpoolDirectory)
	
	if err := os.MkdirAll(h.config.SpoolDirectory, 0755); err != nil {
		return fmt.Errorf("failed to create base spool directory: %w", err)
	}
	
	return nil
}

// listSpoolDirectories lists all pod spool directories
func (h *HTCondorBackend) listSpoolDirectories(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(h.config.SpoolDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read spool directory: %w", err)
	}
	
	var podUIDs []string
	for _, entry := range entries {
		if entry.IsDir() {
			podUIDs = append(podUIDs, entry.Name())
		}
	}
	
	return podUIDs, nil
}

// archiveJobOutput archives job output files for later retrieval
func (h *HTCondorBackend) archiveJobOutput(ctx context.Context, spoolDir, containerName string) error {
	log.G(ctx).Debugf("Archiving job output for container %s", containerName)
	
	// Look for output and error files
	outputFiles := []string{
		fmt.Sprintf("output-%s.txt", containerName),
		fmt.Sprintf("error-%s.txt", containerName),
		fmt.Sprintf("log-%s.txt", containerName),
	}
	
	archiveData := make(map[string]string)
	
	for _, filename := range outputFiles {
		filePath := filepath.Join(spoolDir, filename)
		if _, err := os.Stat(filePath); err == nil {
			content, err := os.ReadFile(filePath)
			if err != nil {
				log.G(ctx).Warnf("Failed to read %s: %v", filename, err)
				continue
			}
			archiveData[filename] = string(content)
		}
	}
	
	if len(archiveData) == 0 {
		log.G(ctx).Debug("No output files found to archive")
		return nil
	}
	
	// Create archive
	archivePath := filepath.Join(spoolDir, fmt.Sprintf("output-%s.tar.gz", containerName))
	if err := h.createTarGz(ctx, archivePath, archiveData); err != nil {
		return fmt.Errorf("failed to create output archive: %w", err)
	}
	
	log.G(ctx).Infof("Job output archived to %s", archivePath)
	return nil
}
