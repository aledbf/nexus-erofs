package snapshotter

import (
	"fmt"
	"sync"
)

// MountState represents the current state of an ext4 block mount.
// This is used to track mounts that the snapshotter creates on the host
// for extract snapshots (where the differ needs to write to the ext4).
//
// WHY EXPLICIT STATE:
// Previously, mount state was determined by calling mountinfo.Mounted(),
// which has several problems:
//   - Race conditions: another process could unmount between check and use
//   - Non-existent paths: mountinfo.Mounted() fails on paths that don't exist
//   - Implicit state: hard to reason about and test
//
// Explicit state tracking solves these issues by recording what we've done,
// not what the filesystem currently looks like.
type MountState int

const (
	// MountStateUnknown is the zero value, indicating state hasn't been set.
	MountStateUnknown MountState = iota

	// MountStateUnmounted indicates the ext4 is not mounted on the host.
	// This is the initial state for new snapshots and the final state after commit.
	MountStateUnmounted

	// MountStateMounted indicates the ext4 is mounted on the host.
	// This state is set when mountBlockRwLayer succeeds.
	MountStateMounted

	// MountStateMountedByUs indicates we mounted it and are responsible for cleanup.
	// This distinguishes between mounts we created vs mounts that existed before.
	MountStateMountedByUs
)

// String implements fmt.Stringer for logging.
func (s MountState) String() string {
	switch s {
	case MountStateUnknown:
		return "unknown"
	case MountStateUnmounted:
		return "unmounted"
	case MountStateMounted:
		return "mounted"
	case MountStateMountedByUs:
		return "mounted-by-us"
	default:
		return fmt.Sprintf("invalid(%d)", s)
	}
}

// IsMounted returns true if the state indicates the mount is active.
func (s MountState) IsMounted() bool {
	return s == MountStateMounted || s == MountStateMountedByUs
}

// NeedsCleanup returns true if we are responsible for unmounting.
func (s MountState) NeedsCleanup() bool {
	return s == MountStateMountedByUs
}

// MountTracker provides thread-safe tracking of ext4 block mount states.
// It maps snapshot IDs to their current mount state.
//
// WHY A TRACKER:
// The snapshotter may have multiple concurrent operations on different snapshots.
// A central tracker ensures consistent state visibility across goroutines.
//
// SCOPE:
// This only tracks ext4 block mounts used for extract snapshots on the host.
// It does NOT track:
//   - EROFS mounts (those are in the VM, not on host)
//   - Overlay mounts (also in the VM)
//   - The writable layer file existence (that's just a file, not a mount)
type MountTracker struct {
	mu     sync.RWMutex
	states map[string]MountState
}

// NewMountTracker creates a new mount state tracker.
func NewMountTracker() *MountTracker {
	return &MountTracker{
		states: make(map[string]MountState),
	}
}

// Get returns the current mount state for a snapshot ID.
// Returns MountStateUnknown if the ID hasn't been tracked.
func (t *MountTracker) Get(id string) MountState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.states[id]
}

// Set updates the mount state for a snapshot ID.
func (t *MountTracker) Set(id string, state MountState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if state == MountStateUnmounted {
		// Remove from map when unmounted to prevent unbounded growth
		delete(t.states, id)
	} else {
		t.states[id] = state
	}
}

// SetMounted marks a snapshot as mounted by us (we are responsible for cleanup).
func (t *MountTracker) SetMounted(id string) {
	t.Set(id, MountStateMountedByUs)
}

// SetUnmounted marks a snapshot as unmounted and removes it from tracking.
func (t *MountTracker) SetUnmounted(id string) {
	t.Set(id, MountStateUnmounted)
}

// IsMounted returns true if the snapshot's ext4 is currently mounted.
func (t *MountTracker) IsMounted(id string) bool {
	return t.Get(id).IsMounted()
}

// NeedsCleanup returns true if we mounted this and need to unmount it.
func (t *MountTracker) NeedsCleanup(id string) bool {
	return t.Get(id).NeedsCleanup()
}

// GetAllMounted returns all snapshot IDs that are currently mounted.
// Useful for cleanup during snapshotter shutdown.
func (t *MountTracker) GetAllMounted() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var mounted []string
	for id, state := range t.states {
		if state.IsMounted() {
			mounted = append(mounted, id)
		}
	}
	return mounted
}

// Clear removes all tracked state. Used during shutdown.
func (t *MountTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.states = make(map[string]MountState)
}
