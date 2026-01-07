package snapshotter

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestLayerBlobNotFoundErrorAs verifies errors.As works correctly for type matching.
// Note: We use errors.As (not errors.Is) for structural error types per Go idioms.
func TestLayerBlobNotFoundErrorAs(t *testing.T) {
	err := &LayerBlobNotFoundError{
		SnapshotID: "test-123",
		Dir:        "/test/path",
		Searched:   []string{"*.erofs"},
	}

	// Test errors.As for type-based matching
	var target *LayerBlobNotFoundError
	if !errors.As(err, &target) {
		t.Error("errors.As should match LayerBlobNotFoundError")
	}
	if target.SnapshotID != "test-123" {
		t.Errorf("expected snapshot ID test-123, got %s", target.SnapshotID)
	}

	// Test that wrapped error can be unwrapped with errors.As
	wrapped := &CommitConversionError{
		SnapshotID: "commit-test",
		UpperDir:   "/upper",
		Cause:      err,
	}

	var wrappedTarget *LayerBlobNotFoundError
	if !errors.As(wrapped, &wrappedTarget) {
		t.Error("errors.As should find LayerBlobNotFoundError in chain")
	}
	if wrappedTarget.SnapshotID != "test-123" {
		t.Errorf("expected snapshot ID test-123, got %s", wrappedTarget.SnapshotID)
	}
}

// TestErrorChainDepth verifies deep error chains work correctly.
func TestErrorChainDepth(t *testing.T) {
	// Create a 3-level error chain
	level1 := errors.New("root cause: filesystem full")
	level2 := &BlockMountError{
		Source: "/path/to/block.img",
		Target: "/mnt/target",
		Cause:  level1,
	}
	level3 := &CommitConversionError{
		SnapshotID: "snap-abc",
		UpperDir:   "/var/lib/snapshotter/abc/upper",
		Cause:      level2,
	}

	// Should find root cause
	if !errors.Is(level3, level1) {
		t.Error("should find root error through 3-level chain")
	}

	// Should find intermediate error
	var blockErr *BlockMountError
	if !errors.As(level3, &blockErr) {
		t.Error("should find BlockMountError in chain")
	}

	// Error message should include context from all levels
	msg := level3.Error()
	if !errContains(msg, "snap-abc") {
		t.Error("error message should contain snapshot ID")
	}
	// Note: The full chain is not automatically included in Error() -
	// only the Cause's Error() is included via %v format
}

// TestIncompatibleBlockSizeErrorFields verifies all fields are accessible.
func TestIncompatibleBlockSizeErrorFields(t *testing.T) {
	err := &IncompatibleBlockSizeError{
		LayerCount: 7,
		Details:    "layer 3 uses 512 bytes, others use 4096 bytes",
	}

	msg := err.Error()
	if !errContains(msg, "7 layers") {
		t.Errorf("error should mention layer count: %s", msg)
	}
	if !errContains(msg, "512 bytes") {
		t.Errorf("error should include details: %s", msg)
	}
}

// TestRetryExhaustedError verifies the error message after max retries.
func TestRetryExhaustedError(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 3,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     1 * time.Millisecond,
		Multiplier:  1.0,
	}

	expectedErr := &LayerBlobNotFoundError{SnapshotID: "retry-test"}

	err := Retry(context.Background(), cfg, func() error {
		return expectedErr
	})

	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}

	// Error message should indicate retry count
	msg := err.Error()
	if !errContains(msg, "3 attempts") {
		t.Errorf("error should mention attempt count: %s", msg)
	}

	// Should still be able to extract original error
	var target *LayerBlobNotFoundError
	if !errors.As(err, &target) {
		t.Error("should find original error in wrapped error")
	}
}

// TestEmptyLayerSequenceOperations verifies operations on empty sequences are safe.
func TestEmptyLayerSequenceOperations(t *testing.T) {
	empty := LayerSequence{}

	// All operations should be safe on empty sequence
	if !empty.IsEmpty() {
		t.Error("empty sequence should be empty")
	}

	if empty.Len() != 0 {
		t.Errorf("empty sequence Len() = %d, want 0", empty.Len())
	}

	reversed := empty.Reverse()
	if !reversed.IsEmpty() {
		t.Error("reversed empty sequence should be empty")
	}

	oldest := empty.ToOldestFirst()
	if !oldest.IsEmpty() {
		t.Error("ToOldestFirst of empty should be empty")
	}

	newest := empty.ToNewestFirst()
	if !newest.IsEmpty() {
		t.Error("ToNewestFirst of empty should be empty")
	}
}

// TestNilSliceLayerSequence verifies nil slice handling.
func TestNilSliceLayerSequence(t *testing.T) {
	seq := NewNewestFirst(nil)

	if !seq.IsEmpty() {
		t.Error("nil slice should create empty sequence")
	}

	if seq.Len() != 0 {
		t.Error("nil slice sequence should have length 0")
	}

	// Should not panic on operations
	_ = seq.Reverse()
	_ = seq.ToOldestFirst()
	_ = seq.ToNewestFirst()
}

// TestRetryZeroAttempts verifies edge case of zero attempts returns an error.
func TestRetryZeroAttempts(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 0,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     1 * time.Millisecond,
		Multiplier:  1.0,
	}

	called := false
	err := Retry(context.Background(), cfg, func() error {
		called = true
		return nil
	})

	// Should return an error for invalid config
	if err == nil {
		t.Error("expected error for MaxAttempts=0")
	}

	// Function should never be called
	if called {
		t.Error("function should not be called with MaxAttempts=0")
	}

	// Error message should indicate the problem
	if !errContains(err.Error(), "MaxAttempts") {
		t.Errorf("error should mention MaxAttempts: %v", err)
	}
}

// TestBlockMountErrorNilCause verifies nil cause is handled.
func TestBlockMountErrorNilCause(t *testing.T) {
	err := &BlockMountError{
		Source: "/path/source",
		Target: "/path/target",
		Cause:  nil,
	}

	// Should not panic
	msg := err.Error()
	if msg == "" {
		t.Error("error message should not be empty")
	}

	// Unwrap should return nil safely
	if err.Unwrap() != nil {
		t.Error("Unwrap with nil cause should return nil")
	}
}
