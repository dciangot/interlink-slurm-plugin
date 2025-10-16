# Quick Start Guide

Get started with the InterLink pluggable backend system in minutes.

## Choose Your Backend

### Option 1: Docker Backend (Single Node)

Perfect for development, testing, or edge deployments.

**1. Create configuration file:**

```bash
cat > /tmp/docker-config.yaml <<EOF
BackendType: docker

Docker:
  Endpoint: "unix:///var/run/docker.sock"
  DataRootFolder: "/tmp/interlink-docker"
  VerboseLogging: true
  ExportPodData: true
EOF
```

**2. Set environment variable:**

```bash
export INTERLINK_CONFIG_PATH=/tmp/docker-config.yaml
```

**3. Start the sidecar:**

```bash
./bin/slurm-sd
```

**4. Verify it's running:**

```bash
curl http://localhost:4000/system-info
```

Expected output:
```
Docker Info:
  Containers: 0 (Running: 0, Paused: 0, Stopped: 0)
  Images: 5
  Server Version: 24.0.7
  ...
```

### Option 2: SLURM Backend (HPC Clusters)

Use with existing SLURM HPC infrastructure.

**1. Use existing configuration:**

Your existing `SlurmConfig.yaml` works without changes!

```bash
export SLURMCONFIGPATH=/etc/interlink/SlurmConfig.yaml
```

**2. Or use unified format:**

```bash
cat > /tmp/slurm-config.yaml <<EOF
BackendType: slurm

SLURM:
  SbatchPath: "sbatch"
  ScancelPath: "scancel"
  SqueuePath: "squeue"
  SinfoPath: "sinfo"
  DataRootFolder: "/var/interlink/slurm"
  ContainerRuntime: "singularity"
  VerboseLogging: false
EOF

export INTERLINK_CONFIG_PATH=/tmp/slurm-config.yaml
```

**3. Start the sidecar:**

```bash
./bin/slurm-sd
```

## Test Pod Execution

### Create a Test Pod

```bash
cat > test-pod.json <<EOF
{
  "pod": {
    "metadata": {
      "uid": "test-$(date +%s)",
      "name": "hello-world",
      "namespace": "default"
    },
    "spec": {
      "containers": [{
        "name": "hello",
        "image": "busybox:latest",
        "command": ["echo"],
        "args": ["Hello from InterLink!"]
      }]
    }
  }
}
EOF
```

### Submit the Pod

```bash
curl -X POST http://localhost:4000/create \
  -H "Content-Type: application/json" \
  -d @test-pod.json
```

Response:
```json
{
  "PodUID": "test-1234567890",
  "PodJID": "container-id-or-slurm-job-id"
}
```

### Check Pod Status

```bash
# Replace with your pod UID from above
POD_UID="test-1234567890"

curl -X GET http://localhost:4000/status \
  -H "Content-Type: application/json" \
  -d "[{\"metadata\": {\"uid\": \"$POD_UID\"}}]"
```

Response:
```json
[{
  "PodName": "hello-world",
  "PodUID": "test-1234567890",
  "PodNamespace": "default",
  "Containers": [{
    "Name": "hello",
    "State": {
      "Running": {
        "StartedAt": "2025-01-15T10:30:00Z"
      }
    },
    "Ready": true
  }]
}]
```

### Get Pod Logs

```bash
curl -X GET http://localhost:4000/getLogs \
  -H "Content-Type: application/json" \
  -d "{
    \"PodUID\": \"$POD_UID\",
    \"ContainerName\": \"hello\",
    \"Opts\": {\"Follow\": false}
  }"
```

Output:
```
Hello from InterLink!
```

### Delete the Pod

```bash
curl -X DELETE http://localhost:4000/delete \
  -H "Content-Type: application/json" \
  -d "{\"metadata\": {\"uid\": \"$POD_UID\"}}"
```

## Docker Backend Examples

### Multi-Container Pod

```json
{
  "pod": {
    "metadata": {
      "uid": "multi-container-test",
      "name": "nginx-with-sidecar"
    },
    "spec": {
      "containers": [
        {
          "name": "nginx",
          "image": "nginx:latest"
        },
        {
          "name": "log-watcher",
          "image": "busybox:latest",
          "command": ["tail"],
          "args": ["-f", "/var/log/nginx/access.log"]
        }
      ]
    }
  }
}
```

### Pod with Init Container

