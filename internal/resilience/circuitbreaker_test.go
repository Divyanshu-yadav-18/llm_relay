package resilience

import (
	"sync"
	"testing"
	"time"
)

func TestCircuitBreaker(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 3,
		ResetTimeout:     50 * time.Millisecond,
	}
	cb := NewCircuitBreaker(cfg)
	provider := "openai"

	t.Run("initial state is closed", func(t *testing.T) {
		if state := cb.State(provider); state != StateClosed {
			t.Errorf("expected closed, got %v", state)
		}
		if err := cb.Allow(provider); err != nil {
			t.Errorf("expected allowed, got %v", err)
		}
	})

	t.Run("opens after failure threshold", func(t *testing.T) {
		cb.RecordFailure(provider)
		cb.RecordFailure(provider)
		
		if state := cb.State(provider); state != StateClosed {
			t.Errorf("expected closed, got %v", state)
		}
		
		cb.RecordFailure(provider) // 3rd failure
		if state := cb.State(provider); state != StateOpen {
			t.Errorf("expected open, got %v", state)
		}
	})

	t.Run("rejects when open", func(t *testing.T) {
		err := cb.Allow(provider)
		if err == nil {
			t.Error("expected error when open")
		}
	})

	t.Run("transitions to half-open after timeout", func(t *testing.T) {
		time.Sleep(60 * time.Millisecond) // wait for ResetTimeout
		if state := cb.State(provider); state != StateHalfOpen {
			t.Errorf("expected half-open, got %v", state)
		}
		if err := cb.Allow(provider); err != nil {
			t.Errorf("expected allowed in half-open, got %v", err)
		}
	})

	t.Run("closes on success in half-open", func(t *testing.T) {
		cb.RecordSuccess(provider)
		if state := cb.State(provider); state != StateClosed {
			t.Errorf("expected closed, got %v", state)
		}
	})

	t.Run("re-opens on failure in half-open", func(t *testing.T) {
		// Get to open state
		cb.RecordFailure(provider)
		cb.RecordFailure(provider)
		cb.RecordFailure(provider)
		time.Sleep(60 * time.Millisecond)
		
		// Now in half-open
		cb.RecordFailure(provider) // should go straight to open
		if state := cb.State(provider); state != StateOpen {
			t.Errorf("expected open, got %v", state)
		}
	})

	t.Run("per-provider isolation", func(t *testing.T) {
		p1 := "provider1"
		p2 := "provider2"
		
		for i := 0; i < 3; i++ {
			cb.RecordFailure(p1)
		}
		
		if cb.State(p1) != StateOpen {
			t.Error("p1 should be open")
		}
		if cb.State(p2) != StateClosed {
			t.Error("p2 should be closed")
		}
	})

	t.Run("thread safety", func(t *testing.T) {
		var wg sync.WaitGroup
		cb := NewCircuitBreaker(cfg)
		
		// Run many parallel requests
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cb.Allow("parallel")
				cb.RecordFailure("parallel")
				cb.RecordSuccess("parallel")
				cb.State("parallel")
			}()
		}
		wg.Wait()
	})
}
