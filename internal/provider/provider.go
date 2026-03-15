package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicOption "github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/openai/openai-go/v3"
	openaiOption "github.com/openai/openai-go/v3/option"

	"spettro/internal/budget"
	"spettro/internal/models"
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
	Local        bool   // true for locally-hosted models (no API key required)
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

// ProviderInfo holds lightweight display info for the connect-provider dialog.
type ProviderInfo struct {
	ID   string
	Name string
	Env  string // primary env var name
}

type Request struct {
	Prompt      string
	Images      []string
	RequireFast bool
	MaxTokens   int // token budget for this request; 0 = unlimited
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

// fallbackModels is shown only when models.dev hasn't loaded yet (first run, no cache).
var fallbackModels = []Model{
	{Provider: "anthropic", ProviderName: "Anthropic", Name: "claude-opus-4", DisplayName: "Claude Opus 4", Vision: true, Reasoning: true, ToolCall: true, EnvKey: "ANTHROPIC_API_KEY"},
	{Provider: "anthropic", ProviderName: "Anthropic", Name: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", Vision: true, Reasoning: true, ToolCall: true, EnvKey: "ANTHROPIC_API_KEY"},
	{Provider: "openai", ProviderName: "OpenAI", Name: "gpt-4.1", DisplayName: "GPT-4.1", Vision: true, ToolCall: true, EnvKey: "OPENAI_API_KEY"},
	{Provider: "openai", ProviderName: "OpenAI", Name: "o3", DisplayName: "o3", Vision: true, Reasoning: true, ToolCall: true, EnvKey: "OPENAI_API_KEY"},
	{Provider: "google", ProviderName: "Google", Name: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro", Vision: true, Reasoning: true, ToolCall: true, EnvKey: "GOOGLE_API_KEY"},
	{Provider: "x-ai", ProviderName: "xAI", Name: "grok-3", DisplayName: "Grok 3", Vision: true, ToolCall: true, EnvKey: "XAI_API_KEY"},
	{Provider: "groq", ProviderName: "Groq", Name: "llama-3.3-70b-versatile", DisplayName: "Llama 3.3 70B", ToolCall: true, EnvKey: "GROQ_API_KEY"},
}

type Manager struct {
	mu           sync.RWMutex
	catalog      []Model           // populated from models.dev; nil = not loaded yet
	localModels  []Model           // models from locally-hosted servers
	apiKeys      map[string]string // provider ID → API key
	providerAPIs map[string]string // provider ID → base URL (from models.dev or local)
}

func NewManager() *Manager {
	return &Manager{
		apiKeys:      map[string]string{},
		providerAPIs: map[string]string{},
	}
}

// SetAPIKeys updates the API keys used by the adapters. Safe to call from any goroutine.
func (m *Manager) SetAPIKeys(keys map[string]string) {
	m.mu.Lock()
	m.apiKeys = make(map[string]string, len(keys))
	for k, v := range keys {
		m.apiKeys[k] = v
	}
	m.mu.Unlock()
}

// SetCatalog replaces the model list with data from a models.dev catalog.
// It is safe to call from any goroutine.
func (m *Manager) SetCatalog(cat models.Catalog) {
	built := buildModels(cat)
	apis := make(map[string]string, len(cat))
	for id, prov := range cat {
		if prov.API != "" {
			apis[id] = prov.API
		}
	}
	m.mu.Lock()
	m.catalog = built
	// Preserve local server entries that were registered via AddLocalModels.
	for k, v := range m.providerAPIs {
		if strings.HasPrefix(k, "http://") || strings.HasPrefix(k, "https://") {
			apis[k] = v
		}
	}
	m.providerAPIs = apis
	m.mu.Unlock()
}

// AddLocalModels registers models from a local server, replacing any previous
// models for the same provider ID (base URL). Safe to call from any goroutine.
func (m *Manager) AddLocalModels(models []Model) {
	if len(models) == 0 {
		return
	}
	providerID := models[0].Provider
	// The OpenAI SDK requires the base URL to include /v1.
	baseURL := strings.TrimRight(providerID, "/") + "/v1"
	m.mu.Lock()
	filtered := m.localModels[:0:0]
	for _, mod := range m.localModels {
		if mod.Provider != providerID {
			filtered = append(filtered, mod)
		}
	}
	m.localModels = append(filtered, models...)
	m.providerAPIs[providerID] = baseURL
	m.mu.Unlock()
}

// RemoveLocalModels removes all models from the given local server URL.
func (m *Manager) RemoveLocalModels(providerID string) {
	m.mu.Lock()
	filtered := m.localModels[:0:0]
	for _, mod := range m.localModels {
		if mod.Provider != providerID {
			filtered = append(filtered, mod)
		}
	}
	m.localModels = filtered
	delete(m.providerAPIs, providerID)
	m.mu.Unlock()
}

// Models returns the full model list (catalog if loaded, else fallback) plus local models.
func (m *Manager) Models() []Model {
	m.mu.RLock()
	cat := m.catalog
	local := m.localModels
	m.mu.RUnlock()
	base := cat
	if len(base) == 0 {
		base = fallbackModels
	}
	out := make([]Model, len(base)+len(local))
	copy(out, base)
	copy(out[len(base):], local)
	return out
}

// ConnectedModels returns models from providers with an API key, plus all local models.
func (m *Manager) ConnectedModels(apiKeys map[string]string) []Model {
	var out []Model
	for _, mod := range m.Models() {
		if mod.Local {
			out = append(out, mod)
			continue
		}
		if key, ok := apiKeys[mod.Provider]; ok && key != "" {
			out = append(out, mod)
		}
	}
	return out
}

// AllProviderInfos returns lightweight info for every provider in the catalog,
// sorted alphabetically (anthropic first). Used by the connect-provider dialog.
func (m *Manager) AllProviderInfos() []ProviderInfo {
	m.mu.RLock()
	cat := m.catalog
	m.mu.RUnlock()

	src := cat
	if len(src) == 0 {
		src = fallbackModels
	}

	seen := map[string]bool{}
	var out []ProviderInfo
	for _, mod := range src {
		if seen[mod.Provider] {
			continue
		}
		seen[mod.Provider] = true
		out = append(out, ProviderInfo{
			ID:   mod.Provider,
			Name: mod.ProviderName,
			Env:  mod.EnvKey,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID == "anthropic" {
			return true
		}
		if out[j].ID == "anthropic" {
			return false
		}
		return out[i].ID < out[j].ID
	})
	return out
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
	m.mu.RLock()
	apiKey := m.apiKeys[providerName]
	baseURL := m.providerAPIs[providerName]
	m.mu.RUnlock()

	if len(req.Images) > 0 && !m.SupportsVision(providerName, modelName) {
		return Response{}, fmt.Errorf("model does not support vision: %s/%s", providerName, modelName)
	}

	allParts := []string{req.Prompt}
	allParts = append(allParts, req.Images...)
	if err := budget.Validate(req.MaxTokens, allParts...); err != nil {
		return Response{}, err
	}

	var adapter Adapter
	if providerName == "anthropic" {
		adapter = AnthropicAdapter{APIKey: apiKey}
	} else {
		if known, ok := knownBaseURLs[providerName]; ok {
			// Prefer known OpenAI-compatible endpoints over models.dev when available.
			baseURL = known
		} else if baseURL == "" {
			if strings.HasPrefix(providerName, "http://") || strings.HasPrefix(providerName, "https://") {
				// Local server: provider ID is the URL itself.
				baseURL = strings.TrimRight(providerName, "/") + "/v1"
			} else if providerName != "openai" && providerName != "openai-compatible" {
				// Avoid silently routing third-party providers to OpenAI.
				return Response{}, fmt.Errorf("no API endpoint configured for provider %q", providerName)
			}
		}
		if apiKey == "" {
			apiKey = "local" // placeholder — local servers don't require auth
		}
		adapter = OpenAICompatibleAdapter{APIKey: apiKey, BaseURL: baseURL}
	}

	resp, err := adapter.Send(ctx, modelName, req)
	if err != nil {
		return Response{}, err
	}
	resp.Provider = providerName
	resp.Model = modelName
	if resp.EstimatedTokens == 0 {
		resp.EstimatedTokens = budget.EstimateTokens(allParts...)
	}
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

		// Collect and sort non-deprecated models alphabetically.
		modelIDs := make([]string, 0, len(prov.Models))
		for id, mod := range prov.Models {
			if mod.Status != "deprecated" {
				modelIDs = append(modelIDs, id)
			}
		}
		sort.Strings(modelIDs)

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

// knownBaseURLs maps provider IDs to their OpenAI-compatible API base URL.
// Used as a fallback when the models.dev catalog hasn't been loaded yet or
// didn't include the API field for that provider.
var knownBaseURLs = map[string]string{
	"groq":         "https://api.groq.com/openai/v1",
	"mistral":      "https://api.mistral.ai/v1",
	"xai":          "https://api.x.ai/v1",
	"x-ai":         "https://api.x.ai/v1",
	"together":     "https://api.together.xyz/v1",
	"togetherai":   "https://api.together.xyz/v1",
	"fireworks":    "https://api.fireworks.ai/inference/v1",
	"fireworks-ai": "https://api.fireworks.ai/inference/v1",
	"openrouter":   "https://openrouter.ai/api/v1",
	"google":       "https://generativelanguage.googleapis.com/v1beta/openai",
	"cohere":       "https://api.cohere.com/compatibility/v1",
	"deepseek":     "https://api.deepseek.com/v1",
	"perplexity":   "https://api.perplexity.ai",
	"zai":          "https://api.zai.ai/v1",
}

// ── adapters ─────────────────────────────────────────────────────────────────

type OpenAICompatibleAdapter struct {
	APIKey  string
	BaseURL string // empty = use OpenAI's default endpoint
}

func (a OpenAICompatibleAdapter) Send(ctx context.Context, model string, req Request) (Response, error) {
	opts := []openaiOption.RequestOption{openaiOption.WithAPIKey(a.APIKey)}
	if a.BaseURL != "" {
		opts = append(opts, openaiOption.WithBaseURL(a.BaseURL))
	}
	client := openai.NewClient(opts...)

	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(req.Prompt),
		},
	})
	if err != nil {
		// Some models (e.g. Codex) only support the legacy /v1/completions endpoint.
		if strings.Contains(err.Error(), "not a chat model") || strings.Contains(err.Error(), "v1/completions") {
			return a.sendLegacyCompletion(ctx, client, model, req)
		}
		return Response{}, err
	}

	content := ""
	if len(completion.Choices) > 0 {
		content = completion.Choices[0].Message.Content
	}
	return Response{
		Content:         content,
		EstimatedTokens: int(completion.Usage.TotalTokens),
	}, nil
}

func (a OpenAICompatibleAdapter) sendLegacyCompletion(ctx context.Context, client openai.Client, model string, req Request) (Response, error) {
	completion, err := client.Completions.New(ctx, openai.CompletionNewParams{
		Model:  openai.CompletionNewParamsModel(model),
		Prompt: openai.CompletionNewParamsPromptUnion{OfString: openai.String(req.Prompt)},
	})
	if err != nil {
		return Response{}, err
	}
	content := ""
	if len(completion.Choices) > 0 {
		content = completion.Choices[0].Text
	}
	return Response{
		Content:         content,
		EstimatedTokens: int(completion.Usage.TotalTokens),
	}, nil
}

type AnthropicAdapter struct {
	APIKey string
}

func (a AnthropicAdapter) Send(ctx context.Context, model string, req Request) (Response, error) {
	client := anthropic.NewClient(anthropicOption.WithAPIKey(a.APIKey))

	msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: 8096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt)),
		},
	})
	if err != nil {
		return Response{}, err
	}

	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.AsText().Text)
		}
	}
	return Response{
		Content:         sb.String(),
		EstimatedTokens: int(msg.Usage.InputTokens + msg.Usage.OutputTokens),
	}, nil
}
