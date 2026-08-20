// Package main is the entrypoint for the LLM Relay gateway.
// It initializes configuration, sets up providers with resilience wrappers,
// and starts the HTTP server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/config"
	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers/anthropic"
	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers/ollama"
	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers/openai"
	"github.com/Divyanshu-yadav-18/llm_relay/internal/proxy"
	"github.com/Divyanshu-yadav-18/llm_relay/internal/resilience"
)

func main() {
	configPath := flag.String("config", "configs/gateway.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	var logLevel slog.Level
	if cfg.Server.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	} else {
		logLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// --- Provider Registry ---
	// Each provider adapter translates the unified OpenAI-compatible API
	// to the specific format of its backend.
	registry := make(map[string]providers.Provider)

	for name, pcfg := range cfg.Providers {
		switch name {
		case "openai":
			registry[name] = openai.New(pcfg.BaseURL, pcfg.APIKey, pcfg.Models)
		case "anthropic":
			registry[name] = anthropic.New(pcfg.BaseURL, pcfg.APIKey, pcfg.Models)
		case "ollama":
			registry[name] = ollama.New(pcfg.BaseURL, pcfg.APIKey, pcfg.Models)
		default:
			slog.Warn("unknown provider type in config", "provider", name)
		}
	}

	// --- Resilience Layer ---
	// Circuit breaker tracks per-provider failure rates and prevents
	// cascade failures by short-circuiting requests to unhealthy providers.
	cb := resilience.NewCircuitBreaker(resilience.CircuitBreakerConfig{
		FailureThreshold: cfg.Resilience.CircuitBreaker.FailureThreshold,
		ResetTimeout:     time.Duration(cfg.Resilience.CircuitBreaker.ResetTimeoutSeconds) * time.Second,
	})

	retryCfg := resilience.RetryConfig{
		MaxRetries:        cfg.Resilience.MaxRetries,
		InitialBackoffMs:  cfg.Resilience.InitialBackoffMs,
		MaxBackoffMs:      cfg.Resilience.MaxBackoffMs,
		BackoffMultiplier: cfg.Resilience.BackoffMultiplier,
	}

	// Set resilience defaults if not configured
	if retryCfg.MaxRetries == 0 {
		retryCfg.MaxRetries = 3
	}
	if retryCfg.InitialBackoffMs == 0 {
		retryCfg.InitialBackoffMs = 500
	}
	if retryCfg.MaxBackoffMs == 0 {
		retryCfg.MaxBackoffMs = 30000
	}
	if retryCfg.BackoffMultiplier == 0 {
		retryCfg.BackoffMultiplier = 2.0
	}

	// Fallback executor iterates through the provider chain with
	// retry + circuit breaker protection at each step.
	fallbackChain := cfg.Routing.FallbackChain
	fallbackExecutor := resilience.NewFallbackExecutor(
		fallbackChain,
		registry,
		cb,
		retryCfg,
		logger,
	)

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	r.Post("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit to prevent OOM

		var req providers.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			providers.WriteErrorResponse(w, http.StatusBadRequest, "invalid JSON payload or payload too large", "invalid_request_error", "")
			return
		}

		// Provider resolution chain:
		// 1. Explicit provider requested in the payload (for testing/debugging)
		// 2. Model map routing (if the requested model is bound to a specific provider)
		// 3. Default provider fallback
		providerName := req.Provider
		if providerName == "" {
			providerName = cfg.Routing.ModelMap[req.Model]
		}
		if providerName == "" {
			providerName = cfg.Routing.DefaultProvider
		}

		slog.Info("processing request",
			"model", req.Model,
			"provider", providerName,
			"stream", req.Stream,
		)

		// Determine if we should use the fallback chain or direct routing.
		// Fallback is used when:
		//   - A fallback chain is configured, AND
		//   - No explicit provider was requested (explicit = bypass resilience)
		useFallback := len(fallbackChain) > 0 && req.Provider == ""

		if req.Stream {
			// For streaming, we route directly to the resolved provider.
			// Fallback doesn't apply to streaming because we can't retry
			// a partially-written SSE stream — the client has already
			// received some chunks.
			p, ok := registry[providerName]
			if !ok {
				providers.WriteErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("provider %q not found", providerName), "invalid_request_error", "")
				return
			}

			chunks, errCh, err := p.ChatStream(r.Context(), &req)
			if err != nil {
				slog.Error("failed to start stream", "error", err)
				providers.WriteErrorResponse(w, http.StatusInternalServerError, err.Error(), "api_error", "")
				return
			}
			err = proxy.StreamToClient(w, chunks, errCh)
			if err != nil {
				slog.Error("stream error", "error", err)
			}
			slog.Info("request completed", "latency_ms", time.Since(start).Milliseconds())
			return
		}

		// Non-streaming: use fallback chain with retry + circuit breaker
		if useFallback {
			var resp *providers.ChatResponse
			err := fallbackExecutor.Execute(r.Context(), func(ctx context.Context, p providers.Provider) error {
				var chatErr error
				resp, chatErr = p.Chat(ctx, &req)
				return chatErr
			})
			if err != nil {
				status := http.StatusInternalServerError
				if perr, ok := err.(*providers.ProviderError); ok {
					status = perr.StatusCode
				}
				slog.Error("all providers failed", "error", err)
				providers.WriteErrorResponse(w, status, err.Error(), "api_error", "")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			slog.Info("request completed", "latency_ms", time.Since(start).Milliseconds())
			return
		}

		// Direct routing (no fallback) — used when provider is explicitly requested
		// or no fallback chain is configured
		p, ok := registry[providerName]
		if !ok {
			providers.WriteErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("provider %q not found", providerName), "invalid_request_error", "")
			return
		}

		resp, err := p.Chat(r.Context(), &req)
		if err != nil {
			status := http.StatusInternalServerError
			if perr, ok := err.(*providers.ProviderError); ok {
				status = perr.StatusCode
			}
			slog.Error("provider error", "error", err)
			providers.WriteErrorResponse(w, status, err.Error(), "api_error", "")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		slog.Info("request completed", "latency_ms", time.Since(start).Milliseconds())
	})

	port := cfg.Server.Port
	if port == 0 {
		port = 8080
	}
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      r,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
	}

	go func() {
		slog.Info("starting server", "port", port, "providers", len(registry), "fallback_chain", fallbackChain)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	slog.Info("shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("shutdown complete")
}
