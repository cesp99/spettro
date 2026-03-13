package agent

import (
	"path/filepath"
	"testing"
)

func TestNormalizeCommand(t *testing.T) {
	got := normalizeCommand("   git    status   --short  ")
	if got != "git status --short" {
		t.Fatalf("unexpected normalized command: %q", got)
	}
}

func TestIsAlwaysAllowedCommand(t *testing.T) {
	if !isAlwaysAllowedCommand("pwd") {
		t.Fatal("expected pwd to be always allowed")
	}
	if !isAlwaysAllowedCommand("git diff --staged") {
		t.Fatal("expected git diff prefix to be always allowed")
	}
	if isAlwaysAllowedCommand("npm publish") {
		t.Fatal("npm publish should not be always allowed")
	}
}

func TestAllowedCommandSetRoundTrip(t *testing.T) {
	cwd := t.TempDir()
	set := map[string]struct{}{
		normalizeCommand("echo  hi"): {},
		"git status":                 {},
	}
	if err := saveAllowedCommandSet(cwd, set); err != nil {
		t.Fatalf("saveAllowedCommandSet: %v", err)
	}

	loaded, err := loadAllowedCommandSet(cwd)
	if err != nil {
		t.Fatalf("loadAllowedCommandSet: %v", err)
	}
	if _, ok := loaded["echo hi"]; !ok {
		t.Fatalf("expected normalized command in loaded set: %+v", loaded)
	}
	if _, ok := loaded["git status"]; !ok {
		t.Fatalf("expected git status in loaded set: %+v", loaded)
	}

	path := allowedCommandsPath(cwd)
	if filepath.Base(path) != "allowed_commands.json" {
		t.Fatalf("unexpected file path: %q", path)
	}
}
