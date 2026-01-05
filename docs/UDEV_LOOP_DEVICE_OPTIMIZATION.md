# Udev Loop Device Optimization

## Problem

When nexuserofs mounts container images, it creates loop devices for each EROFS filesystem layer. A typical container image with 5 layers results in 5 loop devices.

This triggers CPU spikes because udev workers attempt SCSI ID detection on each loop device, spawning multiple processes per device.

### Symptoms

```
Jan 05 00:40:05 mini-pc kernel: loop0: detected capacity change from 0 to 48
Jan 05 00:40:05 mini-pc kernel: erofs (device loop0): mounted with root inode @ nid 36.
Jan 05 00:40:05 mini-pc 55-scsi-sg3_id.rules[1866808]: WARNING: SCSI device loop2 has no device ID...
Jan 05 00:40:05 mini-pc 55-scsi-sg3_id.rules[1866806]: WARNING: SCSI device loop0 has no device ID...
Jan 05 00:40:05 mini-pc 55-scsi-sg3_id.rules[1866807]: WARNING: SCSI device loop1 has no device ID...
```

The warnings repeat multiple times per device as udev retries the detection.

## Root Cause

The `55-scsi-sg3_id.rules` udev rule (from the `sg3-utils` package) runs SCSI ID detection on block devices to populate device identification attributes. This is useful for real SCSI/SAS/SATA devices but pointless for loop devices which:

1. Don't have SCSI device IDs
2. Are virtual devices backed by files
3. Are created/destroyed frequently during container operations

The detection process:
1. Loop device creation triggers udev `add` event
2. `55-scsi-sg3_id.rules` matches the block device
3. Multiple `sg_inq` processes spawn to query SCSI attributes
4. All queries fail (loop devices aren't SCSI)
5. CPU cycles wasted, warnings logged

## Solution

nexuserofs sets a unique serial number on each loop device via sysfs (`/sys/block/loopN/loop/serial`). All serials use the `erofs-` prefix, allowing udev rules to identify and handle these devices specially.

### Installation

```bash
sudo tee /etc/udev/rules.d/50-nexuserofs-loop.rules << 'EOF'
# nexuserofs EROFS loop device optimization
# Matches loop devices with serial numbers set by nexuserofs (erofs-* prefix)

# Match EROFS loop devices by serial and set device type
KERNEL=="loop*", ENV{ID_SERIAL}=="erofs-*", ENV{ID_TYPE}="erofs"

# Suppress SCSI ID warnings for EROFS loop devices
KERNEL=="loop*", ENV{ID_SERIAL}=="erofs-*", OPTIONS+="nowatch"

# Skip SCSI ID detection for EROFS loop devices
KERNEL=="loop*", ENV{ID_SERIAL}=="erofs-*", ENV{ID_SCSI}="0", ENV{ID_SCSI_INQUIRY}="0"
EOF

sudo udevadm control --reload-rules
```

### How It Works

nexuserofs assigns a serial number to each loop device when mounting EROFS layers:
- Format: `erofs-<snapshot-id>` (e.g., `erofs-42`, `erofs-sha256-abc123`)
- Serial is written to `/sys/block/loopN/loop/serial` (requires kernel 5.17+)
- udev reads this as `ID_SERIAL` environment variable

The udev rules then:

| Rule | Purpose |
|------|---------|
| `ENV{ID_TYPE}="erofs"` | Tags the device type for identification |
| `OPTIONS+="nowatch"` | Disables inotify watching, reducing overhead |
| `ENV{ID_SCSI}="0"` | Marks device as non-SCSI |
| `ENV{ID_SCSI_INQUIRY}="0"` | Disables SCSI inquiry commands |

### Rule Syntax Explanation

```
KERNEL=="loop*", ENV{ID_SERIAL}=="erofs-*", ENV{ID_SCSI}="0"
       ^                ^                          ^
       |                |                          |
  Match loop      Match serial               Set ID_SCSI
  devices         prefix "erofs-"            to "0"
```

- `==` is a match operator (condition)
- `=` is an assignment operator (action)
- `erofs-*` uses glob pattern matching

### Verification

After applying the rules:

```bash
# Check serial is set on a loop device
cat /sys/block/loop0/loop/serial

# Verify udev sees the serial
udevadm info --query=property --name=/dev/loop0 | grep -E 'ID_SERIAL|ID_TYPE'

# Monitor udev events during container operations
udevadm monitor --property --subsystem-match=block
```

Expected output for nexuserofs loop devices:
```
ID_SERIAL=erofs-42
ID_TYPE=erofs
```

## Fallback: Skip All Loop Device SCSI Detection

If you want to skip SCSI detection for ALL loop devices (not just nexuserofs):

```bash
sudo tee /etc/udev/rules.d/50-skip-loop-scsi.rules << 'EOF'
# Skip SCSI ID detection for all loop devices
SUBSYSTEM=="block", KERNEL=="loop*", ENV{ID_SCSI}="0", ENV{ID_SCSI_INQUIRY}="0"
EOF

sudo udevadm control --reload-rules
```

This is less targeted but effective if you don't use loop devices for real SCSI passthrough.

## Performance Impact

Without the fix:
- 5-10 `sg_inq` processes per loop device
- Multiple retry attempts
- Noticeable CPU spike during container startup
- Log spam with warnings

With the fix:
- Zero `sg_inq` processes for nexuserofs loop devices
- No CPU overhead from SCSI detection
- Clean logs
- Fast container startup

## Requirements

- Linux kernel 5.17+ (for loop device serial number support via sysfs)
- nexuserofs snapshotter (automatically sets serial numbers)
- `sg3-utils` package installed (the source of SCSI detection overhead)

## References

- [udev(7) man page](https://man7.org/linux/man-pages/man7/udev.7.html)
- [sg3_utils documentation](https://sg.danny.cz/sg/sg3_utils.html)
- [Linux loop device documentation](https://man7.org/linux/man-pages/man4/loop.4.html)
- [Kernel commit: loop: add serial number sysfs attribute](https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/commit/?id=fc755d1e0c1e)
