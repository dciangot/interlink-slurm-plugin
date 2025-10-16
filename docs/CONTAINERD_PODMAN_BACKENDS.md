# Containerd and Podman Backends

This guide covers the Containerd and Podman backends for InterLink, providing alternatives to Docker for container execution.

## Overview

InterLink now supports four container runtime backends:

| Backend | Use Case | Daemon | Root Required | Pod Support |
|---------|----------|--------|---------------|-------------|
| **Docker** | General purpose | Yes | Yes | No |
| **Containerd** | Low-level, K8s-native | Yes | Yes | No |
| **Podman** | Daemonless, rootless | No | No | Yes |
| **SLURM** | HPC batch systems | N/A | Varies | N/A |

## Containerd Backend

### What is Containerd?

Containerd is an industry-standard container runtime that emphasizes simplicity, robustness, and portability. It's the same runtime used by Kubernetes and Docker.

### Key Features

✅ **OCI-compliant** - Full OCI runtime and image spec support  
✅ **Lightweight** - Minimal overhead, efficient resource usage  
✅ **K8s-native** - Same runtime used by Kubernetes  
✅ **Pluggable** - Supports multiple runtimes (runc, Kata, gVisor)  
✅ **Production-ready** - Battle-tested in Kubernetes deployments  

### When to Use Containerd

- **Kubernetes environments** - Already have Containerd installed
- **Custom platforms** - Building container platforms from scratch
- **Performance** - Need low-level control and minimal overhead
- **Security** - Want to use Kata Containers or gVisor
- **Simplicity** - Prefer minimal, focused runtime

### Configuration

```yaml
BackendType: containerd

Containerd:
  Socket: "/run/containerd/containerd.sock"
  Namespace: "interlink"
  DataRootFolder: "/var/interlink/containerd"
  ImagePrefix: "docker.io/"
  Snapshotter: "overlayfs"
  Runtime: "io.containerd.runc.v2"
  VerboseLogging: false
  ExportPodData: true
  EnableProbes: false
```

### Setup

**1. Install Containerd:**

```bash
# Ubuntu/Debian
sudo apt-get install containerd

# RHEL/CentOS
sudo yum install containerd

# Or install from source
wget https://github.com/containerd/containerd/releases/download/v1.7.0/containerd-1.7.0-linux-amd64.tar.gz
sudo tar Cxzvf /usr/local containerd-1.7.0-linux-amd64.tar.gz
```

**2. Start Containerd:**

```bash
sudo systemctl enable --now containerd
```

**3. Verify:**

```bash
sudo ctr version
```

**4. Configure InterLink:**

```bash
export INTERLINK_CONFIG_PATH=/path/to/ContainerdConfig.yaml
./bin/slurm-sd
```

### Runtime Options

Containerd supports multiple runtimes:

**runc (default):**
```yaml
Runtime: "io.containerd.runc.v2"
```

**Kata Containers (VM-based isolation):**
```yaml
Runtime: "io.containerd.kata.v2"
```

**gVisor (user-space kernel):**
```yaml
Runtime: "runsc"
```

### Snapshotter Options

Control filesystem layer management:

```yaml
# Default - overlayfs (recommended)
Snapshotter: "overlayfs"

# Native - direct filesystem operations
Snapshotter: "native"

# btrfs - requires btrfs filesystem
Snapshotter: "btrfs"

# zfs - requires ZFS filesystem
Snapshotter: "zfs"
```

### Namespaces

Containerd uses namespaces for isolation:

```yaml
# Default namespace
Namespace: "interlink"

# Use custom namespace
Namespace: "production"

# Kubernetes namespace
Namespace: "k8s.io"
```

Containers are isolated per namespace. List containers in namespace:

```bash
sudo ctr -n interlink containers list
```

### Advanced Features

**Custom Runtime Configuration:**

Create `/etc/containerd/config.toml`:

```toml
version = 2

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
    SystemdCgroup = true
```

**Image Pull with Authentication:**

```bash
# Login to registry
sudo ctr -n interlink images pull --user username:password docker.io/library/nginx:latest
```

### Troubleshooting

