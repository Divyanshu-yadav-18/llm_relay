package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

func mockSSEHandler(chunks []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func TestClient(t *testing.T) {
	t.Run("Name() returns openai", func(t *testing.T) {
		t.Helper()
		c := New("", "", nil)
		if c.Name() != "openai" {
			t.Errorf("expected openai, got %s", c.Name())
		}
	})

	t.Run("Models() returns configured models", func(t *testing.T) {
		t.Helper()
		models := []string{"gpt-4", "gpt-3.5-turbo"}
		c := New("", "", models)
		got := c.Models()
		if len(got) != 2 || got[0] != "gpt-4" || got[1] != "gpt-3.5-turbo" {
			t.Errorf("expected %v, got %v", models, got)
		}
	})

	t.Run("Successful Chat request returns parsed response", func(t *testing.T) {
		t.Helper()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id": "chatcmpl-123", "object": "chat.completion", "model": "gpt-4"}`))
		}))
		defer ts.Close()

		c := New(ts.URL, "test_key", nil)
		req := &providers.ChatRequest{Model: "gpt-4"}
		resp, err := c.Chat(context.Background(), req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if resp.ID != "chatcmpl-123" {
			t.Errorf("expected id chatcmpl-123, got %s", resp.ID)
		}
	})

	t.Run("Chat with 400 error returns non-retryable ProviderError", func(t *testing.T) {
		t.Helper()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": {"message": "bad request"}}`))
		}))
		defer ts.Close()

		c := New(ts.URL, "", nil)
		req := &providers.ChatRequest{Model: "gpt-4"}
		_, err := c.Chat(context.Background(), req)
		
		perr, ok := err.(*providers.ProviderError)
		if !ok {
			t.Fatalf("expected ProviderError, got %T", err)
		}
		if perr.StatusCode != 400 {
			t.Errorf("expected status 400, got %d", perr.StatusCode)
		}
		if perr.Retryable {
			t.Errorf("expected non-retryable error")
		}
	})

	t.Run("Chat with 429 error returns retryable ProviderError", func(t *testing.T) {
		t.Helper()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": {"message": "rate limited"}}`))
		}))
		defer ts.Close()

		c := New(ts.URL, "", nil)
		req := &providers.ChatRequest{Model: "gpt-4"}
		_, err := c.Chat(context.Background(), req)
		
		perr, ok := err.(*providers.ProviderError)
		if !ok {
			t.Fatalf("expected ProviderError, got %T", err)
		}
		if perr.StatusCode != 429 {
			t.Errorf("expected status 429, got %d", perr.StatusCode)
		}
		if !perr.Retryable {
			t.Errorf("expected retryable error")
		}
	})

	t.Run("Chat with 500 error returns retryable ProviderError", func(t *testing.T) {
		t.Helper()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": {"message": "server error"}}`))
		}))
		defer ts.Close()

		c := New(ts.URL, "", nil)
		req := &providers.ChatRequest{Model: "gpt-4"}
		_, err := c.Chat(context.Background(), req)
		
		perr, ok := err.(*providers.ProviderError)
		if !ok {
			t.Fatalf("expected ProviderError, got %T", err)
		}
		if perr.StatusCode != 500 {
			t.Errorf("expected status 500, got %d", perr.StatusCode)
		}
		if !perr.Retryable {
			t.Errorf("expected retryable error")
		}
	})

	t.Run("Successful ChatStream returns chunks in order through channel", func(t *testing.T) {
		t.Helper()
		chunks := []string{
			`{"id":"1","object":"chunk","choices":[{"delta":{"content":"Hello"}}]}`,
			`{"id":"2","object":"chunk","choices":[{"delta":{"content":" World"}}]}`,
		}
		ts := httptest.NewServer(mockSSEHandler(chunks))
		defer ts.Close()

		c := New(ts.URL, "", nil)
		req := &providers.ChatRequest{Model: "gpt-4"}
		chunkCh, errCh, err := c.ChatStream(context.Background(), req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		var received []providers.StreamChunk
		for chunk := range chunkCh {
			received = append(received, chunk)
		}

		if len(received) != 2 {
			t.Fatalf("expected 2 chunks, got %d", len(received))
		}
		if received[0].ID != "1" || received[1].ID != "2" {
			t.Errorf("chunks out of order or incorrect")
		}

		streamErr := <-errCh
		if streamErr != nil {
			t.Errorf("expected no stream error, got %v", streamErr)
		}
	})

	t.Run("ChatStream with [DONE] terminates cleanly", func(t *testing.T) {
		t.Helper()
		ts := httptest.NewServer(mockSSEHandler(nil))
		defer ts.Close()

		c := New(ts.URL, "", nil)
		req := &providers.ChatRequest{Model: "gpt-4"}
		chunkCh, errCh, err := c.ChatStream(context.Background(), req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		for range chunkCh {
			// consume
		}

		streamErr := <-errCh
		if streamErr != nil {
			t.Errorf("expected no stream error, got %v", streamErr)
		}
	})

	t.Run("ChatStream with context cancellation stops reading", func(t *testing.T) {
		t.Helper()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			fmt.Fprintf(w, "data: {\"id\":\"1\"}\n\n")
			flusher.Flush()
			// hang forever
			<-r.Context().Done()
		}))
		defer ts.Close()

		c := New(ts.URL, "", nil)
		req := &providers.ChatRequest{Model: "gpt-4"}
		ctx, cancel := context.WithCancel(context.Background())
		
		chunkCh, errCh, err := c.ChatStream(ctx, req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// wait for first chunk
		<-chunkCh

		// cancel context
		cancel()

		// wait for error
		streamErr := <-errCh
		if streamErr == nil || !errors.Is(streamErr, context.Canceled) {
			t.Errorf("expected context.Canceled error (possibly wrapped), got %v", streamErr)
		}
	})
}
