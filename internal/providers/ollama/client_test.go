package ollama

import (
	"context"
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
	t.Run("Name() returns ollama", func(t *testing.T) {
		t.Helper()
		c := New("", "", nil)
		if c.Name() != "ollama" {
			t.Errorf("expected ollama, got %s", c.Name())
		}
	})

	t.Run("Default base URL is applied", func(t *testing.T) {
		t.Helper()
		c := New("", "", nil)
		if c.baseURL != "http://localhost:11434" {
			t.Errorf("expected default base URL, got %s", c.baseURL)
		}
	})

	t.Run("Models() returns configured models", func(t *testing.T) {
		t.Helper()
		models := []string{"llama3"}
		c := New("", "", models)
		got := c.Models()
		if len(got) != 1 || got[0] != "llama3" {
			t.Errorf("expected %v, got %v", models, got)
		}
	})

	t.Run("Chat request goes to correct path and authorization is handled", func(t *testing.T) {
		t.Helper()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/chat/completions" {
				t.Errorf("expected /v1/chat/completions, got %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "" {
				t.Errorf("expected no authorization header when empty")
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id": "1", "object": "chat.completion", "model": "llama3"}`))
		}))
		defer ts.Close()

		c := New(ts.URL, "", nil)
		req := &providers.ChatRequest{Model: "llama3"}
		resp, err := c.Chat(context.Background(), req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if resp.ID != "1" {
			t.Errorf("expected id 1, got %s", resp.ID)
		}
	})
	
	t.Run("Chat request with API key adds Authorization header", func(t *testing.T) {
		t.Helper()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth := r.Header.Get("Authorization"); auth != "Bearer secret" {
				t.Errorf("expected Bearer secret, got %s", auth)
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id": "1", "object": "chat.completion", "model": "llama3"}`))
		}))
		defer ts.Close()

		c := New(ts.URL, "secret", nil)
		req := &providers.ChatRequest{Model: "llama3"}
		_, err := c.Chat(context.Background(), req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
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
		req := &providers.ChatRequest{Model: "llama3"}
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

	t.Run("Successful ChatStream returns chunks", func(t *testing.T) {
		t.Helper()
		chunks := []string{
			`{"id":"1","object":"chunk","choices":[{"delta":{"content":"Hi"}}]}`,
		}
		ts := httptest.NewServer(mockSSEHandler(chunks))
		defer ts.Close()

		c := New(ts.URL, "", nil)
		req := &providers.ChatRequest{Model: "llama3"}
		chunkCh, errCh, err := c.ChatStream(context.Background(), req)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		var received []providers.StreamChunk
		for chunk := range chunkCh {
			received = append(received, chunk)
		}

		if len(received) != 1 {
			t.Fatalf("expected 1 chunk, got %d", len(received))
		}

		streamErr := <-errCh
		if streamErr != nil {
			t.Errorf("expected no stream error, got %v", streamErr)
		}
	})
}
