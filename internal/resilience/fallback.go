package resilience

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

// FallbackExecutor runs a request against a prioritized chain of providers.
// If the primary provider fails with a retryable error, it tries the next
// provider in the chain. Non-retryable errors (4xx) fail immediately.
type FallbackExecutor struct {
	chain          []string
	registry       map[string]providers.Provider
	circuitBreaker *CircuitBreaker
	retryConfig    RetryConfig
	logger         *slog.Logger
}

// NewFallbackExecutor creates a fallback executor.
func NewFallbackExecutor(
	chain []string,
	registry map[string]providers.Provider,
	cb *CircuitBreaker,
	retryCfg RetryConfig,
	logger *slog.Logger,
) *FallbackExecutor {
	return &FallbackExecutor{
		chain:          chain,
		registry:       registry,
		circuitBreaker: cb,
		retryConfig:    retryCfg,
		logger:         logger,
	}
}

// Execute runs the given function against providers in the fallback chain.
// For each provider:
//   1. Check circuit breaker — skip if open
//   2. Try the request with retries
//   3. On success, record success and return
//   4. On retryable failure, record failure and try next provider
//   5. On non-retryable failure, return immediately
func (fe *FallbackExecutor) Execute(
	ctx context.Context,
	fn func(ctx context.Context, p providers.Provider) error,
) error {
	if len(fe.chain) == 0 {
		return errors.New("empty fallback chain")
	}

	var errs []error

	for i, providerName := range fe.chain {
		provider, ok := fe.registry[providerName]
		if !ok {
			if fe.logger != nil {
				fe.logger.Warn("provider in fallback chain not found in registry", "provider", providerName)
			}
			errs = append(errs, fmt.Errorf("provider %s not found", providerName))
			continue
		}

		// 1. Check circuit breaker
		if err := fe.circuitBreaker.Allow(providerName); err != nil {
			if fe.logger != nil {
				fe.logger.Warn("skipping provider due to open circuit", "provider", providerName)
			}
			errs = append(errs, err)
			continue
		}

		if i > 0 && fe.logger != nil {
			fe.logger.Info("falling back", "from", fe.chain[i-1], "to", providerName)
		}

		// 2. Try the request with retries
		err := WithRetry(ctx, fe.retryConfig, func(retryCtx context.Context) error {
			return fn(retryCtx, provider)
		}, fe.logger)

		if err == nil {
			// 3. On success, record success and return
			fe.circuitBreaker.RecordSuccess(providerName)
			return nil
		}

		// Failure occurred
		fe.circuitBreaker.RecordFailure(providerName)
		errs = append(errs, err)

		var providerErr *providers.ProviderError
		if errors.As(err, &providerErr) {
			if !providerErr.Retryable {
				// 5. On non-retryable failure, return immediately
				return err
			}
		} else {
			// Not a provider error, return immediately
			return err
		}
		// 4. On retryable failure, record failure and try next provider (continue loop)
	}

	// If all providers fail, return an aggregate error
	return errors.Join(errs...)
}
