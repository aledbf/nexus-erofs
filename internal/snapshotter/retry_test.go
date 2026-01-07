package snapshotter

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetrySucceedsFirstAttempt(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := Retry(ctx, DefaultRetryConfig, func() error {
		attempts++
		return nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
}

func TestRetrySucceedsAfterRetries(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	succeedOn := 3

	cfg := RetryConfig{
		MaxAttempts: 5,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     10 * time.Millisecond,
		Multiplier:  2.0,
	}

	err := Retry(ctx, cfg, func() error {
		attempts++
		if attempts >= succeedOn {
			return nil
		}
		return errors.New("transient error")
	})

	if err != nil {
		t.Errorf("expected success, got %v", err)
	}
	if attempts != succeedOn {
		t.Errorf("expected %d attempts, got %d", succeedOn, attempts)
	}
}

func TestRetryFailsAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	expectedErr := errors.New("persistent error")

	cfg := RetryConfig{
		MaxAttempts: 3,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     10 * time.Millisecond,
		Multiplier:  2.0,
	}

	err := Retry(ctx, cfg, func() error {
		attempts++
		return expectedErr
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if attempts != cfg.MaxAttempts {
		t.Errorf("expected %d attempts, got %d", cfg.MaxAttempts, attempts)
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected wrapped error to contain original: %v", err)
	}
}

func TestRetryRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0

	cfg := RetryConfig{
		MaxAttempts: 10,
		InitialWait: 100 * time.Millisecond,
		MaxWait:     1 * time.Second,
		Multiplier:  2.0,
	}

	// Cancel after first attempt
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := Retry(ctx, cfg, func() error {
		attempts++
		return errors.New("keep trying")
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if attempts > 2 {
		t.Errorf("expected at most 2 attempts before cancel, got %d", attempts)
	}
}

func TestRetryWithResultSuccess(t *testing.T) {
	ctx := context.Background()
	cfg := RetryConfig{
		MaxAttempts: 3,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     10 * time.Millisecond,
		Multiplier:  2.0,
	}

	result, err := RetryWithResult(ctx, cfg, func() (string, error) {
		return "success", nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result != "success" {
		t.Errorf("expected 'success', got %q", result)
	}
}

func TestRetryWithResultFailure(t *testing.T) {
	ctx := context.Background()
	cfg := RetryConfig{
		MaxAttempts: 2,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     10 * time.Millisecond,
		Multiplier:  2.0,
	}

	result, err := RetryWithResult(ctx, cfg, func() (int, error) {
		return 0, errors.New("always fail")
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if result != 0 {
		t.Errorf("expected zero value, got %d", result)
	}
}

func TestRetryBackoffGrowth(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	var waits []time.Duration
	lastTime := time.Now()

	cfg := RetryConfig{
		MaxAttempts: 4,
		InitialWait: 10 * time.Millisecond,
		MaxWait:     100 * time.Millisecond,
		Multiplier:  2.0,
	}

	Retry(ctx, cfg, func() error { //nolint:errcheck
		now := time.Now()
		if attempts > 0 {
			waits = append(waits, now.Sub(lastTime))
		}
		lastTime = now
		attempts++
		return errors.New("fail")
	})

	// Verify backoff grows (with some tolerance for timing)
	for i := 1; i < len(waits); i++ {
		if waits[i] < waits[i-1] {
			// Allow some tolerance - timing isn't exact
			if waits[i]*2 < waits[i-1] {
				t.Errorf("backoff should grow: wait[%d]=%v < wait[%d]=%v", i, waits[i], i-1, waits[i-1])
			}
		}
	}
}

func TestRetryMaxWaitCap(t *testing.T) {
	ctx := context.Background()
	var waits []time.Duration
	lastTime := time.Now()

	cfg := RetryConfig{
		MaxAttempts: 10,
		InitialWait: 1 * time.Millisecond,
		MaxWait:     5 * time.Millisecond,
		Multiplier:  10.0, // Aggressive growth
	}

	Retry(ctx, cfg, func() error { //nolint:errcheck
		now := time.Now()
		if len(waits) > 0 || now.Sub(lastTime) > 0 {
			waits = append(waits, now.Sub(lastTime))
		}
		lastTime = now
		return errors.New("fail")
	})

	// Later waits should be capped at MaxWait (with tolerance)
	maxTolerance := cfg.MaxWait * 3 // Allow some OS scheduling variance
	for i, wait := range waits {
		if wait > maxTolerance && i > 2 {
			t.Errorf("wait[%d]=%v exceeds max cap %v", i, wait, cfg.MaxWait)
		}
	}
}
