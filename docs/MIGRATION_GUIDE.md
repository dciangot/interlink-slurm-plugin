# Migration Guide

This guide helps existing InterLink SLURM Plugin users migrate to the new pluggable backend architecture.

## For Existing SLURM Users

**Good news**: You don't need to change anything! The new version maintains 100% backward compatibility.

### What Stays the Same

✅ Your existing `SlurmConfig.yaml` files work without modification  
✅ All SLURM features are preserved (Singularity, Enroot, probes, etc.)  
✅ HTTP API endpoints are unchanged  
✅ Deployment procedures are identical  
✅ Performance characteristics are the same  

### What's New

✨ **Option to use Docker backend** for development/testing  
✨ **Unified configuration format** (optional migration)  
✨ **Extensible architecture** for future batch systems  

## Zero-Downtime Migration

### Option 1: No Changes Required (Recommended)

Continue using your existing configuration:

```bash
# Your current startup command
export SLURMCONFIGPATH=/etc/interlink/SlurmConfig.yaml
./bin/slurm-sd
```

This will continue to work indefinitely. The system automatically detects legacy configuration format.

### Option 2: Migrate to Unified Format (Optional)

If you want to use the new unified configuration format:

**Before (Legacy Format):**
```yaml
# /etc/interlink/SlurmConfig.yaml
SbatchPath: "sbatch"
ScancelPath: "scancel"
SqueuePath: "squeue"
SinfoPath: "sinfo"
SidecarPort: "4000"
DataRootFolder: "/var/interlink/slurm"
ContainerRuntime: "singularity"
VerboseLogging: false
# ... more SLURM settings
```

**After (Unified Format):**
```yaml
# /etc/interlink/InterLinkConfig.yaml
BackendType: slurm

SLURM:
  SbatchPath: "sbatch"
  ScancelPath: "scancel"
  SqueuePath: "squeue"
  SinfoPath: "sinfo"
  SidecarPort: "4000"
  DataRootFolder: "/var/interlink/slurm"
  ContainerRuntime: "singularity"
  VerboseLogging: false
  # ... same SLURM settings under SLURM: section
```

**Update environment variable:**
```bash
# Old variable (still works)
export SLURMCONFIGPATH=/etc/interlink/SlurmConfig.yaml

# New variable (recommended)
export INTERLINK_CONFIG_PATH=/etc/interlink/InterLinkConfig.yaml
```

**Startup command remains the same:**
```bash
./bin/slurm-sd
```

## Migration Steps (If Updating Config Format)

### Step 1: Backup Current Configuration

```bash
cp /etc/interlink/SlurmConfig.yaml /etc/interlink/SlurmConfig.yaml.backup
```

### Step 2: Create Unified Configuration

```bash
cat > /etc/interlink/InterLinkConfig.yaml <<EOF
BackendType: slurm

SLURM:
$(cat /etc/interlink/SlurmConfig.yaml | sed 's/^/  /')
EOF
```

This script:
1. Sets `BackendType: slurm`
2. Indents your existing config under `SLURM:` section

### Step 3: Update Environment Variable

```bash
# In your systemd service file or startup script
# Change from:
Environment="SLURMCONFIGPATH=/etc/interlink/SlurmConfig.yaml"

# To:
Environment="INTERLINK_CONFIG_PATH=/etc/interlink/InterLinkConfig.yaml"
```

### Step 4: Reload and Restart

```bash
# If using systemd
sudo systemctl daemon-reload
sudo systemctl restart interlink-sidecar

# Or just restart the service
./bin/slurm-sd
```

### Step 5: Verify

```bash
# Check logs for successful startup
journalctl -u interlink-sidecar -f

# Or check logs directly
# Should see: "Backend type: slurm"
# Should see: "Initializing SLURM backend"

# Test the API
curl http://localhost:4000/system-info
```

## Systemd Service Update

### Current Service File

```ini
[Unit]
Description=InterLink SLURM Sidecar
After=network.target

[Service]
Type=simple
User=interlink
Environment="SLURMCONFIGPATH=/etc/interlink/SlurmConfig.yaml"
ExecStart=/usr/local/bin/slurm-sd
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

### Updated Service File (Optional)

```ini
[Unit]
Description=InterLink Sidecar
After=network.target

[Service]
Type=simple
User=interlink
Environment="INTERLINK_CONFIG_PATH=/etc/interlink/InterLinkConfig.yaml"
ExecStart=/usr/local/bin/slurm-sd
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

## Docker Compose Update

### Current docker-compose.yml

```yaml
version: '3'
services:
  interlink-sidecar:
    image: interlink-slurm-plugin:latest
    environment:
      - SLURMCONFIGPATH=/etc/interlink/SlurmConfig.yaml
    volumes:
      - /etc/interlink:/etc/interlink:ro
```

