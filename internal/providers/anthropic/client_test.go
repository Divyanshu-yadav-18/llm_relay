package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

func TestClient_Name(t *testing.T) {
	c := New("", "", nil)
	if got := c.Name(); got != "anthropic" {
		t.Errorf("Name() = %v, want anthropic", got)
	}
}

func TestClient_Chat(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("Expected x-api-key: test-key")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("Expected anthropic-version: 2023-06-01")
		}

		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}

		if req.MaxTokens != 1024 {
			t.Errorf("Expected max_tokens 1024, got %v", req.MaxTokens)
		}
		if req.System != "System message" {
			t.Errorf("Expected system 'System message', got %v", req.System)
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "User message" {
			t.Errorf("Expected 1 user message, got %v", req.Messages)
		}

		resp := anthropicResponse{
			ID:    "msg_123",
			Type:  "message",
			Role:  "assistant",
			Model: req.Model,
			Content: []anthropicContent{
				{Type: "text", Text: "Hello!"},
			},
			StopReason: "end_turn",
			Usage: anthropicUsage{
				InputTokens:  10,
				OutputTokens: 5,
			},
		}

		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	c := New(ts.URL, "test-key", []string{"claude-3-5-sonnet-20240620"})
	maxToks := 1024
	req := &providers.ChatRequest{
		Model: "claude-3-5-sonnet-20240620",
		Messages: []providers.Message{
			{Role: "system", Content: "System message"},
			{Role: "user", Content: "User message"},
		},
		MaxTokens: &maxToks,
	}

	resp, err := c.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if resp.ID != "msg_123" {
		t.Errorf("Expected ID msg_123, got %v", resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Expected 1 choice, got %v", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("Expected content Hello!, got %v", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("Expected total tokens 15, got %v", resp.Usage.TotalTokens)
	}
}

func TestClient_ChatStream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		
		fmt.Fprintf(w, "event: message_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-3-5-sonnet-20240620\"}}\n\n")

		fmt.Fprintf(w, "event: content_block_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")

		fmt.Fprintf(w, "event: content_block_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n")

		fmt.Fprintf(w, "event: message_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":15}}\n\n")

		fmt.Fprintf(w, "event: message_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer ts.Close()

	c := New(ts.URL, "test-key", []string{"claude-3-5-sonnet-20240620"})
	req := &providers.ChatRequest{
		Model: "claude-3-5-sonnet-20240620",
		Messages: []providers.Message{
			{Role: "user", Content: "Hi"},
		},
	}

	chunks, errs, err := c.ChatStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	var results []providers.StreamChunk
	for chunk := range chunks {
		results = append(results, chunk)
	}

	if err := <-errs; err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("Expected 3 chunks, got %v", len(results))
	}

	if results[0].Choices[0].Delta.Role != "assistant" {
		t.Errorf("Expected role assistant, got %v", results[0].Choices[0].Delta.Role)
	}
	if results[1].Choices[0].Delta.Content != "Hello" {
		t.Errorf("Expected content Hello, got %v", results[1].Choices[0].Delta.Content)
	}
	if *results[2].Choices[0].FinishReason != "stop" {
		t.Errorf("Expected finish reason stop, got %v", *results[2].Choices[0].FinishReason)
	}
}

func TestClient_Errors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		retryable  bool
	}{
		{"Bad Request", http.StatusBadRequest, false},
		{"Too Many Requests", http.StatusTooManyRequests, true},
		{"Internal Server Error", http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				json.NewEncoder(w).Encode(anthropicErrorResponse{})
			}))
			defer ts.Close()

			c := New(ts.URL, "test-key", nil)
			req := &providers.ChatRequest{Model: "claude"}

			_, err := c.Chat(context.Background(), req)
			
			pErr, ok := err.(*providers.ProviderError)
			if !ok {
				t.Fatalf("Expected ProviderError, got %T", err)
			}
			if pErr.StatusCode != tt.status {
				t.Errorf("Expected status %v, got %v", tt.status, pErr.StatusCode)
			}
			if pErr.Retryable != tt.retryable {
				t.Errorf("Expected retryable %v, got %v", tt.retryable, pErr.Retryable)
			}
		})
	}
}
