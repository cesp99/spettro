package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"spettro/internal/hooks"
)

const sessionStartMarker = ".hooks_session_started"

func (r *toolRuntime) runSessionStartHooks(ctx context.Context) error {
	if len(r.hooksConfig.Rules) == 0 {
		return nil
	}
	if strings.TrimSpace(r.sessionDir) != "" {
		marker := filepath.Join(r.sessionDir, sessionStartMarker)
		if _, err := os.Stat(marker); err == nil {
			return nil
		}
		if err := os.MkdirAll(r.sessionDir, 0o700); err == nil {
			_ = os.WriteFile(marker, []byte("started"), 0o644)
		}
	}
	for _, rule := range r.hooksConfig.Rules {
		if !rule.Enabled || rule.Event != hooks.EventSessionStart {
			continue
		}
		if _, err := hooks.Run(ctx, rule, hooks.RunInput{Event: hooks.EventSessionStart}); err != nil {
			return err
		}
	}
	return nil
}

func (r *toolRuntime) runPreToolHooks(ctx context.Context, toolID string, args json.RawMessage) (json.RawMessage, string, error) {
	updated := args
	for _, rule := range r.hooksConfig.Rules {
		if !rule.Enabled || rule.Event != hooks.EventPreToolUse || !hooks.Match(rule, toolID) {
			continue
		}
		res, err := hooks.Run(ctx, rule, hooks.RunInput{Event: hooks.EventPreToolUse, ToolID: toolID, ToolArgs: updated})
		if err != nil {
			return nil, "", err
		}
		switch res.Decision {
		case "deny", "block":
			reason := strings.TrimSpace(res.Reason)
			if reason == "" {
				reason = strings.TrimSpace(res.Message)
			}
			if reason == "" {
				reason = fmt.Sprintf("hook %s denied request", rule.ID)
			}
			r.emitApprovalTrace("denied", "hook", toolID, "", reason)
			return nil, reason, nil
		case "allow":
			if len(res.UpdatedArgs) > 0 && (toolID == "shell-exec" || toolID == "bash" || toolID == "bash-output") {
				updated = res.UpdatedArgs
			}
		}
	}
	return updated, "", nil
}

func (r *toolRuntime) runPostToolHooks(ctx context.Context, toolID string, args json.RawMessage, output string) error {
	for _, rule := range r.hooksConfig.Rules {
		if !rule.Enabled || rule.Event != hooks.EventPostToolUse || !hooks.Match(rule, toolID) {
			continue
		}
		_, err := hooks.Run(ctx, rule, hooks.RunInput{Event: hooks.EventPostToolUse, ToolID: toolID, ToolArgs: args, ToolOutput: truncate(output, 2000)})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *toolRuntime) runPermissionRequestHooks(ctx context.Context, toolID, command string) (string, string, error) {
	for _, rule := range r.hooksConfig.Rules {
		if !rule.Enabled || rule.Event != hooks.EventPermissionRequest || !hooks.Match(rule, toolID) {
			continue
		}
		res, err := hooks.Run(ctx, rule, hooks.RunInput{Event: hooks.EventPermissionRequest, ToolID: toolID, Command: command})
		if err != nil {
			return "", "", err
		}
		switch res.Decision {
		case "deny", "block":
			reason := strings.TrimSpace(res.Reason)
			if reason == "" {
				reason = strings.TrimSpace(res.Message)
			}
			if reason == "" {
				reason = fmt.Sprintf("hook %s denied request", rule.ID)
			}
			return "deny", reason, nil
		case "allow":
			return "allow", strings.TrimSpace(res.Reason), nil
		}
	}
	return "", "", nil
}
