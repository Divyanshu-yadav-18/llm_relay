package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Message represents a chat message within the conversation history.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest represents a chat completion request.
// It matches the OpenAI API format so any client written for OpenAI works unchanged against the gateway.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
	// Provider is an optional field to explicitly select a provider
	Provider    string    `json:"provider,omitempty"`
}

// ChatResponse represents a successful chat completion response.
// It matches the OpenAI API format so any client written for OpenAI works unchanged against the gateway.
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice represents a single generated completion choice.
type Choice struct {
	Index        int      `json:"index"`
	Message      *Message `json:"message,omitempty"`
	Delta        *Message `json:"delta,omitempty"`
	FinishReason *string  `json:"finish_reason,omitempty"`
}

// Usage represents token consumption statistics for a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamChunk represents a single SSE event in a streaming response.
// The gateway never buffers the full response; chunks are forwarded to the client immediately.
type StreamChunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

// ErrorResponse matches the OpenAI error format, ensuring clients receive consistent errors.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains the structured details of an API error.
type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// ProviderError wraps errors from underlying providers with HTTP status and retryability info.
// The Retryable field is used by the resilience layer to decide whether to retry or fail immediately (4xx = don't retry, 429/5xx = retry).
type ProviderError struct {
	StatusCode int
	Message    string
	Retryable  bool
	Provider   string
}

// Error implements the error interface, providing a formatted string of the provider error.
func (e *ProviderError) Error() string {
	return fmt.Sprintf("provider %s returned status %d: %s", e.Provider, e.StatusCode, e.Message)
}

// Provider is the abstraction boundary for all LLM backends.
// Every LLM backend (OpenAI, Anthropic, Ollama) implements this, so the gateway logic never knows which provider it's talking to.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, <-chan error, error)
	Models() []string
}

// NewErrorResponse creates an OpenAI-compatible error response struct.
func NewErrorResponse(status int, message, errType, code string) (int, ErrorResponse) {
	return status, ErrorResponse{
		Error: ErrorDetail{
			Message: message,
			Type:    errType,
			Code:    code,
		},
	}
}

// WriteErrorResponse writes an OpenAI-compatible error to the response writer.
// This ensures all gateway errors look like OpenAI errors, so clients don't need special error handling.
func WriteErrorResponse(w http.ResponseWriter, status int, message, errType, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, resp := NewErrorResponse(status, message, errType, code)
	_ = json.NewEncoder(w).Encode(resp)
}
