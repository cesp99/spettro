package provider

import (
	"context"
	"fmt"
	"strings"

	"spettro/internal/budget"
)

// SDK dependencies required by spec:
// - github.com/openai/openai-go/v3
// - github.com/anthropics/anthropic-sdk-go
import (
	_ "github.com/anthropics/anthropic-sdk-go"
	_ "github.com/openai/openai-go/v3"
)

type Model struct {
	Provider string
	Name     string
	Vision   bool
}

type Request struct {
	Prompt      string
	Images      []string
	RequireFast bool
}

type Response struct {
	Content         string
	EstimatedTokens int
	Provider        string
	Model           string
}

type Adapter interface {
	Send(context.Context, string, Request) (Response, error)
}

type Manager struct {
	adapters map[string]Adapter
	models   []Model
}

func NewManager() *Manager {
	return &Manager{
		adapters: map[string]Adapter{
			"openai-compatible": OpenAICompatibleAdapter{},
			"anthropic":         AnthropicAdapter{},
		},
		models: []Model{
			{Provider: "openai-compatible", Name: "gpt-5-mini", Vision: true},
			{Provider: "openai-compatible", Name: "gpt-4.1", Vision: true},
			{Provider: "openai-compatible", Name: "qwen-coder-plus", Vision: false},
			{Provider: "anthropic", Name: "claude-3-7-sonnet", Vision: true},
		},
	}
}

func (m *Manager) Models() []Model {
	return append([]Model(nil), m.models...)
}

func (m *Manager) SupportsVision(providerName, model string) bool {
	for _, item := range m.models {
		if item.Provider == providerName && item.Name == model {
			return item.Vision
		}
	}
	return false
}

func (m *Manager) HasModel(providerName, model string) bool {
	for _, item := range m.models {
		if item.Provider == providerName && item.Name == model {
			return true
		}
	}
	return false
}

func (m *Manager) Send(ctx context.Context, providerName, model string, req Request) (Response, error) {
	adapter, ok := m.adapters[providerName]
	if !ok {
		return Response{}, fmt.Errorf("unsupported provider: %s", providerName)
	}

	if len(req.Images) > 0 && !m.SupportsVision(providerName, model) {
		return Response{}, fmt.Errorf("model does not support vision: %s/%s", providerName, model)
	}

	allParts := []string{req.Prompt}
	allParts = append(allParts, req.Images...)
	if err := budget.Validate(allParts...); err != nil {
		return Response{}, err
	}

	resp, err := adapter.Send(ctx, model, req)
	if err != nil {
		return Response{}, err
	}
	resp.EstimatedTokens = budget.EstimateTokens(allParts...)
	resp.Provider = providerName
	resp.Model = model
	return resp, nil
}

type OpenAICompatibleAdapter struct{}

func (OpenAICompatibleAdapter) Send(_ context.Context, model string, req Request) (Response, error) {
	return Response{
		Content: fmt.Sprintf("[openai-compatible/%s] %s", model, summarize(req.Prompt)),
	}, nil
}

type AnthropicAdapter struct{}

func (AnthropicAdapter) Send(_ context.Context, model string, req Request) (Response, error) {
	return Response{
		Content: fmt.Sprintf("[anthropic/%s] %s", model, summarize(req.Prompt)),
	}, nil
}

func summarize(s string) string {
	const max = 160
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
