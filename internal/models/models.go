// Package models fetches and caches the models.dev catalog.
package models

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const apiURL = "https://models.dev/api.json"

// DevModel is a single model entry from models.dev.
type DevModel struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Family     string    `json:"family"`
	Reasoning  bool      `json:"reasoning"`
	ToolCall   bool      `json:"tool_call"`
	Attachment bool      `json:"attachment"`
	Modalities *struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit *struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost *struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"cost"`
	Status string `json:"status"` // "alpha" | "beta" | "deprecated"
}

// SupportsImage reports whether this model accepts image inputs.
func (m DevModel) SupportsImage() bool {
	if m.Modalities == nil {
		return false
	}
	for _, in := range m.Modalities.Input {
		if in == "image" {
			return true
		}
	}
	return false
}

// DevProvider is a provider entry from models.dev.
type DevProvider struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	API    string              `json:"api"`
	Env    []string            `json:"env"`
	Models map[string]DevModel `json:"models"`
}

// Catalog is the full map returned by models.dev.
type Catalog map[string]DevProvider

// cacheFile returns the path to the local JSON cache.
func cacheFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".spettro", "models.json"), nil
}

// Load returns the catalog from disk cache, or fetches it if unavailable.
func Load() (Catalog, error) {
	path, err := cacheFile()
	if err == nil {
		if data, err := os.ReadFile(path); err == nil {
			var cat Catalog
			if json.Unmarshal(data, &cat) == nil && len(cat) > 0 {
				return cat, nil
			}
		}
	}
	return Fetch()
}

// Fetch downloads the catalog from models.dev and updates the disk cache.
func Fetch() (Catalog, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("models.dev fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("models.dev read: %w", err)
	}

	var cat Catalog
	if err := json.Unmarshal(body, &cat); err != nil {
		return nil, fmt.Errorf("models.dev parse: %w", err)
	}

	if path, err := cacheFile(); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, body, 0o644)
	}

	return cat, nil
}

// RefreshBackground starts a goroutine that refreshes the cache once now and
// then every hour, exactly like opencode's ModelsDev.refresh() interval.
func RefreshBackground(onRefresh func(Catalog)) {
	go func() {
		if cat, err := Fetch(); err == nil && onRefresh != nil {
			onRefresh(cat)
		}
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if cat, err := Fetch(); err == nil && onRefresh != nil {
				onRefresh(cat)
			}
		}
	}()
}
