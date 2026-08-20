package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

func TestWithRetry(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:        3,
		InitialBackoffMs:  10,
		MaxBackoffMs:      50,
		BackoffMultiplier: 2.0,
	}

	t.Run("successful first attempt (no retry needed)", func(t *testing.T) {
		attempts := 0
		err := WithRetry(context.Background(), cfg, func(ctx context.Context) error {
			attempts++
			return nil
		}, nil)

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
	})

	t.Run("retry on retryable error then success", func(t *testing.T) {
		attempts := 0
		err := WithRetry(context.Background(), cfg, func(ctx context.Context) error {
			attempts++
			if attempts < 3 {
				return &providers.ProviderError{Retryable: true}
			}
			return nil
		}, nil)

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}
	})

	t.Run("no retry on non-retryable error", func(t *testing.T) {
		attempts := 0
		err := WithRetry(context.Background(), cfg, func(ctx context.Context) error {
			attempts++
			return &providers.ProviderError{Retryable: false}
		}, nil)

		if err == nil {
			t.Error("expected error, got nil")
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
	})

	t.Run("max retries exhausted", func(t *testing.T) {
		attempts := 0
		expectedErr := &providers.ProviderError{Retryable: true}
		err := WithRetry(context.Background(), cfg, func(ctx context.Context) error {
			attempts++
			return expectedErr
		}, nil)

		if !errors.Is(err, expectedErr) {
			t.Errorf("expected err %v, got %v", expectedErr, err)
		}
		if attempts != 4 { // Initial + 3 retries
			t.Errorf("expected 4 attempts, got %d", attempts)
		}
	})

	t.Run("context cancellation stops retries", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		err := WithRetry(ctx, cfg, func(c context.Context) error {
			attempts++
			cancel() // cancel after first attempt
			return &providers.ProviderError{Retryable: true}
		}, nil)

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
	})

	t.Run("backoff increases", func(t *testing.T) {
		start := time.Now()
		cfgBackoff := RetryConfig{
			MaxRetries:        2,
			InitialBackoffMs:  50,
			MaxBackoffMs:      100,
			BackoffMultiplier: 2.0,
		}
		WithRetry(context.Background(), cfgBackoff, func(ctx context.Context) error {
			return &providers.ProviderError{Retryable: true}
		}, nil)
		elapsed := time.Since(start)
		
		if elapsed > 300*time.Millisecond {
			t.Errorf("took too long, expected < 300ms, got %v", elapsed)
		}
	})
}
