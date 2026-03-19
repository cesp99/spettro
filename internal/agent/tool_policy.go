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
		if _, ok := allowed[normalizePermissionFamily(action)]; ok {
			return true
		}
	}
	return false
}

func toolPermissionFamilies(tool config.ToolSpec) []string {
	families := make([]string, 0, len(tool.PermittedActions))
	seen := map[string]struct{}{}
	for _, action := range tool.PermittedActions {
		fam := normalizePermissionFamily(action)
		if fam == "" {
			continue
		}
		if _, ok := seen[fam]; ok {
			continue
		}
		seen[fam] = struct{}{}
		families = append(families, fam)
	}
	return families
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
		for _, alias := range tool.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				toolByID[alias] = tool
			}
		}
	}

	agentActions := map[string]struct{}{}
	for _, action := range spec.PermittedActions {
		action = normalizePermissionFamily(action)
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
		if tool.PrimaryOnly && !spec.IsPrimaryRole() {
			continue
		}
		if !hasAnyAction(tool, agentActions) {
			continue
		}
		if !isToolAllowedByRules(spec, tool, manifest) {
			continue
		}
		allowed = append(allowed, id)
		policies[id] = tool
	}

	return allowed, policies
}

func isToolAllowedByRules(spec config.AgentSpec, tool config.ToolSpec, manifest *config.AgentManifest) bool {
	if manifest == nil {
		return true
	}
	layers := [][]config.PermissionRule{manifest.Runtime.PermissionRules, spec.PermissionRules, tool.PermissionRules}
	if evaluatePermissionRule("tool", tool.ID, layers...) == config.RuleDeny {
		return false
	}
	for _, fam := range toolPermissionFamilies(tool) {
		if evaluatePermissionRule(fam, tool.ID, layers...) == config.RuleDeny {
			return false
		}
	}
	return true
}