**Socket permission denied:**
```bash
# Add user to root group or use sudo
sudo usermod -aG root $USER
# Or run sidecar with sudo
sudo ./bin/slurm-sd
```

**Namespace not found:**
```bash
# List namespaces
sudo ctr namespaces list

# Create namespace
sudo ctr namespaces create interlink
```

**Image pull fails:**
```bash
# Verify connectivity
sudo ctr -n interlink images pull docker.io/library/alpine:latest

# Check Containerd logs
sudo journalctl -u containerd -f
```

---

## Podman Backend

### What is Podman?

Podman is a daemonless container engine for developing, managing, and running OCI containers. It's designed as a drop-in replacement for Docker with enhanced security.

### Key Features

✅ **Daemonless** - No background service required  
✅ **Rootless** - Run containers without root privileges  
✅ **Pod support** - Native Kubernetes pod concept  
✅ **Docker-compatible** - Same CLI and API  
✅ **Systemd integration** - Generate systemd units  
✅ **Secure by default** - No daemon attack surface  

### When to Use Podman

- **Rootless containers** - Security-first environments
- **No daemon** - Simpler deployment model
- **Pod semantics** - Need Kubernetes-style pods
- **Development** - Local development without Docker Desktop
- **Edge computing** - Resource-constrained environments
- **Security** - Minimize attack surface

### Configuration

```yaml
BackendType: podman

Podman:
  Endpoint: "unix:///run/podman/podman.sock"
  DataRootFolder: "/var/interlink/podman"
  ImagePrefix: "docker.io/"
  UsePods: true
  NetworkMode: "bridge"
  VerboseLogging: false
  ExportPodData: true
```

### Setup

**1. Install Podman:**

```bash
# Ubuntu 20.10+
sudo apt-get install podman

# RHEL/CentOS 8+
sudo yum install podman

# Fedora
sudo dnf install podman
```

**2. Enable Podman API service:**

**Rootful (system-wide):**
```bash
sudo systemctl enable --now podman.socket
```

**Rootless (user-specific):**
```bash
systemctl --user enable --now podman.socket
```

**3. Verify:**

```bash
# Check socket
ls -la /run/podman/podman.sock

# Test API
curl --unix-socket /run/podman/podman.sock http://d/v3.0.0/libpod/_ping
```

**4. Configure InterLink:**

```bash
export INTERLINK_CONFIG_PATH=/path/to/PodmanConfig.yaml
./bin/slurm-sd
```

### Rootless Mode

Podman's killer feature - run containers without root:

**Setup rootless:**

```bash
# Enable user namespaces
echo "user.max_user_namespaces=28633" | sudo tee -a /etc/sysctl.d/userns.conf
sudo sysctl -p /etc/sysctl.d/userns.conf

# Start rootless socket
systemctl --user enable --now podman.socket

# Verify
podman --remote info
```

**Configure for rootless:**

```yaml
Podman:
  Endpoint: "unix:///run/user/$(id -u)/podman/podman.sock"
```

Or use environment variable:
```bash
export XDG_RUNTIME_DIR=/run/user/$(id -u)
# InterLink auto-detects rootless socket
```

### Pod Support

Podman has native pod support (like Kubernetes):

```yaml
# Enable pods (recommended)
UsePods: true
```

**With UsePods=true:**
- Each Kubernetes pod becomes a Podman pod
- Containers share network namespace
- Grouped lifecycle management
- True pod semantics

**With UsePods=false:**
- Containers run independently
- Each container has own network
- Simpler but less Kubernetes-like

**Managing Podman pods:**

```bash
# List pods
podman pod list

# Inspect pod
podman pod inspect <pod-id>

# Remove pod (removes all containers)
podman pod rm <pod-id>
```

### Network Modes

Configure networking when UsePods=false:

```yaml
# Bridge network (default)
NetworkMode: "bridge"

# Host network
NetworkMode: "host"

# No network
NetworkMode: "none"

# Custom network
NetworkMode: "my-network"
```

Create custom network:
```bash
podman network create my-network
```

### Remote Podman

Run Podman on remote host:

