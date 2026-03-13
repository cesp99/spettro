package agent_test

// Integration tests for LLMPlanner using a scripted HTTP server.

import (
	"context"
	"strings"
	"testing"

	"spettro/internal/agent"
)

func TestPlanner_ProducesMarkdownPlan(t *testing.T) {
	dir := t.TempDir()

	srv := scriptedServer(t, []string{
		`TOOL_CALL {"tool":"repo-search","args":{"query":""}}`,
		"FINAL\n## Context\nNeeded for X.\n\n## Current state\n- no files\n\n## Proposed changes\n1. Add foo.go",
	})
	pm, providerName := testProvider(srv)

	planner := agent.LLMPlanner{
		ProviderManager: pm,
		ProviderName:    func() string { return providerName },
		ModelName:       func() string { return "fake-model" },
		CWD:             dir,
	}

	result, err := planner.Plan(context.Background(), "Add a foo feature.")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !strings.Contains(result.Content, "# Plan") {
		t.Errorf("expected '# Plan' header, got: %q", result.Content)
	}
	if !strings.Contains(result.Content, "## Context") {
		t.Errorf("expected '## Context' section, got: %q", result.Content)
	}
}

func TestPlanner_EmptyPrompt_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	srv := scriptedServer(t, nil)
	pm, providerName := testProvider(srv)

	planner := agent.LLMPlanner{
		ProviderManager: pm,
		ProviderName:    func() string { return providerName },
		ModelName:       func() string { return "fake-model" },
		CWD:             dir,
	}

	_, err := planner.Plan(context.Background(), "   ")
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}
