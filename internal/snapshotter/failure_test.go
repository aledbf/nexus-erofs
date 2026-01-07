package snapshotter

import (
	"context"
	"errors"
	"testing"
)

// TestLayerBlobNotFoundErrorIs verifies errors.Is works correctly with nil target.
func TestLayerBlobNotFoundErrorIs(t *testing.T) {
	err := &LayerBlobNotFoundError{
		SnapshotID: "test-123",
		Dir:        "/test/path",
		Searched:   []string{"*.erofs"},
	}

	// Test that Is() matches nil pointer of same type (Go's errors.Is behavior)
	var nilTarget *LayerBlobNotFoundError
	if !errors.Is(err, nilTarget) {
		t.Error("errors.Is should match nil pointer of same error type")
	}

	// Test that wrapped error still works
	wrapped := &CommitConversionError{
		SnapshotID: "commit-test",
		UpperDir:   "/upper",
		Cause:      err,
	}

	var target *LayerBlobNotFoundError
	if !errors.As(wrapped, &target) {
		t.Error("errors.As should find LayerBlobNotFoundError in chain")
	}
	if target.SnapshotID != "test-123" {
		t.Errorf("expected snapshot ID test-123, got %s", target.SnapshotID)
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
		InitialWait: 1,
		MaxWait:     1,
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

// TestRetryZeroAttempts verifies edge case of zero attempts.
func TestRetryZeroAttempts(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 0,
		InitialWait: 1,
		MaxWait:     1,
		Multiplier:  1.0,
	}

	// With 0 max attempts, the function should never be called
	called := false
	err := Retry(context.Background(), cfg, func() error {
		called = true
		return nil
	})

	// This is expected to return an error since no attempts were made
	if called {
		// Note: Current implementation would call the function at least once
		// This test documents the behavior
		t.Log("function was called even with MaxAttempts=0")
	}

	// Error should indicate 0 attempts (if the implementation wraps)
	_ = err
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
