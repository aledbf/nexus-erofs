package snapshotter

import (
	"context"
	"fmt"
	"time"
)

// RetryConfig controls retry behavior for operations that may fail transiently.
type RetryConfig struct {
	// MaxAttempts is the maximum number of attempts (including initial).
	MaxAttempts int

	// InitialWait is the wait time before the first retry.
	InitialWait time.Duration

	// MaxWait is the maximum wait time between retries.
	MaxWait time.Duration

	// Multiplier is applied to wait time after each retry.
	Multiplier float64
}

// DefaultRetryConfig provides reasonable defaults for most operations.
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	InitialWait: 100 * time.Millisecond,
	MaxWait:     2 * time.Second,
	Multiplier:  2.0,
}

// Retry executes fn with exponential backoff until it succeeds or max attempts reached.
// Returns nil if fn eventually succeeds, or the last error if all attempts fail.
//
// Example usage:
//
//	err := Retry(ctx, DefaultRetryConfig, func() error {
//	    return syncFile(path)
//	})
func Retry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	_, err := retryLoop(ctx, cfg, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

// RetryWithResult executes fn with exponential backoff, returning both result and error.
// Useful for operations that return a value.
//
// Example usage:
//
//	path, err := RetryWithResult(ctx, DefaultRetryConfig, func() (string, error) {
//	    return findLayerBlob(id)
//	})
func RetryWithResult[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	return retryLoop(ctx, cfg, fn)
}

// retryLoop is the common implementation for retry logic.
// It handles exponential backoff with proper timer cleanup to avoid memory leaks.
func retryLoop[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T

	// Validate MaxAttempts to prevent nil error wrapping
	if cfg.MaxAttempts < 1 {
		return zero, fmt.Errorf("invalid retry config: MaxAttempts must be >= 1, got %d", cfg.MaxAttempts)
	}

	var lastErr error
	wait := cfg.InitialWait

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if result, err := fn(); err == nil {
			return result, nil
		} else {
			lastErr = err
		}

		// Don't wait after last attempt
		if attempt == cfg.MaxAttempts {
			break
		}

		// Use time.NewTimer instead of time.After to avoid memory leak.
		// time.After timers are not garbage collected until they fire,
		// even if the context is canceled.
		if err := waitWithContext(ctx, wait); err != nil {
			return zero, err
		}

		// Calculate next wait with exponential backoff
		wait = time.Duration(float64(wait) * cfg.Multiplier)
		if wait > cfg.MaxWait {
			wait = cfg.MaxWait
		}
	}

	return zero, fmt.Errorf("after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

// waitWithContext waits for the specified duration or until context is canceled.
// Uses time.NewTimer for proper cleanup to avoid memory leaks.
func waitWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// IsRetryable returns true if the error is likely transient and worth retrying.
// This is a heuristic and may need adjustment based on observed failure patterns.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Add specific error type checks as needed
	// For now, we retry all errors and let the caller decide
	return true
}
