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

type Manager struct {
	mu           sync.RWMutex
	catalog      []Model
	localModels  []Model
	apiKeys      map[string]string
	providerAPIs map[string]string
}

func NewManager() *Manager {
	return &Manager{
		apiKeys:      map[string]string{},
		providerAPIs: map[string]string{},
	}
}

func (m *Manager) SetAPIKeys(keys map[string]string) {
	m.mu.Lock()
	m.apiKeys = make(map[string]string, len(keys))
	for k, v := range keys {
		m.apiKeys[k] = v
	}
	m.mu.Unlock()
}

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
	for k, v := range m.providerAPIs {
		if strings.HasPrefix(k, "http://") || strings.HasPrefix(k, "https://") {
			apis[k] = v
		}
	}
	m.providerAPIs = apis
	m.mu.Unlock()
}

func (m *Manager) AddLocalModels(models []Model) {
	if len(models) == 0 {
		return
	}
	providerID := models[0].Provider
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

func (m *Manager) ProviderEnvKey(providerID string) string {
	for _, mod := range m.Models() {
		if mod.Provider == providerID && mod.EnvKey != "" {
			return mod.EnvKey
		}
	}
	return ""
}

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
			baseURL = known
		} else if baseURL == "" {
			if strings.HasPrefix(providerName, "http://") || strings.HasPrefix(providerName, "https://") {
				baseURL = strings.TrimRight(providerName, "/") + "/v1"
			} else if providerName != "openai" && providerName != "openai-compatible" {
				return Response{}, fmt.Errorf("no API endpoint configured for provider %q", providerName)
			}
		}
		if apiKey == "" {
			apiKey = "local"
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