**1. Start Podman service on remote:**
```bash
# On remote host
podman system service --time=0 tcp:0.0.0.0:8080
```

**2. Configure InterLink:**
```yaml
Podman:
  Endpoint: "http://remote-host:8080"
```

**Security warning:** Use SSH tunnel or VPN for production:
```bash
# SSH tunnel
ssh -L 8080:localhost:8080 remote-host
```

### Podman vs Docker

| Feature | Podman | Docker |
|---------|--------|--------|
| Daemon | No | Yes |
| Root required | No | Yes |
| Pods | Yes | No |
| Docker Compose | Yes (podman-compose) | Yes |
| Kubernetes YAML | Yes | No |
| Systemd integration | Native | Via daemon |
| Security | Rootless | Daemon runs as root |

### Migration from Docker

Podman is Docker-compatible:

```bash
# Most Docker commands work
alias docker=podman

# Docker images work directly
podman pull docker.io/nginx
podman run nginx
```

### Advanced Features

**Generate Kubernetes YAML:**
```bash
podman generate kube <pod-name> > pod.yaml
```

**Generate systemd units:**
```bash
podman generate systemd --name <container-name>
```

**Play Kubernetes YAML:**
```bash
podman play kube pod.yaml
```

### Troubleshooting

**Socket not found:**
```bash
# Start socket
sudo systemctl start podman.socket

# Or for rootless
systemctl --user start podman.socket

# Verify
curl --unix-socket /run/podman/podman.sock http://d/_ping
```

**Permission denied (rootless):**
```bash
# Check user namespaces
cat /proc/sys/user/max_user_namespaces
# Should be > 0

# Check subuid/subgid
grep $USER /etc/subuid
grep $USER /etc/subgid
```

**API version mismatch:**
```bash
# Check Podman version
podman version

# Update APIVersion in config to match
APIVersion: "v4.0.0"
```

**Network issues:**
```bash
# Reset network
podman network prune

# Recreate default network
podman network create podman
```

---

## Comparison Matrix

### Performance

| Metric | Docker | Containerd | Podman |
|--------|--------|------------|--------|
| Startup time | Medium | Fast | Fast |
| Memory overhead | Medium | Low | Low |
| Container density | High | Very High | High |
| Image pull speed | Fast | Fast | Fast |

### Security

| Feature | Docker | Containerd | Podman |
|---------|--------|------------|--------|
| Rootless | No | Limited | Yes |
| No daemon | No | No | Yes |
| SELinux | Yes | Yes | Yes |
| AppArmor | Yes | Yes | Yes |
| Seccomp | Yes | Yes | Yes |
| User namespaces | Yes | Yes | Yes |

### Operations

| Operation | Docker | Containerd | Podman |
|-----------|--------|------------|--------|
| Container create | `docker run` | `ctr run` | `podman run` |
| List containers | `docker ps` | `ctr containers list` | `podman ps` |
| View logs | `docker logs` | `ctr tasks logs` | `podman logs` |
| Stop container | `docker stop` | `ctr tasks kill` | `podman stop` |
| Remove container | `docker rm` | `ctr containers rm` | `podman rm` |

---

## Best Practices

### Containerd

1. **Use namespaces** for isolation between workloads
2. **Choose appropriate snapshotter** based on filesystem
3. **Configure resource limits** in OCI spec
4. **Monitor with ctr** commands for debugging
5. **Use systemd** for service management

### Podman

1. **Prefer rootless** for security
2. **Use pods** when running multiple containers
3. **Enable SELinux** for additional security
4. **Generate systemd units** for production
5. **Use podman-compose** for multi-container apps

---

## Getting Help

### Containerd

- **Documentation**: https://containerd.io/docs/
- **GitHub**: https://github.com/containerd/containerd
- **Slack**: #containerd on Cloud Native Slack

### Podman

- **Documentation**: https://docs.podman.io/
- **GitHub**: https://github.com/containers/podman
- **IRC**: #podman on Libera.Chat

### InterLink

- **Documentation**: See `docs/` directory
- **GitHub Issues**: Report bugs and request features
- **Examples**: Check `examples/` for configurations
