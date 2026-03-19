package agent

import (
	"strings"

	"spettro/internal/config"
)

func hasAnyAction(tool config.ToolSpec, allowed map[string]struct{}) bool {
	if len(allowed) == 0 || len(tool.PermittedActions) == 0 {
		return true
	}
	for _, action := range tool.PermittedActions {
		if _, ok := allowed[action]; ok {
			return true
		}
	}
	return false
}

func resolveToolPolicies(spec config.AgentSpec, manifest *config.AgentManifest) ([]string, map[string]config.ToolSpec) {
	ordered := make([]string, 0, len(spec.AllowedTools))
	seen := map[string]struct{}{}
	for _, id := range spec.AllowedTools {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}

	if manifest == nil {
		return ordered, map[string]config.ToolSpec{}
	}

	toolByID := map[string]config.ToolSpec{}
	for _, tool := range manifest.Tools {
		toolByID[tool.ID] = tool
	}

	agentActions := map[string]struct{}{}
	for _, action := range spec.PermittedActions {
		action = strings.TrimSpace(action)
		if action != "" {
			agentActions[action] = struct{}{}
		}
	}

	allowed := make([]string, 0, len(ordered))
	policies := map[string]config.ToolSpec{}
	for _, id := range ordered {
		tool, ok := toolByID[id]
		if !ok || !tool.Enabled {
			continue
		}
		if !manifest.Runtime.AllowNetworkTools && toolAllowsNetwork(tool) {
			continue
		}
		if !hasAnyAction(tool, agentActions) {
			continue
		}
		allowed = append(allowed, id)
		policies[id] = tool
	}

	return allowed, policies
}

func toolAllowsNetwork(tool config.ToolSpec) bool {
	for _, action := range tool.PermittedActions {
		if strings.EqualFold(strings.TrimSpace(action), "network") {
			return true
		}
	}
	switch tool.ID {
	case "web-fetch", "web-search":
		return true
	default:
		return false
	}
}
