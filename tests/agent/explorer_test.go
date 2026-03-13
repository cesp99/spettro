package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spettro/internal/agent"
)

func TestExplorer_ListsFiles(t *testing.T) {
	// Create a temp dir with a Go file.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Script the LLM:
	// 1st call: call glob to list .go files
	// 2nd call: return FINAL
	pm, provName, modelName := scriptedManager(t, []string{
		`TOOL_CALL {"tool":"glob","args":{"pattern":"**/*.go"}}`,
		"FINAL\nFound Go files in the codebase.",
	})

	explorer := agent.LLMExplorer{
		ProviderManager: pm,
		ProviderName:    func() string { return provName },
		ModelName:       func() string { return modelName },
		CWD:             dir,
		MaxTokens:       0,
	}

	result, err := explorer.Explore(context.Background(), "List all Go files.")
	if err != nil {
		t.Fatalf("Explore returned error: %v", err)
	}
	if strings.TrimSpace(result.Content) == "" {
		t.Error("expected non-empty result content")
	}
	// Should have used at least one tool (the glob call)
	if len(result.Tools) == 0 {
		t.Error("expected at least one tool trace")
	}
	found := false
	for _, tr := range result.Tools {
		if tr.Name == "glob" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a glob tool trace")
	}
}

func TestExplorer_EmptyTask_ReturnsError(t *testing.T) {
	pm, provName, modelName := scriptedManager(t, []string{"FINAL\ndone"})

	explorer := agent.LLMExplorer{
		ProviderManager: pm,
		ProviderName:    func() string { return provName },
		ModelName:       func() string { return modelName },
		CWD:             t.TempDir(),
		MaxTokens:       0,
	}

	_, err := explorer.Explore(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty task, got nil")
	}
	if !strings.Contains(err.Error(), "empty explore task") {
		t.Errorf("expected 'empty explore task' in error, got: %v", err)
	}
}
