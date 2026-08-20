package resilience

import (
	"context"
	"github.com/Divyanshu-yadav-18/llm_relay/internal/providers"
)

type MockProvider struct {
	NameStr        string
	ChatFunc       func(ctx context.Context, req *providers.ChatRequest) (*providers.ChatResponse, error)
	ChatStreamFunc func(ctx context.Context, req *providers.ChatRequest) (<-chan providers.StreamChunk, <-chan error, error)
	ModelsFunc     func() []string
}

func (m *MockProvider) Name() string {
	return m.NameStr
}

func (m *MockProvider) Chat(ctx context.Context, req *providers.ChatRequest) (*providers.ChatResponse, error) {
	if m.ChatFunc != nil {
		return m.ChatFunc(ctx, req)
	}
	return nil, nil
}

func (m *MockProvider) ChatStream(ctx context.Context, req *providers.ChatRequest) (<-chan providers.StreamChunk, <-chan error, error) {
	if m.ChatStreamFunc != nil {
		return m.ChatStreamFunc(ctx, req)
	}
	return nil, nil, nil
}

func (m *MockProvider) Models() []string {
	if m.ModelsFunc != nil {
		return m.ModelsFunc()
	}
	return nil
}
