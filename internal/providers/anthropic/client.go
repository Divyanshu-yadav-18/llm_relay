// Package anthropic provides the Anthropic provider adapter for the LLM Relay gateway.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

// Client is the Anthropic provider adapter.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	models     []string
}

// New creates a new Anthropic client.
func New(baseURL, apiKey string, models []string) *Client {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		models:     models,
	}
}

// Name returns the provider name ("anthropic").
func (c *Client) Name() string {
	return "anthropic"
}

// Models returns the list of models this provider supports.
func (c *Client) Models() []string {
	return c.models
}

type anthropicRequest struct {
	Model       string               `json:"model"`
	MaxTokens   int                  `json:"max_tokens"`
	Messages    []anthropicMessage   `json:"messages"`
	System      string               `json:"system,omitempty"`
	Temperature *float64             `json:"temperature,omitempty"`
	TopP        *float64             `json:"top_p,omitempty"`
	Stop        []string             `json:"stop_sequences,omitempty"`
	Stream      bool                 `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Role       string             `json:"role"`
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      anthropicUsage     `json:"usage"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// toAnthropicRequest converts OpenAI ChatRequest to Anthropic format.
func toAnthropicRequest(req *providers.ChatRequest) *anthropicRequest {
	aReq := &anthropicRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
		Stream:      req.Stream,
	}

	if req.MaxTokens != nil {
		aReq.MaxTokens = *req.MaxTokens
	} else {
		aReq.MaxTokens = 4096
	}

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			aReq.System = msg.Content
		} else {
			aReq.Messages = append(aReq.Messages, anthropicMessage{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
	}

	return aReq
}

func finishReasonMap(stopReason string) *string {
	var reason string
	switch stopReason {
	case "end_turn":
		reason = "stop"
	case "max_tokens":
		reason = "length"
	case "stop_sequence":
		reason = "stop"
	default:
		reason = stopReason
	}
	return &reason
}

// Chat handles a standard chat completion request.
func (c *Client) Chat(ctx context.Context, req *providers.ChatRequest) (*providers.ChatResponse, error) {
	aReq := toAnthropicRequest(req)
	bodyBytes, err := json.Marshal(aReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if c.apiKey != "" {
		httpReq.Header.Set("x-api-key", c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp anthropicErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, &providers.ProviderError{
			StatusCode: resp.StatusCode,
			Message:    errResp.Error.Message,
			Retryable:  retryable,
			Provider:   c.Name(),
		}
	}

	var aResp anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&aResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	text := ""
	for _, c := range aResp.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}

	chatResp := &providers.ChatResponse{
		ID:      aResp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   aResp.Model,
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.Message{
					Role:    "assistant",
					Content: text,
				},
				FinishReason: finishReasonMap(aResp.StopReason),
			},
		},
		Usage: &providers.Usage{
			PromptTokens:     aResp.Usage.InputTokens,
			CompletionTokens: aResp.Usage.OutputTokens,
			TotalTokens:      aResp.Usage.InputTokens + aResp.Usage.OutputTokens,
		},
	}

	return chatResp, nil
}

type anthropicStreamEvent struct {
	Type         string `json:"type"`
	Message      *anthropicResponse `json:"message,omitempty"`
	Index        int `json:"index,omitempty"`
	ContentBlock *anthropicContent `json:"content_block,omitempty"`
	Delta        *anthropicDelta `json:"delta,omitempty"`
	Usage        *anthropicUsage `json:"usage,omitempty"`
}

type anthropicDelta struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// ChatStream handles a streaming chat completion request.
func (c *Client) ChatStream(ctx context.Context, req *providers.ChatRequest) (<-chan providers.StreamChunk, <-chan error, error) {
	req.Stream = true
	aReq := toAnthropicRequest(req)
	bodyBytes, err := json.Marshal(aReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if c.apiKey != "" {
		httpReq.Header.Set("x-api-key", c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("http request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var errResp anthropicErrorResponse
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
		var msgID string
		var model string

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

			if strings.HasPrefix(line, "event: ") {
				// We can ignore the event line, the data line has the info
				continue
			}

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			var event anthropicStreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				errCh <- fmt.Errorf("failed to unmarshal chunk: %w", err)
				return
			}

			switch event.Type {
			case "message_start":
				if event.Message != nil {
					msgID = event.Message.ID
					model = event.Message.Model
				}
				chunkCh <- providers.StreamChunk{
					ID:      msgID,
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   model,
					Choices: []providers.Choice{
						{
							Index: 0,
							Delta: &providers.Message{
								Role: "assistant",
							},
						},
					},
				}
			case "content_block_delta":
				if event.Delta != nil && event.Delta.Type == "text_delta" {
					chunkCh <- providers.StreamChunk{
						ID:      msgID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   model,
						Choices: []providers.Choice{
							{
								Index: 0,
								Delta: &providers.Message{
									Content: event.Delta.Text,
								},
							},
						},
					}
				}
			case "message_delta":
				if event.Delta != nil && event.Delta.StopReason != "" {
					chunkCh <- providers.StreamChunk{
						ID:      msgID,
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Model:   model,
						Choices: []providers.Choice{
							{
								Index:        0,
								Delta:        &providers.Message{},
								FinishReason: finishReasonMap(event.Delta.StopReason),
							},
						},
					}
				}
			case "message_stop":
				return
			}
		}

		if err := scanner.Err(); err != nil {
			errCh <- fmt.Errorf("stream scanner error: %w", err)
		}
	}()

	return chunkCh, errCh, nil
}
