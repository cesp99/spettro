package agent_test

import (
	"path/filepath"
	"testing"

	"spettro/internal/agent"
)

func TestNormalizeCommand(t *testing.T) {
	got := agent.NormalizeCommandForTesting("   git    status   --short  ")
	if got != "git status --short" {
		t.Fatalf("unexpected normalized command: %q", got)
	}
}

func TestIsAlwaysAllowedCommand(t *testing.T) {
	if !agent.IsAlwaysAllowedCommandForTesting("pwd") {
		t.Fatal("expected pwd to be always allowed")
	}
	if !agent.IsAlwaysAllowedCommandForTesting("git diff --staged") {
		t.Fatal("expected git diff prefix to be always allowed")
	}
	if agent.IsAlwaysAllowedCommandForTesting("npm publish") {
		t.Fatal("npm publish should not be always allowed")
	}
}

func TestAllowedCommandSetRoundTrip(t *testing.T) {
	cwd := t.TempDir()
	set := map[string]struct{}{
		agent.NormalizeCommandForTesting("echo  hi"): {},
		"git status": {},
	}
	if err := agent.SaveAllowedCommandSetForTesting(cwd, set); err != nil {
		t.Fatalf("saveAllowedCommandSet: %v", err)
	}

	loaded, err := agent.LoadAllowedCommandSetForTesting(cwd)
	if err != nil {
		t.Fatalf("loadAllowedCommandSet: %v", err)
	}
	if _, ok := loaded["echo hi"]; !ok {
		t.Fatalf("expected normalized command in loaded set: %+v", loaded)
	}
	if _, ok := loaded["git status"]; !ok {
		t.Fatalf("expected git status in loaded set: %+v", loaded)
	}

	path := agent.AllowedCommandsPathForTesting(cwd)
	if filepath.Base(path) != "allowed_commands.json" {
		t.Fatalf("unexpected file path: %q", path)
	}
}

func TestSplitShellCommandSegments(t *testing.T) {
	parts := agent.SplitShellCommandSegmentsForTesting(`cd src && git status | rg foo; echo "a && b"`)
	if len(parts) != 4 {
		t.Fatalf("expected 4 command segments, got %d: %#v", len(parts), parts)
	}
	want := []string{"cd src", "git status", "rg foo", `echo "a && b"`}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("segment %d mismatch: want %q, got %q", i, want[i], parts[i])
		}
	}
}
