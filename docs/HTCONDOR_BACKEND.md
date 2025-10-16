# HTCondor Backend for InterLink

This document describes the HTCondor batch system backend implementation for the InterLink SLURM plugin.

## Overview

The HTCondor backend enables InterLink to submit Kubernetes pods as HTCondor jobs, using Singularity/Apptainer as the container runtime. This implementation is designed for scenarios where:

- HTCondor is used as the batch scheduler
- No shared filesystem exists between the submit node and execute nodes
- Singularity/Apptainer is available on execute nodes for running containers

## Architecture

### Key Components

1. **htcondor.go** - Main BatchSystem interface implementation
   - Pod submission and job management
   - Integration with HTCondor CLI tools (condor_submit, condor_q, condor_rm, condor_history)

2. **submit.go** - Submit file generation and Singularity command building
   - Generates HTCondor submit files (.sub)
   - Creates wrapper scripts for job execution
   - Handles volume mounting via Singularity bind mounts

3. **status.go** - Job status monitoring
   - Queries job status from condor_q and condor_history
   - Translates HTCondor states to Kubernetes pod phases
   - Builds container status information

4. **spool.go** - Spool directory management
   - Stages ConfigMaps and Secrets as tar.gz archives
   - Manages file transfer to/from execute nodes
   - Cleanup of spool directories

5. **helpers.go** - Utility functions
   - Command execution
   - Configuration validation
   - Resource formatting

6. **types.go** - Data structures and configuration
   - HTCondorConfig structure
   - Job state enumeration
   - Job information tracking

## No Shared Filesystem Design

The implementation assumes **no shared filesystem** between the submit node (where the plugin runs) and execute nodes (where containers execute). This differs from traditional HPC setups and has important implications:

### File Transfer Mechanism

HTCondor's built-in file transfer is used to move data:

1. **ConfigMaps and Secrets**:
   - Staged as tar.gz archives in the spool directory on the submit node
   - Listed in `transfer_input_files` in the submit file
   - Transferred to execute node's `_condor_scratch_dir`
   - Extracted by wrapper script before container execution

2. **Job Output**:
   - Stdout/stderr captured to files on execute node
   - Transferred back to spool directory via `transfer_output_files`
   - Available for retrieval after job completion

### Volume Support

| Volume Type | Support | Implementation |
|-------------|---------|----------------|
| HostPath | ✅ Yes | Direct Singularity bind mount (path must exist on execute node) |
| ConfigMap | ✅ Yes | Transferred as tar.gz, extracted on execute node |
| Secret | ✅ Yes | Transferred as tar.gz, extracted on execute node |
| EmptyDir | ✅ Yes | Created locally in `_condor_scratch_dir` |
| PersistentVolumeClaim | ❌ No | Requires shared storage |
| NFS | ❌ No | Not applicable without shared filesystem |
| CSI | ❌ No | Not applicable |

### Log Streaming

**Real-time log streaming is disabled** because:
- No shared filesystem to tail logs from
- Logs only available after job completes and files are transferred back
- `EnableLogStreaming` is always set to `false`

Users can retrieve logs after job completion via the `/getLogs` endpoint.

## Configuration

Example configuration file (`examples/HTCondorConfig.yaml`):

```yaml
BackendType: htcondor

HTCondor:
  CondorSubmitPath: "/usr/bin/condor_submit"
  CondorQPath: "/usr/bin/condor_q"
  CondorRmPath: "/usr/bin/condor_rm"
  CondorHistoryPath: "/usr/bin/condor_history"
  SingularityPath: "/usr/bin/singularity"
  SingularityOptions:
    - "--cleanenv"
    - "--containall"
  SpoolDirectory: "/var/interlink/htcondor/spool"
  DataRootFolder: "/var/interlink/htcondor"
  Requirements: ""
  VerboseLogging: false
  ErrorsOnlyLogging: false
```

## Job Submission Flow

