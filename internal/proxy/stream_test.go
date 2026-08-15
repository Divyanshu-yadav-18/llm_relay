package proxy

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

// mockResponseWriter implements http.ResponseWriter and http.Flusher
type mockResponseWriter struct {
	*httptest.ResponseRecorder
}

func (m *mockResponseWriter) Flush() {
	m.ResponseRecorder.Flush()
}

func TestStreamToClient(t *testing.T) {
	t.Run("Successful stream writes correct SSE format", func(t *testing.T) {
		t.Helper()
		chunks := make(chan providers.StreamChunk, 2)
		errCh := make(chan error, 1)

		chunks <- providers.StreamChunk{ID: "1"}
		chunks <- providers.StreamChunk{ID: "2"}
		close(chunks)
		close(errCh)

		rec := httptest.NewRecorder()
		w := &mockResponseWriter{ResponseRecorder: rec}

		err := StreamToClient(w, chunks, errCh)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		body := rec.Body.String()
		if !strings.Contains(body, "data: {\"id\":\"1\"") {
			t.Errorf("missing chunk 1")
		}
		if !strings.Contains(body, "data: {\"id\":\"2\"") {
			t.Errorf("missing chunk 2")
		}
		if !strings.HasSuffix(body, "data: [DONE]\n\n") {
			t.Errorf("missing or incorrect [DONE] suffix: %q", body)
		}
		
		if rec.Header().Get("Content-Type") != "text/event-stream" {
			t.Errorf("incorrect Content-Type")
		}
	})

	t.Run("Empty chunk channel produces only [DONE]", func(t *testing.T) {
		t.Helper()
		chunks := make(chan providers.StreamChunk)
		errCh := make(chan error, 1)

		close(chunks)
		close(errCh)

		rec := httptest.NewRecorder()
		w := &mockResponseWriter{ResponseRecorder: rec}

		err := StreamToClient(w, chunks, errCh)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		body := rec.Body.String()
		if body != "data: [DONE]\n\n" {
			t.Errorf("expected only [DONE], got %q", body)
		}
	})

	t.Run("Error on errCh is returned after streaming", func(t *testing.T) {
		t.Helper()
		chunks := make(chan providers.StreamChunk)
		errCh := make(chan error, 1)

		close(chunks)
		
		expectedErr := errors.New("stream failed")
		errCh <- expectedErr
		close(errCh)

		rec := httptest.NewRecorder()
		w := &mockResponseWriter{ResponseRecorder: rec}

		err := StreamToClient(w, chunks, errCh)
		if err != expectedErr {
			t.Errorf("expected %v, got %v", expectedErr, err)
		}
	})

	t.Run("ResponseWriter without Flusher returns error", func(t *testing.T) {
		t.Helper()
		chunks := make(chan providers.StreamChunk)
		errCh := make(chan error, 1)

		// httptest.ResponseRecorder does NOT implement Flusher by default
		// unless we wrap it like we did in mockResponseWriter
		rec := httptest.NewRecorder()

		err := StreamToClient(rec, chunks, errCh)
		if err == nil {
			t.Fatalf("expected error for missing Flusher, got nil")
		}
		if !strings.Contains(err.Error(), "streaming unsupported") {
			t.Errorf("expected streaming unsupported error, got %v", err)
		}
	})
}
