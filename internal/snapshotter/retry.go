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
	var lastErr error
	wait := cfg.InitialWait

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		// Don't wait after last attempt
		if attempt == cfg.MaxAttempts {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
			wait = time.Duration(float64(wait) * cfg.Multiplier)
			if wait > cfg.MaxWait {
				wait = cfg.MaxWait
			}
		}
	}

	return fmt.Errorf("after %d attempts: %w", cfg.MaxAttempts, lastErr)
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
	var lastErr error
	var zero T
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

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(wait):
			wait = time.Duration(float64(wait) * cfg.Multiplier)
			if wait > cfg.MaxWait {
				wait = cfg.MaxWait
			}
		}
	}

	return zero, fmt.Errorf("after %d attempts: %w", cfg.MaxAttempts, lastErr)
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
