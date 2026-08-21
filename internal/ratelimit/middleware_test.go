package ratelimit

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestMiddleware_Allowed(t *testing.T) {
	mr, client := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	limiter := NewTokenBucket(client, 60, 5)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Middleware(limiter, logger)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	server := httptest.NewServer(handler)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	// We want consistent IP for key extraction
	req.Header.Set("X-Forwarded-For", "192.168.1.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	limitHeader := resp.Header.Get("X-RateLimit-Limit")
	if limitHeader != "60" {
		t.Errorf("expected limit 60, got %s", limitHeader)
	}

	remainingHeader := resp.Header.Get("X-RateLimit-Remaining")
	if remainingHeader != "4" {
		t.Errorf("expected remaining 4, got %s", remainingHeader)
	}

	if resp.Header.Get("X-RateLimit-Reset") == "" {
		t.Error("expected reset header to be set")
	}
}

func TestMiddleware_Denied(t *testing.T) {
	mr, client := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	limiter := NewTokenBucket(client, 60, 1) // burst 1
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Middleware(limiter, logger)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	server := httptest.NewServer(handler)
	defer server.Close()

	// First request - allowed
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.2")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected first request to be OK, got %d", resp.StatusCode)
	}

	// Second request - denied
	req2, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	req2.Header.Set("X-Forwarded-For", "192.168.1.2")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", resp2.StatusCode)
	}

	if resp2.Header.Get("Retry-After") == "" {
		t.Error("expected Retry-After header to be set")
	}

	body, _ := io.ReadAll(resp2.Body)
	if !bytes.Contains(body, []byte("rate_limit_exceeded")) {
		t.Errorf("expected error body to contain 'rate_limit_exceeded', got %s", string(body))
	}
}

func TestMiddleware_FailOpen(t *testing.T) {
	// Use an invalid redis address to simulate downtime
	client := redis.NewClient(&redis.Options{Addr: "localhost:12345", DialTimeout: 10 * time.Millisecond})
	defer client.Close()

	limiter := NewTokenBucket(client, 60, 5)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := Middleware(limiter, logger)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	server := httptest.NewServer(handler)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	// Should pass through with 200 OK
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK despite redis error, got %d", resp.StatusCode)
	}
}