```json
{
  "pod": {
    "metadata": {
      "uid": "init-container-test",
      "name": "app-with-init"
    },
    "spec": {
      "initContainers": [{
        "name": "setup",
        "image": "busybox:latest",
        "command": ["sh", "-c", "echo 'Setup complete' > /data/setup.txt"]
      }],
      "containers": [{
        "name": "app",
        "image": "nginx:latest"
      }]
    }
  }
}
```

### Pod with Volumes

```json
{
  "pod": {
    "metadata": {
      "uid": "volume-test",
      "name": "app-with-volumes"
    },
    "spec": {
      "containers": [{
        "name": "app",
        "image": "nginx:latest",
        "volumeMounts": [
          {
            "name": "data",
            "mountPath": "/data"
          },
          {
            "name": "config",
            "mountPath": "/etc/app"
          }
        ]
      }],
      "volumes": [
        {
          "name": "data",
          "hostPath": {"path": "/tmp/data"}
        },
        {
          "name": "config",
          "emptyDir": {}
        }
      ]
    }
  }
}
```

### Pod with Custom Job Script

```json
{
  "pod": {
    "metadata": {
      "uid": "custom-script-test",
      "name": "custom-job"
    },
    "spec": {
      "containers": []
    }
  },
  "jobScript": "#!/bin/bash\necho 'Running custom script'\ndate\nhostname\n"
}
```

## Monitoring and Debugging

### Check System Health

```bash
curl http://localhost:4000/system-info
```

### Docker Backend: View Running Containers

```bash
docker ps --filter "label=interlink.pod.namespace"
```

### Docker Backend: Check Container Logs

```bash
# Find container ID
docker ps -a | grep interlink

# View logs
docker logs <container-id>
```

### Docker Backend: Inspect Metadata

```bash
# List all tracked jobs
ls -la /tmp/interlink-docker/.metadata/

# View job metadata
cat /tmp/interlink-docker/.metadata/<pod-uid>.json
```

### SLURM Backend: Check Job Queue

```bash
squeue --me
```

### SLURM Backend: View Job Output

```bash
ls /var/interlink/slurm/<namespace>-<pod-uid>/
cat /var/interlink/slurm/<namespace>-<pod-uid>/*.out
```

## Troubleshooting

### Docker: Permission Denied

**Error**: `failed to create Docker client: permission denied`

**Solution**:
```bash
# Add user to docker group
sudo usermod -aG docker $USER
newgrp docker

# Or run with sudo (not recommended for production)
sudo ./bin/slurm-sd
```

### Docker: Cannot Connect to Docker Daemon

**Error**: `error during connect: Get "http://%2Fvar%2Frun%2Fdocker.sock/...": dial unix`

**Solution**:
```bash
# Check Docker is running
sudo systemctl status docker

# Start Docker if needed
sudo systemctl start docker
```

### SLURM: Command Not Found

**Error**: `sbatch: command not found`

**Solution**:
```bash
# Add SLURM bin directory to PATH
export PATH=$PATH:/usr/local/bin:/opt/slurm/bin

# Or specify full paths in config
SbatchPath: "/usr/local/bin/sbatch"
```

### Port Already in Use

**Error**: `bind: address already in use`

**Solution**:
```bash
# Find process using port 4000
sudo lsof -i :4000

# Kill the process or use a different port
# In config file:
SLURM:
  SidecarPort: "4001"  # Use different port
```

## Next Steps

- **Production Deployment**: See [BACKEND_ARCHITECTURE.md](./BACKEND_ARCHITECTURE.md)
- **Docker Backend Details**: See [DOCKER_BACKEND.md](./DOCKER_BACKEND.md)
- **Adding New Backends**: See architecture documentation
- **Configuration Reference**: See example configs in `examples/`

## Getting Help

- **GitHub Issues**: https://github.com/interlink-hq/interlink-slurm-plugin/issues
- **Documentation**: Check `docs/` directory
- **Examples**: See `examples/` directory

## Environment Variables Reference

| Variable | Description | Default |
|----------|-------------|---------|
| `INTERLINK_CONFIG_PATH` | Path to config file | `/etc/interlink/InterLinkConfig.yaml` |
| `SLURMCONFIGPATH` | Legacy SLURM config path | - |
| `ENABLE_TRACING` | Enable OpenTelemetry tracing | `0` |
| `TELEMETRY_ENDPOINT` | OTLP endpoint | `localhost:4317` |
| `TELEMETRY_UNIQUE_ID` | Unique telemetry ID | Auto-generated |
