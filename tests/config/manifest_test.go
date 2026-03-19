package config_test

import (
	"strings"
	"testing"

	"spettro/internal/config"
)

func TestDefaultAgentManifestIsValid(t *testing.T) {
	m := config.DefaultAgentManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("default manifest should validate: %v", err)
	}
	if m.DefaultAgent != "plan" {
		t.Fatalf("expected plan as default agent, got %q", m.DefaultAgent)
	}
}

func TestDecodeAgentManifest(t *testing.T) {
	raw := `
version = 1
default_agent = "plan"

[metadata]
name = "Test agents"
description = "Manifest for tests"

[runtime]
default_permission = "ask-first"
default_timeout_sec = 90
allow_network_tools = false
log_tool_calls = true

[[tools]]
id = "repo-search"
name = "Repository Search"
description = "Searches files"
kind = "builtin"
enabled = true
timeout_sec = 30
requires_approval = false
permitted_actions = ["read", "search"]

[[tools]]
id = "provider-chat"
name = "Provider Chat"
description = "Calls active provider"
kind = "builtin"
enabled = true
timeout_sec = 60
requires_approval = false
permitted_actions = ["chat"]

[[agents]]
id = "plan"
name = "Planning"
description = "Plans work"
skill = "architecture"
mode = "orchestrator"
allowed_tools = ["repo-search"]
permitted_actions = ["read", "search", "plan"]
permission = "ask-first"
temperature = 0.2
max_tokens = 2048
max_steps = 10
handoffs = ["ask"]
enabled = true

[[agents]]
id = "ask"
name = "Ask"
description = "Chat mode"
skill = "conversation"
mode = "orchestrator"
allowed_tools = ["provider-chat", "repo-search"]
permitted_actions = ["chat", "read"]
permission = "restricted"
temperature = 0.5
max_tokens = 4096
max_steps = 8
handoffs = ["plan"]
enabled = true
`
	m, err := config.DecodeAgentManifest(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(m.Agents))
	}
	if len(m.EnabledToolsForAgent("ask")) != 2 {
		t.Fatalf("expected 2 enabled tools for ask")
	}
}

func TestDecodeAgentManifestUnknownToolRefFails(t *testing.T) {
	raw := `
version = 1
default_agent = "plan"

[runtime]
default_permission = "ask-first"
default_timeout_sec = 60

[[tools]]
id = "repo-search"
name = "Repository Search"
description = "Searches files"
kind = "builtin"
enabled = true
timeout_sec = 30
requires_approval = false
permitted_actions = ["read", "search"]

[[agents]]
id = "plan"
name = "Planning"
description = "Plans work"
skill = "architecture"
mode = "orchestrator"
allowed_tools = ["missing-tool"]
permitted_actions = ["plan"]
permission = "ask-first"
max_steps = 5
handoffs = []
enabled = true
`
	_, err := config.DecodeAgentManifest(strings.NewReader(raw))
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error, got %v", err)
	}
}

func TestDecodeAgentManifest_V1IsAutoNormalizedToV2(t *testing.T) {
	raw := `
version = 1
default_agent = "plan"

[runtime]
default_permission = "ask-first"
default_timeout_sec = 60
allow_network_tools = false
log_tool_calls = true

[[tools]]
id = "repo-search"
name = "Repository Search"
description = "Searches files"
kind = "builtin"
enabled = true
timeout_sec = 30
requires_approval = false
permitted_actions = ["read", "search"]

[[agents]]
id = "plan"
name = "Planning"
description = "Plans work"
skill = "architecture"
mode = "orchestrator"
allowed_tools = ["repo-search"]
permitted_actions = ["read", "search", "plan"]
permission = "ask-first"
max_steps = 10
handoffs = []
enabled = true
`
	m, original, changed, err := config.DecodeAgentManifestWithMigrationInfo(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if original != 1 {
		t.Fatalf("expected original version 1, got %d", original)
	}
	if !changed {
		t.Fatal("expected migration change flag for v1 manifest")
	}
	if m.Version != 2 {
		t.Fatalf("expected normalized version 2, got %d", m.Version)
	}
}
