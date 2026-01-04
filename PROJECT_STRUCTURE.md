# EROFS Snapshotter - External Snapshotter for containerd

An external EROFS snapshotter that communicates with containerd via gRPC socket.
This allows using EROFS-based container layers without modifying containerd.

## Project Layout

```
nexuserofs/
├── cmd/
│   └── erofs-snapshotter/
│       └── main.go              # Entry point, gRPC server setup
├── pkg/
│   ├── snapshotter/
│   │   ├── snapshotter.go       # Core snapshotter implementation
│   │   ├── snapshotter_linux.go # Linux-specific (mounts, loop devices)
│   │   ├── config.go            # Configuration options
│   │   └── erofs_other.go       # Stubs for non-Linux
│   ├── differ/
│   │   ├── differ.go            # EROFS differ implementation
│   │   ├── compare_linux.go     # Layer comparison (Linux)
│   │   └── compare_other.go     # Stubs for non-Linux
│   ├── erofs/
│   │   ├── mkfs.go              # mkfs.erofs wrapper
│   │   ├── mount.go             # EROFS mount handling
│   │   └── convert.go           # Tar to EROFS conversion
│   ├── loopback/
│   │   ├── loopback.go          # Loop device management
│   │   └── loopback_linux.go    # Linux loop device implementation
│   └── overlay/
│       └── overlay.go           # Overlay mount handling
├── internal/
│   ├── fsverity/                # fsverity support
│   ├── mountutils/              # Mount utilities
│   └── storage/                 # BoltDB metadata storage
├── service/
│   ├── snapshots.go             # gRPC snapshot service wrapper
│   └── diff.go                  # gRPC diff service wrapper
├── go.mod
├── go.sum
├── Makefile
└── config/
    └── config.toml.example      # Example containerd config
```

## Architecture

```
┌─────────────────────┐     gRPC/socket     ┌────────────────────────┐
│     containerd      │◄───────────────────►│  erofs-snapshotter     │
│                     │                     │  (this project)        │
│  - content store    │                     │                        │
│  - images           │◄────client conn─────│  Implements:           │
│  - proxy plugins    │    (for content)    │  - SnapshotsServer     │
└─────────────────────┘                     │  - DiffServer          │
                                            └────────────────────────┘
```

## containerd Configuration

```toml
# /etc/containerd/config.toml
version = 2

[proxy_plugins]
  [proxy_plugins.erofs]
    type = "snapshot"
    address = "/run/erofs-snapshotter/snapshotter.sock"

  [proxy_plugins.erofs-diff]
    type = "diff"
    address = "/run/erofs-snapshotter/snapshotter.sock"

# Use as default snapshotter
[plugins."io.containerd.cri.v1.images"]
  snapshotter = "erofs"
```

## Snapshotter Configuration

```toml
# /etc/erofs-snapshotter/config.toml
version = 1

[snapshotter]
root = "/var/lib/erofs-snapshotter"
address = "/run/erofs-snapshotter/snapshotter.sock"

# Connect to containerd for content store access
containerd_address = "/run/containerd/containerd.sock"
containerd_namespace = "default"

[snapshotter.options]
# Default writable layer size (0 = directory mode, >0 = block mode)
# Block mode uses ext4 loop mounts for writable layers
default_writable_size = "512M"

# Enable fsverity for layer integrity validation
enable_fsverity = false

# Set immutable flag on committed layers
set_immutable = true

# Extra overlay mount options
overlay_options = ["index=off", "metacopy=off"]

# Max layers before triggering fsmerge (0 = disabled)
max_unmerged_layers = 0

[differ]
# Extra mkfs.erofs options
mkfs_options = ["-zlz4hc,12", "-C65536"]

# Enable tar index mode (faster apply, requires erofs-utils 1.8+)
tar_index_mode = false
```

## Building

```bash
make build
```

## Running

```bash
# Start the snapshotter daemon
sudo ./bin/erofs-snapshotter --config /etc/erofs-snapshotter/config.toml

# Or with flags
sudo ./bin/erofs-snapshotter \
  --root /var/lib/erofs-snapshotter \
  --address /run/erofs-snapshotter/snapshotter.sock \
  --containerd-address /run/containerd/containerd.sock
```

## Requirements

- Linux kernel with EROFS support (5.4+)
- erofs-utils (mkfs.erofs)
- containerd 2.0+

## License

Apache 2.0
