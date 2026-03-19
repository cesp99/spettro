package agent

import (
	"path/filepath"
	"strings"

	"spettro/internal/config"
)

func normalizePermissionFamily(action string) string {
	a := strings.ToLower(strings.TrimSpace(action))
	switch a {
	case "write", "edit", "apply_patch", "file-write", "multiedit":
		return "edit"
	default:
		return a
	}
}

func evaluatePermissionRule(permission, pattern string, layers ...[]config.PermissionRule) config.RuleAction {
	perm := strings.TrimSpace(permission)
	pat := filepath.ToSlash(strings.TrimSpace(pattern))
	if perm == "" {
		return config.RuleAsk
	}
	decision := config.RuleAsk
	for _, rules := range layers {
		for _, rule := range rules {
			if !wildcardMatch(rule.Permission, perm) {
				continue
			}
			if !wildcardMatch(rule.Pattern, pat) {
				continue
			}
			decision = rule.Action
		}
	}
	return decision
}

func wildcardMatch(rulePattern, value string) bool {
	rulePattern = strings.TrimSpace(rulePattern)
	value = filepath.ToSlash(strings.TrimSpace(value))
	if rulePattern == "" {
		return false
	}
	if rulePattern == "*" {
		return true
	}
	rulePattern = filepath.ToSlash(rulePattern)
	ok, err := filepath.Match(rulePattern, value)
	if err == nil && ok {
		return true
	}
	if strings.HasSuffix(rulePattern, "/*") {
		prefix := strings.TrimSuffix(rulePattern, "*")
		return strings.HasPrefix(value, prefix)
	}
	return rulePattern == value
}
