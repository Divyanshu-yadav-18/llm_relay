package resilience

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

// RetryConfig holds retry policy configuration.
type RetryConfig struct {
	MaxRetries        int
	InitialBackoffMs  int
	MaxBackoffMs      int
	BackoffMultiplier float64
}

// RetryableFunc is a function that can be retried. It returns an error
// that may or may not be retryable.
type RetryableFunc func(ctx context.Context) error

// WithRetry executes fn with configurable retries.
// It only retries if the error is a *providers.ProviderError with Retryable=true.
// Uses exponential backoff with full jitter to prevent thundering herd.
// Returns the last error if all retries fail.
func WithRetry(ctx context.Context, cfg RetryConfig, fn RetryableFunc, logger *slog.Logger) error {
	var lastErr error
	backoff := float64(cfg.InitialBackoffMs)

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		var providerErr *providers.ProviderError
		if errors.As(err, &providerErr) {
			if !providerErr.Retryable {
				return err
			}
		} else {
			// Non-provider error, or an error we don't know how to retry
			return err
		}

		if attempt == cfg.MaxRetries {
			break
		}

		// Calculate jittered delay
		jitteredDelay := rand.Float64() * backoff
		delayDuration := time.Duration(jitteredDelay) * time.Millisecond

		if logger != nil {
			logger.Warn("retryable error encountered", 
				"attempt", attempt+1, 
				"delay_ms", delayDuration.Milliseconds(), 
				"error", err,
			)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delayDuration):
		}

		backoff *= cfg.BackoffMultiplier
		if backoff > float64(cfg.MaxBackoffMs) {
			backoff = float64(cfg.MaxBackoffMs)
		}
	}

	return lastErr
}
