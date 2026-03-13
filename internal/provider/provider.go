package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"spettro/internal/budget"
	"spettro/internal/models"
)

// SDK dependencies required by spec:
// - github.com/openai/openai-go/v3
// - github.com/anthropics/anthropic-sdk-go
import (
	_ "github.com/anthropics/anthropic-sdk-go"
	_ "github.com/openai/openai-go/v3"
)

// Model is a provider+model pair enriched with metadata from models.dev.
type Model struct {
	Provider     string // provider ID, e.g. "anthropic"
	ProviderName string // display name, e.g. "Anthropic"
	Name         string // model ID, e.g. "claude-opus-4"
	DisplayName  string // human name, e.g. "Claude Opus 4"
	Vision       bool
	Reasoning    bool
	ToolCall     bool
	Context      int    // max context tokens (0 = unknown)
	Status       string // "" | "alpha" | "beta" | "deprecated"
	EnvKey       string // primary env var, e.g. "ANTHROPIC_API_KEY"
}

// Tag returns a compact badge string for the model selector UI.
func (m Model) Tag() string {
	var parts []string
	if m.Vision {
		parts = append(parts, "img")
	}
	if m.Reasoning {
		parts = append(parts, "think")
	}
	if m.Status != "" {
		parts = append(parts, m.Status)
	}
	if m.Context > 0 {
		switch {
		case m.Context >= 1_000_000:
			parts = append(parts, fmt.Sprintf("%dM ctx", m.Context/1_000_000))
		case m.Context >= 1_000:
			parts = append(parts, fmt.Sprintf("%dk ctx", m.Context/1_000))
		default:
			parts = append(parts, fmt.Sprintf("%d ctx", m.Context))
		}
	}
	return strings.Join(parts, "  ")
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

// fallbackModels are used when models.dev has not been loaded yet.
var fallbackModels = []Model{
	{Provider: "anthropic", ProviderName: "Anthropic", Name: "claude-opus-4", DisplayName: "Claude Opus 4", Vision: true, Reasoning: true, ToolCall: true, EnvKey: "ANTHROPIC_API_KEY"},
	{Provider: "anthropic", ProviderName: "Anthropic", Name: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", Vision: true, Reasoning: true, ToolCall: true, EnvKey: "ANTHROPIC_API_KEY"},
	{Provider: "anthropic", ProviderName: "Anthropic", Name: "claude-3-7-sonnet", DisplayName: "Claude 3.7 Sonnet", Vision: true, Reasoning: true, ToolCall: true, EnvKey: "ANTHROPIC_API_KEY"},
	{Provider: "openai", ProviderName: "OpenAI", Name: "gpt-4.1", DisplayName: "GPT-4.1", Vision: true, ToolCall: true, EnvKey: "OPENAI_API_KEY"},
	{Provider: "openai", ProviderName: "OpenAI", Name: "gpt-4o", DisplayName: "GPT-4o", Vision: true, ToolCall: true, EnvKey: "OPENAI_API_KEY"},
	{Provider: "openai", ProviderName: "OpenAI", Name: "o3", DisplayName: "o3", Vision: true, Reasoning: true, ToolCall: true, EnvKey: "OPENAI_API_KEY"},
}

type Manager struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
	catalog  []Model // populated from models.dev; nil = not loaded yet
}

func NewManager() *Manager {
	return &Manager{
		adapters: map[string]Adapter{
			"anthropic": AnthropicAdapter{},
			// All other providers use the OpenAI-compatible adapter.
		},
	}
}

// SetCatalog replaces the model list with data from a models.dev catalog.
// It is safe to call from any goroutine.
func (m *Manager) SetCatalog(cat models.Catalog) {
	built := buildModels(cat)
	m.mu.Lock()
	m.catalog = built
	m.mu.Unlock()
}

// Models returns the full model list (catalog if loaded, else fallback).
func (m *Manager) Models() []Model {
	m.mu.RLock()
	cat := m.catalog
	m.mu.RUnlock()
	if len(cat) > 0 {
		out := make([]Model, len(cat))
		copy(out, cat)
		return out
	}
	return append([]Model(nil), fallbackModels...)
}

// ProviderEnvKey returns the primary env var name for a provider.
func (m *Manager) ProviderEnvKey(providerID string) string {
	for _, mod := range m.Models() {
		if mod.Provider == providerID && mod.EnvKey != "" {
			return mod.EnvKey
		}
	}
	return ""
}

// ProviderNames returns the deduplicated list of provider IDs present in the
// current model list, in alphabetical order (anthropic first).
func (m *Manager) ProviderNames() []string {
	seen := map[string]bool{}
	for _, mod := range m.Models() {
		seen[mod.Provider] = true
	}
	names := make([]string, 0, len(seen))
	for k := range seen {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		// anthropic first, then alphabetical
		if names[i] == "anthropic" {
			return true
		}
		if names[j] == "anthropic" {
			return false
		}
		return names[i] < names[j]
	})
	return names
}

