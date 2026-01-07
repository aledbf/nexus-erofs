package snapshotter

import (
	"sync"
	"testing"
)

func TestMountStateString(t *testing.T) {
	tests := []struct {
		state    MountState
		expected string
	}{
		{MountStateUnknown, "unknown"},
		{MountStateUnmounted, "unmounted"},
		{MountStateMounted, "mounted"},
		{MountStateMountedByUs, "mounted-by-us"},
		{MountState(99), "invalid(99)"},
	}

	for _, tt := range tests {
		got := tt.state.String()
		if got != tt.expected {
			t.Errorf("MountState(%d).String() = %q, want %q", tt.state, got, tt.expected)
		}
	}
}

func TestMountStateIsMounted(t *testing.T) {
	tests := []struct {
		state    MountState
		expected bool
	}{
		{MountStateUnknown, false},
		{MountStateUnmounted, false},
		{MountStateMounted, true},
		{MountStateMountedByUs, true},
	}

	for _, tt := range tests {
		got := tt.state.IsMounted()
		if got != tt.expected {
			t.Errorf("MountState(%d).IsMounted() = %v, want %v", tt.state, got, tt.expected)
		}
	}
}

func TestMountStateNeedsCleanup(t *testing.T) {
	tests := []struct {
		state    MountState
		expected bool
	}{
		{MountStateUnknown, false},
		{MountStateUnmounted, false},
		{MountStateMounted, false},
		{MountStateMountedByUs, true},
	}

	for _, tt := range tests {
		got := tt.state.NeedsCleanup()
		if got != tt.expected {
			t.Errorf("MountState(%d).NeedsCleanup() = %v, want %v", tt.state, got, tt.expected)
		}
	}
}

func TestMountTrackerBasicOperations(t *testing.T) {
	tracker := NewMountTracker()

	// Initially unknown
	if state := tracker.Get("snap-1"); state != MountStateUnknown {
		t.Errorf("initial state = %v, want Unknown", state)
	}

	// Set mounted
	tracker.SetMounted("snap-1")
	if state := tracker.Get("snap-1"); state != MountStateMountedByUs {
		t.Errorf("after SetMounted = %v, want MountedByUs", state)
	}

	// IsMounted should return true
	if !tracker.IsMounted("snap-1") {
		t.Error("IsMounted should return true for mounted snapshot")
	}

	// NeedsCleanup should return true
	if !tracker.NeedsCleanup("snap-1") {
		t.Error("NeedsCleanup should return true for MountedByUs")
	}

	// Set unmounted
	tracker.SetUnmounted("snap-1")
	if state := tracker.Get("snap-1"); state != MountStateUnknown {
		t.Errorf("after SetUnmounted = %v, want Unknown (removed from map)", state)
	}

	// IsMounted should return false
	if tracker.IsMounted("snap-1") {
		t.Error("IsMounted should return false after unmount")
	}
}

func TestMountTrackerGetAllMounted(t *testing.T) {
	tracker := NewMountTracker()

	// Mount several snapshots
	tracker.SetMounted("snap-1")
	tracker.SetMounted("snap-2")
	tracker.Set("snap-3", MountStateMounted) // External mount
	tracker.SetMounted("snap-4")
	tracker.SetUnmounted("snap-2") // Unmount one

	mounted := tracker.GetAllMounted()

	// Should have 3 mounted (snap-1, snap-3, snap-4)
	if len(mounted) != 3 {
		t.Errorf("GetAllMounted returned %d, want 3", len(mounted))
	}

	// Verify the correct ones are in the list
	found := make(map[string]bool)
	for _, id := range mounted {
		found[id] = true
	}

	if !found["snap-1"] {
		t.Error("snap-1 should be in mounted list")
	}
	if found["snap-2"] {
		t.Error("snap-2 should not be in mounted list (unmounted)")
	}
	if !found["snap-3"] {
		t.Error("snap-3 should be in mounted list")
	}
	if !found["snap-4"] {
		t.Error("snap-4 should be in mounted list")
	}
}

func TestMountTrackerClear(t *testing.T) {
	tracker := NewMountTracker()

	tracker.SetMounted("snap-1")
	tracker.SetMounted("snap-2")

	if len(tracker.GetAllMounted()) != 2 {
		t.Error("should have 2 mounted before clear")
	}

	tracker.Clear()

	if len(tracker.GetAllMounted()) != 0 {
		t.Error("should have 0 mounted after clear")
	}

	// New operations should work after clear
	tracker.SetMounted("snap-3")
	if !tracker.IsMounted("snap-3") {
		t.Error("should be able to mount after clear")
	}
}

func TestMountTrackerConcurrentAccess(t *testing.T) {
	tracker := NewMountTracker()
	const numGoroutines = 100

	var wg sync.WaitGroup

	// Concurrent mounts
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			snapID := string(rune('a' + id%26))
			tracker.SetMounted(snapID)
			_ = tracker.IsMounted(snapID)
			_ = tracker.Get(snapID)
		}(i)
	}

	wg.Wait()

	// Concurrent unmounts
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			snapID := string(rune('a' + id%26))
			tracker.SetUnmounted(snapID)
			_ = tracker.IsMounted(snapID)
		}(i)
	}

	wg.Wait()

	// After all unmounts, should be empty
	if len(tracker.GetAllMounted()) != 0 {
		t.Errorf("expected 0 mounted after concurrent unmounts, got %d", len(tracker.GetAllMounted()))
	}
}

func TestMountTrackerSetVariants(t *testing.T) {
	tracker := NewMountTracker()

	// Test Set with different states
	tracker.Set("snap-1", MountStateMounted)
	if state := tracker.Get("snap-1"); state != MountStateMounted {
		t.Errorf("Set(Mounted) = %v, want Mounted", state)
	}

	// MountStateMounted does not need cleanup
	if tracker.NeedsCleanup("snap-1") {
		t.Error("MountStateMounted should not need cleanup")
	}

	// MountStateMountedByUs needs cleanup
	tracker.Set("snap-2", MountStateMountedByUs)
	if !tracker.NeedsCleanup("snap-2") {
		t.Error("MountStateMountedByUs should need cleanup")
	}

	// Setting to Unmounted removes from map
	tracker.Set("snap-1", MountStateUnmounted)
	if state := tracker.Get("snap-1"); state != MountStateUnknown {
		t.Errorf("after Set(Unmounted) = %v, want Unknown", state)
	}
}
