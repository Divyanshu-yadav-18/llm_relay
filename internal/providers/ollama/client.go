// Package ollama provides the Ollama provider adapter for the LLM Relay gateway.
// Ollama exposes an OpenAI-compatible API at /v1/chat/completions, so this adapter
// is structurally similar to the OpenAI adapter but targets a different base URL path.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

// Client is the Ollama provider adapter.
// It manages communication with a local or remote Ollama instance.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	models     []string
}

// New creates a new Ollama client.
// baseURL is the API endpoint (defaults to http://localhost:11434 if empty).
// apiKey is an optional authentication token.
// models is the list of models supported by this provider instance.
func New(baseURL, apiKey string, models []string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{},
		models:     models,
	}
}

// Name returns the provider name ("ollama").
func (c *Client) Name() string {
	return "ollama"
}

// Models returns the list of models this provider supports.
func (c *Client) Models() []string {
	return c.models
}

// Chat handles a standard (non-streaming) chat completion request.
// It marshals the request, sends it to the provider, handles errors with retryability logic,
// and decodes the final response back to the client format.
func (c *Client) Chat(ctx context.Context, req *providers.ChatRequest) (*providers.ChatResponse, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp providers.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, &providers.ProviderError{
			StatusCode: resp.StatusCode,
			Message:    errResp.Error.Message,
			Retryable:  retryable,
			Provider:   c.Name(),
		}
	}

	var chatResp providers.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &chatResp, nil
}

// ChatStream handles a streaming chat completion request via Server-Sent Events (SSE).
// It spawns a goroutine to read SSE chunks without blocking the caller.
// The error channel is buffered to prevent goroutine leaks if the caller stops reading.
// It checks ctx.Done() at each iteration to halt processing if the client disconnects.
func (c *Client) ChatStream(ctx context.Context, req *providers.ChatRequest) (<-chan providers.StreamChunk, <-chan error, error) {
	req.Stream = true
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("http request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var errResp providers.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, nil, &providers.ProviderError{
			StatusCode: resp.StatusCode,
			Message:    errResp.Error.Message,
			Retryable:  retryable,
			Provider:   c.Name(),
		}
	}

	chunkCh := make(chan providers.StreamChunk)
	errCh := make(chan error, 1)

	go func() {
		defer resp.Body.Close()
		defer close(chunkCh)
		defer close(errCh)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			default:
			}

			line := scanner.Text()
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return
			}

			var chunk providers.StreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				errCh <- fmt.Errorf("failed to unmarshal chunk: %w", err)
				return
			}

			select {
			case chunkCh <- chunk:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}

		if err := scanner.Err(); err != nil {
			errCh <- fmt.Errorf("stream scanner error: %w", err)
		}
	}()

	return chunkCh, errCh, nil
}
