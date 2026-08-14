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
	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers/ollama"
	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers/openai"
	"github.com/Divyanshu-yadav-18/llm_relay/internal/proxy"
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
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	registry := make(map[string]providers.Provider)

	for name, pcfg := range cfg.Providers {
		switch name {
		case "openai":
			registry[name] = openai.New(pcfg.BaseURL, pcfg.APIKey, pcfg.Models)
		case "ollama":
			registry[name] = ollama.New(pcfg.BaseURL, pcfg.APIKey, pcfg.Models)
		default:
			slog.Warn("unknown provider type in config", "provider", name)
		}
	}

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
		var req providers.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			providers.WriteErrorResponse(w, http.StatusBadRequest, "invalid JSON payload", "invalid_request_error", "")
			return
		}

		providerName := req.Provider
		if providerName == "" {
			providerName = cfg.Routing.ModelMap[req.Model]
		}
		if providerName == "" {
			providerName = cfg.Routing.DefaultProvider
		}

		p, ok := registry[providerName]
		if !ok {
			providers.WriteErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("provider %q not found", providerName), "invalid_request_error", "")
			return
		}

		slog.Info("processing request",
			"model", req.Model,
			"provider", providerName,
			"stream", req.Stream,
		)

		if req.Stream {
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
		slog.Info("starting server", "port", port)
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