1. **Pod Received**: Plugin receives pod creation request
2. **Spool Directory Created**: Directory created for pod UID in spool directory
3. **ConfigMaps/Secrets Staged**: Data archived as tar.gz files
4. **Init Containers**: Submitted sequentially, each waits for previous to complete
5. **Regular Containers**: Submitted in parallel after init containers finish
6. **File Transfer**: HTCondor transfers input files to execute node
7. **Container Execution**: Wrapper script extracts files and runs Singularity
8. **Output Transfer**: HTCondor transfers output files back to submit node

## Job States

HTCondor job states are mapped to Kubernetes pod phases:

| HTCondor State | Value | Kubernetes Phase | Description |
|----------------|-------|------------------|-------------|
| Idle | 1 | Pending | Job waiting in queue |
| Running | 2 | Running | Job executing |
| Removed | 3 | Failed | Job removed/killed |
| Completed | 4 | Succeeded/Failed | Job finished (check exit code) |
| Held | 5 | Pending | Job on hold |
| Suspended | 7 | Pending | Job suspended |

## Resource Limits

Container resource limits are translated to HTCondor requests:

```yaml
resources:
  limits:
    cpu: "4"         # → request_cpus = 4
    memory: "8Gi"    # → request_memory = 8192 MB
```

## Custom ClassAds

Use pod annotations to add custom HTCondor ClassAd attributes:

```yaml
metadata:
  annotations:
    htcondor.interlink.io/classads: |
      +ProjectName = "MyProject"
      +Group = "physics"
      request_gpus = 1
```

## Container Images

The backend supports:

1. **Singularity SIF files**:
   ```yaml
   image: /cvmfs/unpacked.cern.ch/registry.hub.docker.com/library/ubuntu:20.04
   ```

2. **Docker images** (pulled by Singularity):
   ```yaml
   image: docker://ubuntu:20.04
   ```

Images must be accessible on execute nodes.

## Environment Variables

Container environment variables are:
- Passed to Singularity via `--env` flags
- Exported in the wrapper script before container execution

## Wrapper Script

Each container gets a wrapper script that:
1. Extracts ConfigMap and Secret tar.gz archives
2. Creates EmptyDir directories
3. Sets environment variables
4. Executes the Singularity container
5. Captures exit code

## Error Handling

- **Submit failures**: Job ID not returned from condor_submit
- **Status query failures**: Job not found in queue or history
- **File transfer failures**: Detected via HTCondor job status
- **Container failures**: Exit code captured and reported

## Limitations

1. **No real-time log streaming** - logs only available after completion
2. **No shared storage volumes** - only HostPath, ConfigMap, Secret, EmptyDir
3. **Container images** - must be accessible on execute nodes
4. **HostPath volumes** - paths must exist on execute nodes
5. **Init container dependencies** - strictly sequential execution

## Troubleshooting

### Jobs stuck in Idle state
- Check HTCondor pool resources: `condor_status`
- Check job requirements: `condor_q -better-analyze <job_id>`
- Review ClassAds in submit file

### File transfer failures
- Check spool directory permissions
- Verify ConfigMap/Secret data is staged correctly
- Review HTCondor file transfer logs

### Container execution failures
- Check Singularity is installed on execute nodes
- Verify container image is accessible
- Review job stderr file in spool directory

### Logs not available
- Ensure job has completed (`condor_q` shows no job)
- Check output files transferred back to spool directory
- Verify file transfer completed successfully

## Development

To extend the HTCondor backend:

1. **Add new volume types**: Modify `buildVolumeBindMount()` in submit.go
2. **Custom job submission**: Extend `generateSubmitFile()` in submit.go
3. **Additional monitoring**: Enhance `queryJobStatus()` in status.go
4. **New ClassAd attributes**: Update submit file generation

## References

- [HTCondor Documentation](https://htcondor.readthedocs.io/)
- [HTCondor Submit File](https://htcondor.readthedocs.io/en/latest/users-manual/submitting-a-job.html)
- [Singularity Documentation](https://sylabs.io/docs/)
- [HTCondor File Transfer](https://htcondor.readthedocs.io/en/latest/users-manual/file-transfer.html)
