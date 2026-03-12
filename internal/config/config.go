package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type PermissionLevel string

const (
	PermissionYOLO       PermissionLevel = "yolo"
	PermissionRestricted PermissionLevel = "restricted"
	PermissionAskFirst   PermissionLevel = "ask-first"
)

type UserConfig struct {
	ActiveProvider string            `json:"active_provider"`
	ActiveModel    string            `json:"active_model"`
	Permission     PermissionLevel   `json:"permission"`
	APIKeys        map[string]string `json:"api_keys,omitempty"`
}

func Default() UserConfig {
	return UserConfig{
		ActiveProvider: "openai-compatible",
		ActiveModel:    "gpt-5-mini",
		Permission:     PermissionAskFirst,
		APIKeys: map[string]string{
			"openai-compatible": "",
			"anthropic":         "",
		},
	}
}

func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".spettro", "config.json"), nil
}

func LoadOrCreate() (UserConfig, error) {
	p, err := Path()
	if err != nil {
		return UserConfig{}, err
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return UserConfig{}, fmt.Errorf("create global config dir: %w", err)
	}

	var cfg UserConfig
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = Default()
			if err := Save(cfg); err != nil {
				return UserConfig{}, err
			}
			return cfg, nil
		}
		return UserConfig{}, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return UserConfig{}, fmt.Errorf("decode config: %w", err)
	}

	if cfg.APIKeys == nil {
		cfg.APIKeys = map[string]string{}
	}
	return cfg, nil
}

func Save(cfg UserConfig) error {
	p, err := Path()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create global config dir: %w", err)
	}

	// Never persist plaintext API keys in config.json.
	scrubbed := cfg
	scrubbed.APIKeys = nil
	raw, err := json.MarshalIndent(scrubbed, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	return os.Rename(tmp, p)
}
