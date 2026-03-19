package agent_test

import (
	"context"
	"strings"
	"testing"

	"spettro/internal/agent"
	"spettro/internal/config"
)

func TestLLMAgent_DelegationRespectsHandoffs(t *testing.T) {
	pm, providerName, modelName := scriptedManager(t, []string{
		`TOOL_CALL {"name":"agent","arguments":{"agent":"blocked","task":"inspect repository"}}`,
		"FINAL\ndone",
	})

	manifest := config.AgentManifest{
		Version:      1,
		DefaultAgent: "parent",
		Runtime: config.RuntimePolicy{
			DefaultPermission: config.PermissionAskFirst,
			DefaultTimeoutSec: 60,
		},
		Tools: []config.ToolSpec{
			{ID: "agent", Name: "Agent", Kind: "builtin", Enabled: true, TimeoutSec: 30, PermittedActions: []string{"plan"}},
			{ID: "comment", Name: "Comment", Kind: "builtin", Enabled: true, TimeoutSec: 10, PermittedActions: []string{"read"}},
		},
		Agents: []config.AgentSpec{
			{ID: "parent", Name: "Parent", Mode: "orchestrator", AllowedTools: []string{"agent"}, Permission: config.PermissionAskFirst, MaxSteps: 4, Enabled: true, Handoffs: []string{"allowed"}},
			{ID: "allowed", Name: "Allowed Worker", Mode: "worker", AllowedTools: []string{"comment"}, Permission: config.PermissionAskFirst, MaxSteps: 2, Enabled: true},
			{ID: "blocked", Name: "Blocked Worker", Mode: "worker", AllowedTools: []string{"comment"}, Permission: config.PermissionAskFirst, MaxSteps: 2, Enabled: true},
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

	result, err := ag.Run(context.Background(), "delegate safely")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatalf("expected tool traces, got none")
	}
	if result.Tools[0].Status != "error" {
		t.Fatalf("expected error trace, got: %+v", result.Tools[0])
	}
	if !strings.Contains(result.Tools[0].Output, "cannot delegate") {
		t.Fatalf("expected handoff error message, got: %q", result.Tools[0].Output)
	}
}

func TestLLMAgent_DelegationReturnsStructuredEnvelope(t *testing.T) {
	pm, providerName, modelName := scriptedManager(t, []string{
		`TOOL_CALL {"name":"agent","arguments":{"agent":"worker","task":"inspect repository"}}`,
		"FINAL\nworker summary",
		"FINAL\ndone",
	})

	manifest := config.AgentManifest{
		Version:      2,
		DefaultAgent: "parent",
		Runtime: config.RuntimePolicy{
			DefaultPermission: config.PermissionAskFirst,
			DefaultTimeoutSec: 60,
			SandboxMode:       config.SandboxWorkspaceWrite,
			Delegation:        config.DelegationPolicy{MaxParallelWorkers: 2, MaxDepth: 2},
		},
		Tools: []config.ToolSpec{
			{ID: "agent", Name: "Agent", Kind: "builtin", Enabled: true, TimeoutSec: 30, PermittedActions: []string{"plan"}, RiskLevel: "medium", PrimaryOnly: true},
			{ID: "comment", Name: "Comment", Kind: "builtin", Enabled: true, TimeoutSec: 10, PermittedActions: []string{"read"}, RiskLevel: "low"},
		},
		Agents: []config.AgentSpec{
			{ID: "parent", Name: "Parent", Mode: "orchestrator", Role: config.AgentRoleOrchestrator, AllowedTools: []string{"agent"}, Permission: config.PermissionAskFirst, MaxSteps: 4, Enabled: true, Handoffs: []string{"worker"}},
			{ID: "worker", Name: "Worker", Mode: "ask", Role: config.AgentRoleWorker, AllowedTools: []string{"comment"}, Permission: config.PermissionAskFirst, MaxSteps: 2, Enabled: true},
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

	result, err := ag.Run(context.Background(), "delegate safely")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatalf("expected tool traces, got none")
	}
	if !strings.Contains(result.Tools[0].Output, `"agent":"worker"`) {
		t.Fatalf("expected structured sub-agent envelope, got %q", result.Tools[0].Output)
	}
	if !strings.Contains(result.Tools[0].Output, `"tool_trace_count"`) {
		t.Fatalf("expected envelope metadata, got %q", result.Tools[0].Output)
	}
}
