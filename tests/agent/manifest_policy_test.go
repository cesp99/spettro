package agent_test

import (
	"context"
	"strings"
	"testing"

	"spettro/internal/agent"
	"spettro/internal/config"
)

func TestLLMAgent_ManifestPolicyFiltersDisallowedTools(t *testing.T) {
	pm, providerName, modelName := scriptedManager(t, []string{
		`TOOL_CALL {"name":"web-search","arguments":{"query":"latest release"}}`,
		"FINAL\ndone",
	})

	manifest := config.AgentManifest{
		Version:      1,
		DefaultAgent: "ask",
		Runtime: config.RuntimePolicy{
			DefaultPermission: config.PermissionAskFirst,
			DefaultTimeoutSec: 60,
			AllowNetworkTools: false,
			LogToolCalls:      true,
		},
		Tools: []config.ToolSpec{
			{ID: "web-search", Name: "Web Search", Kind: "builtin", Enabled: true, TimeoutSec: 30, PermittedActions: []string{"network"}},
			{ID: "comment", Name: "Comment", Kind: "builtin", Enabled: true, TimeoutSec: 10, PermittedActions: []string{"read"}},
		},
		Agents: []config.AgentSpec{
			{
				ID:               "ask",
				Name:             "Ask",
				Mode:             "orchestrator",
				AllowedTools:     []string{"web-search", "comment"},
				PermittedActions: []string{"read"},
				Permission:       config.PermissionAskFirst,
				MaxSteps:         4,
				Enabled:          true,
			},
		},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest validate: %v", err)
	}

	ag := agent.LLMAgent{
		Spec:            manifest.Agents[0],
		ProviderManager: pm,
		ProviderName:    func() string { return providerName },
		ModelName:       func() string { return modelName },
		CWD:             t.TempDir(),
		Manifest:        &manifest,
	}

	result, err := ag.Run(context.Background(), "search the web")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Tools) == 0 || result.Tools[0].Status != "error" {
		t.Fatalf("expected blocked tool error, got: %+v", result.Tools)
	}
	if !strings.Contains(result.Tools[0].Output, `not allowed`) {
		t.Fatalf("expected not allowed error, got: %q", result.Tools[0].Output)
	}
}
