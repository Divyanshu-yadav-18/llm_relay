package resilience

import (
	"fmt"
	"sync"
	"time"
)

// CircuitState represents the current state of the circuit breaker.
type CircuitState int

const (
	StateClosed CircuitState = iota // Normal operation
	StateOpen                       // Failing, reject requests
	StateHalfOpen                   // Testing with single request
)

// ErrCircuitOpen is returned when the circuit is open.
type ErrCircuitOpen struct {
	Provider string
}

func (e *ErrCircuitOpen) Error() string {
	return fmt.Sprintf("circuit open for provider: %s", e.Provider)
}

// CircuitBreakerConfig configures the circuit breaker.
type CircuitBreakerConfig struct {
	FailureThreshold    int           // Failures before opening
	ResetTimeout        time.Duration // Time before trying half-open
}

type providerState struct {
	mu           sync.Mutex
	state        CircuitState
	failures     int
	lastFailure  time.Time
}

// CircuitBreaker tracks failure rates per-provider and opens the circuit
// when failures exceed a threshold, preventing cascade failures.
type CircuitBreaker struct {
	cfg       CircuitBreakerConfig
	providers map[string]*providerState
	mu        sync.RWMutex
}

// NewCircuitBreaker creates a circuit breaker with the given config.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		cfg:       cfg,
		providers: make(map[string]*providerState),
	}
}

func (cb *CircuitBreaker) getProviderState(provider string) *providerState {
	cb.mu.RLock()
	ps, exists := cb.providers[provider]
	cb.mu.RUnlock()

	if !exists {
		cb.mu.Lock()
		defer cb.mu.Unlock()
		// Double-check
		ps, exists = cb.providers[provider]
		if !exists {
			ps = &providerState{state: StateClosed}
			cb.providers[provider] = ps
		}
	}
	return ps
}

// Allow checks whether a request to the named provider should be allowed.
// Returns an error if the circuit is open.
func (cb *CircuitBreaker) Allow(provider string) error {
	ps := cb.getProviderState(provider)
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.state == StateOpen {
		if time.Since(ps.lastFailure) >= cb.cfg.ResetTimeout {
			ps.state = StateHalfOpen
			return nil
		}
		return &ErrCircuitOpen{Provider: provider}
	}
	return nil
}

// RecordSuccess records a successful call to the named provider.
// In half-open state, this closes the circuit.
func (cb *CircuitBreaker) RecordSuccess(provider string) {
	ps := cb.getProviderState(provider)
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.failures = 0
	ps.state = StateClosed
}

// RecordFailure records a failed call to the named provider.
// If failures exceed the threshold, opens the circuit.
func (cb *CircuitBreaker) RecordFailure(provider string) {
	ps := cb.getProviderState(provider)
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.failures++
	if ps.failures >= cb.cfg.FailureThreshold {
		ps.state = StateOpen
		ps.lastFailure = time.Now()
	} else if ps.state == StateHalfOpen {
		ps.state = StateOpen
		ps.lastFailure = time.Now()
	}
}

// State returns the current state for a provider.
func (cb *CircuitBreaker) State(provider string) CircuitState {
	ps := cb.getProviderState(provider)
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.state == StateOpen && time.Since(ps.lastFailure) >= cb.cfg.ResetTimeout {
		return StateHalfOpen
	}
	return ps.state
}
