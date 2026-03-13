package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spettro/internal/config"
)

func TestLoadOrCreateRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfg, err := config.LoadOrCreate()
	if err != nil {
		t.Fatalf("load/create: %v", err)
	}

	cfg.ActiveProvider = "anthropic"
	cfg.ActiveModel = "claude-3-7-sonnet"
	if err := config.Save(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.LoadOrCreate()
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

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatal("plaintext key leaked into config file")
	}
}
