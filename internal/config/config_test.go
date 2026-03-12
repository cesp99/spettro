package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfg, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("load/create: %v", err)
	}

	cfg.ActiveProvider = "anthropic"
	cfg.ActiveModel = "claude-3-7-sonnet"
	cfg.APIKeys["anthropic"] = "secret"
	if err := Save(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ActiveProvider != "anthropic" {
		t.Fatalf("expected anthropic provider, got %s", reloaded.ActiveProvider)
	}

	p := filepath.Join(tmpHome, ".spettro", "config.json")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("expected private permissions, got %o", info.Mode().Perm())
	}
}