### Updated docker-compose.yml

```yaml
version: '3'
services:
  interlink-sidecar:
    image: interlink-slurm-plugin:latest
    environment:
      - INTERLINK_CONFIG_PATH=/etc/interlink/InterLinkConfig.yaml
    volumes:
      - /etc/interlink:/etc/interlink:ro
```

## Testing Your Migration

### 1. Check Configuration Loading

```bash
# Start with verbose logging
INTERLINK_CONFIG_PATH=/etc/interlink/InterLinkConfig.yaml \
  ./bin/slurm-sd 2>&1 | grep -i backend
```

Expected output:
```
INFO Backend type: slurm
INFO Initializing SLURM backend
```

### 2. Submit a Test Job

Use your existing test pods - they should work identically:

```bash
# Your existing pod submission should work
curl -X POST http://localhost:4000/create \
  -H "Content-Type: application/json" \
  -d @your-test-pod.json
```

### 3. Verify SLURM Integration

```bash
# Check that jobs appear in SLURM queue
squeue --me

# Verify job files are created in expected location
ls /var/interlink/slurm/
```

## Rollback Procedure

If you need to rollback:

### Step 1: Restore Old Configuration

```bash
cp /etc/interlink/SlurmConfig.yaml.backup /etc/interlink/SlurmConfig.yaml
```

### Step 2: Revert Environment Variable

```bash
# Change back to:
export SLURMCONFIGPATH=/etc/interlink/SlurmConfig.yaml
```

### Step 3: Restart Service

```bash
sudo systemctl restart interlink-sidecar
# or
./bin/slurm-sd
```

## Common Migration Issues

### Issue: "Unknown backend type"

**Symptom:**
```
FATAL Unknown backend type:  (must be 'slurm' or 'docker')
```

**Cause**: `BackendType` field is missing or empty

**Solution**: Add `BackendType: slurm` at the top of your unified config:
```yaml
BackendType: slurm

SLURM:
  # ... your config
```

### Issue: "SLURM backend selected but no SLURM configuration provided"

**Symptom:**
```
FATAL SLURM backend selected but no SLURM configuration provided
```

**Cause**: SLURM settings are not under `SLURM:` section

**Solution**: Ensure all SLURM settings are indented under `SLURM:`:
```yaml
BackendType: slurm

SLURM:
  SbatchPath: "sbatch"  # ← Must be indented
  ScancelPath: "scancel"
  # ... etc
```

### Issue: Configuration not found

**Symptom:**
```
failed to read config file /etc/interlink/InterLinkConfig.yaml: no such file or directory
```

**Solution**: Either:
1. Create the file at the specified path, or
2. Set `INTERLINK_CONFIG_PATH` to your actual config location

```bash
export INTERLINK_CONFIG_PATH=/path/to/your/config.yaml
```

## Kubernetes ConfigMap Migration

If you deploy configuration via Kubernetes ConfigMap:

### Before

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: slurm-config
data:
  SlurmConfig.yaml: |
    SbatchPath: "sbatch"
    ScancelPath: "scancel"
    # ... more settings
```

### After

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: interlink-config
data:
  InterLinkConfig.yaml: |
    BackendType: slurm
    
    SLURM:
      SbatchPath: "sbatch"
      ScancelPath: "scancel"
      # ... more settings
```

Update your pod spec:
```yaml
# Before
volumes:
  - name: config
    configMap:
      name: slurm-config

# After
volumes:
  - name: config
    configMap:
      name: interlink-config
```

## FAQ

**Q: Do I have to migrate?**  
A: No, existing configurations work indefinitely.

**Q: Will old configs be deprecated?**  
A: No current plans to deprecate legacy SLURM config format.

**Q: Can I switch between backends?**  
A: Yes, change `BackendType` in your config and restart the service.

**Q: Does this affect my existing jobs?**  
A: No, running jobs are unaffected. Changes apply to new job submissions.

**Q: Is there a performance difference?**  
A: No, SLURM backend performance is identical to the previous version.

**Q: Can I use both backends simultaneously?**  
A: Not currently. You must choose one backend at startup.

## Getting Help

If you encounter issues during migration:

1. Check the logs for error messages
2. Verify your configuration syntax with `yamllint`
3. Review the [Quick Start Guide](./QUICK_START.md)
4. Open an issue on GitHub with your configuration (redact sensitive info)

## Next Steps After Migration

Once migrated to the unified format, you can:

- **Try Docker backend** for local testing (just change `BackendType: docker`)
- **Share configs** more easily with unified format
- **Prepare for future backends** (PBS, Torque, etc.)
- **Simplify deployment** with consistent configuration structure
