package snapshotter

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestLayerSequenceConcurrentAccess verifies LayerSequence is safe for concurrent reads.
// LayerSequence is immutable by design - all mutating operations return copies.
func TestLayerSequenceConcurrentAccess(t *testing.T) {
	const numGoroutines = 100
	original := NewNewestFirst([]string{"layer5", "layer4", "layer3", "layer2", "layer1"})

	var wg sync.WaitGroup
	errors := make(chan string, numGoroutines*3)

	for range numGoroutines {
		wg.Add(3)

		// Concurrent ToOldestFirst
		go func() {
			defer wg.Done()
			result := original.ToOldestFirst()
			if result.Order != OrderOldestFirst {
				errors <- "ToOldestFirst returned wrong order"
			}
			if result.IDs[0] != "layer1" {
				errors <- "ToOldestFirst returned wrong first element"
			}
		}()

		// Concurrent Reverse
		go func() {
			defer wg.Done()
			result := original.Reverse()
			if result.Order != OrderOldestFirst {
				errors <- "Reverse returned wrong order"
			}
			if result.IDs[0] != "layer1" {
				errors <- "Reverse returned wrong first element"
			}
		}()

		// Concurrent Len
		go func() {
			defer wg.Done()
			if original.Len() != 5 {
				errors <- "Len returned wrong value"
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent access error: %s", err)
	}

	// Verify original is unchanged after all operations
	if original.Order != OrderNewestFirst {
		t.Error("original order was modified")
	}
	if original.IDs[0] != "layer5" {
		t.Error("original IDs were modified")
	}
}

// TestLayerSequenceNoDataRace runs operations that would trigger race detector if unsafe.
func TestLayerSequenceNoDataRace(t *testing.T) {
	// Create sequence that will be accessed concurrently
	seq := NewNewestFirst([]string{"a", "b", "c", "d", "e"})

	var wg sync.WaitGroup

	// Mix of read operations
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 5 {
			case 0:
				_ = seq.Len()
			case 1:
				_ = seq.IsEmpty()
			case 2:
				_ = seq.ToOldestFirst()
			case 3:
				_ = seq.ToNewestFirst()
			case 4:
				_ = seq.Reverse()
			}
		}(i)
	}

	wg.Wait()
}

// TestRetryWithConcurrentCalls verifies Retry is safe for concurrent use.
func TestRetryWithConcurrentCalls(t *testing.T) {
	const numGoroutines = 20
	var wg sync.WaitGroup
	results := make(chan int, numGoroutines)

	cfg := RetryConfig{
		MaxAttempts: 3,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     5 * time.Millisecond,
		Multiplier:  2.0,
	}

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			attempts := 0
			_ = Retry(context.Background(), cfg, func() error {
				attempts++
				if attempts >= 2 {
					return nil
				}
				return &LayerBlobNotFoundError{SnapshotID: "test"}
			})
			results <- attempts
		}(i)
	}

	wg.Wait()
	close(results)

	// All goroutines should complete successfully
	count := 0
	for attempts := range results {
		count++
		if attempts < 2 {
			t.Errorf("expected at least 2 attempts, got %d", attempts)
		}
	}

	if count != numGoroutines {
		t.Errorf("expected %d results, got %d", numGoroutines, count)
	}
}
