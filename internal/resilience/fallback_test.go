package resilience

import (
	"context"
	"testing"
	"time"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

func TestFallbackExecutor(t *testing.T) {
	retryCfg := RetryConfig{MaxRetries: 0}
	cbCfg := CircuitBreakerConfig{FailureThreshold: 2, ResetTimeout: time.Second}
	cb := NewCircuitBreaker(cbCfg)

	p1 := &MockProvider{NameStr: "p1"}
	p2 := &MockProvider{NameStr: "p2"}
	registry := map[string]providers.Provider{"p1": p1, "p2": p2}
	chain := []string{"p1", "p2"}

	fe := NewFallbackExecutor(chain, registry, cb, retryCfg, nil)

	t.Run("primary provider succeeds (no fallback)", func(t *testing.T) {
		calledP1 := false
		err := fe.Execute(context.Background(), func(ctx context.Context, p providers.Provider) error {
			if p.Name() == "p1" {
				calledP1 = true
				return nil
			}
			t.Error("should not call p2")
			return nil
		})
		
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if !calledP1 {
			t.Error("p1 was not called")
		}
	})

	t.Run("falls back on retryable failure", func(t *testing.T) {
		calledP2 := false
		err := fe.Execute(context.Background(), func(ctx context.Context, p providers.Provider) error {
			if p.Name() == "p1" {
				return &providers.ProviderError{Retryable: true}
			}
			if p.Name() == "p2" {
				calledP2 = true
				return nil
			}
			return nil
		})

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if !calledP2 {
			t.Error("p2 was not called")
		}
	})

	t.Run("stops on non-retryable failure (no fallback)", func(t *testing.T) {
		err := fe.Execute(context.Background(), func(ctx context.Context, p providers.Provider) error {
			if p.Name() == "p1" {
				return &providers.ProviderError{Retryable: false}
			}
			if p.Name() == "p2" {
				t.Error("should not fall back to p2")
			}
			return nil
		})

		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("skips providers with open circuit", func(t *testing.T) {
		// Open circuit for p1
		cb.RecordFailure("p1")
		cb.RecordFailure("p1")
		
		calledP2 := false
		err := fe.Execute(context.Background(), func(ctx context.Context, p providers.Provider) error {
			if p.Name() == "p1" {
				t.Error("p1 should be skipped due to open circuit")
			}
			if p.Name() == "p2" {
				calledP2 = true
				return nil
			}
			return nil
		})

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if !calledP2 {
			t.Error("p2 was not called")
		}
	})

	t.Run("all providers fail returns aggregate error", func(t *testing.T) {
		// Reset circuit breaker by recreating it
		cb = NewCircuitBreaker(cbCfg)
		fe = NewFallbackExecutor(chain, registry, cb, retryCfg, nil)

		err := fe.Execute(context.Background(), func(ctx context.Context, p providers.Provider) error {
			return &providers.ProviderError{Retryable: true}
		})

		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("empty chain returns error", func(t *testing.T) {
		emptyFe := NewFallbackExecutor([]string{}, registry, cb, retryCfg, nil)
		err := emptyFe.Execute(context.Background(), func(ctx context.Context, p providers.Provider) error {
			return nil
		})
		if err == nil || err.Error() != "empty fallback chain" {
			t.Errorf("expected empty fallback chain error, got %v", err)
		}
	})
}
