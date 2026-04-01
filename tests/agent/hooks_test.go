package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"spettro/internal/hooks"
)

func TestHooksLoadEffectiveProjectOverridesGlobal(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".spettro")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "hooks.json"), []byte(`{"hooks":[{"id":"fmt","event":"PreToolUse","matcher":"bash","command":"echo global"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".spettro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".spettro", "hooks.json"), []byte(`{"hooks":[{"id":"fmt","event":"PreToolUse","matcher":"bash","command":"echo project"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := hooks.LoadEffective(cwd)
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 merged rule, got %d", len(cfg.Rules))
	}
	if cfg.Rules[0].Command != "echo project" {
		t.Fatalf("expected project override, got %q", cfg.Rules[0].Command)
	}
}

func TestHooksRunDecisionParse(t *testing.T) {
	rule := hooks.EffectiveRule{Rule: hooks.Rule{ID: "deny", Event: hooks.EventPreToolUse, Command: `echo '{"decision":"deny","reason":"blocked"}'`}, Enabled: true}
	res, err := hooks.Run(context.Background(), rule, hooks.RunInput{Event: hooks.EventPreToolUse, ToolID: "bash"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision != "deny" || res.Reason != "blocked" {
		t.Fatalf("unexpected decision parse: %+v", res)
	}
}