func (m *Manager) SupportsVision(providerName, modelName string) bool {
	for _, item := range m.Models() {
		if item.Provider == providerName && item.Name == modelName {
			return item.Vision
		}
	}
	return false
}

func (m *Manager) HasModel(providerName, modelName string) bool {
	for _, item := range m.Models() {
		if item.Provider == providerName && item.Name == modelName {
			return true
		}
	}
	return false
}

func (m *Manager) Send(ctx context.Context, providerName, modelName string, req Request) (Response, error) {
	adapter, ok := m.adapters[providerName]
	if !ok {
		// Fall back to the OpenAI-compatible adapter for unknown providers.
		adapter = OpenAICompatibleAdapter{}
	}

	if len(req.Images) > 0 && !m.SupportsVision(providerName, modelName) {
		return Response{}, fmt.Errorf("model does not support vision: %s/%s", providerName, modelName)
	}

	allParts := []string{req.Prompt}
	allParts = append(allParts, req.Images...)
	if err := budget.Validate(allParts...); err != nil {
		return Response{}, err
	}

	resp, err := adapter.Send(ctx, modelName, req)
	if err != nil {
		return Response{}, err
	}
	resp.EstimatedTokens = budget.EstimateTokens(allParts...)
	resp.Provider = providerName
	resp.Model = modelName
	return resp, nil
}

// buildModels converts a models.dev catalog into our Model slice.
// Providers are sorted alphabetically (anthropic first); within each provider
// models are sorted alphabetically by ID, deprecated ones last.
func buildModels(cat models.Catalog) []Model {
	// Sort providers: anthropic first, then alphabetical.
	providerIDs := make([]string, 0, len(cat))
	for id := range cat {
		providerIDs = append(providerIDs, id)
	}
	sort.Slice(providerIDs, func(i, j int) bool {
		if providerIDs[i] == "anthropic" {
			return true
		}
		if providerIDs[j] == "anthropic" {
			return false
		}
		return providerIDs[i] < providerIDs[j]
	})

	var out []Model
	for _, pid := range providerIDs {
		prov := cat[pid]

		// Sort models within provider: non-deprecated first, then alpha.
		modelIDs := make([]string, 0, len(prov.Models))
		for id := range prov.Models {
			modelIDs = append(modelIDs, id)
		}
		sort.Slice(modelIDs, func(i, j int) bool {
			mi, mj := prov.Models[modelIDs[i]], prov.Models[modelIDs[j]]
			di := mi.Status == "deprecated"
			dj := mj.Status == "deprecated"
			if di != dj {
				return !di // non-deprecated first
			}
			return modelIDs[i] < modelIDs[j]
		})

		envKey := ""
		if len(prov.Env) > 0 {
			envKey = prov.Env[0]
		}

		for _, mid := range modelIDs {
			mod := prov.Models[mid]
			ctx := 0
			if mod.Limit != nil {
				ctx = mod.Limit.Context
			}
			out = append(out, Model{
				Provider:     pid,
				ProviderName: prov.Name,
				Name:         mid,
				DisplayName:  mod.Name,
				Vision:       mod.SupportsImage(),
				Reasoning:    mod.Reasoning,
				ToolCall:     mod.ToolCall,
				Context:      ctx,
				Status:       mod.Status,
				EnvKey:       envKey,
			})
		}
	}
	return out
}

// ── adapters ─────────────────────────────────────────────────────────────────

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
