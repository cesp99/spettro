package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"spettro/internal/session"
)

func (r *toolRuntime) emitApprovalTrace(decision, source, toolID, segment, reason string) {
	decision = strings.TrimSpace(strings.ToLower(decision))
	source = strings.TrimSpace(strings.ToLower(source))
	toolID = strings.TrimSpace(toolID)
	segment = strings.TrimSpace(segment)
	reason = strings.TrimSpace(reason)
	if decision == "" {
		decision = "unknown"
	}
	if source == "" {
		source = "unknown"
	}

	if r.toolCallback != nil {
		payload := map[string]string{
			"decision": decision,
			"source":   source,
			"tool_id":  toolID,
			"segment":  segment,
			"reason":   reason,
		}
		raw, _ := json.Marshal(payload)
		r.toolCallback(ToolTrace{
			AgentID: r.agentID,
			Name:    "approval",
			Status:  "success",
			Args:    string(raw),
			Output:  reason,
		})
	}

	if strings.TrimSpace(r.sessionDir) == "" {
		return
	}
	sessionID := filepath.Base(r.sessionDir)
	globalDir := filepath.Dir(filepath.Dir(r.sessionDir))
	_ = session.AppendEvent(globalDir, sessionID, session.AgentEvent{
		At:             time.Now(),
		Kind:           "approval",
		AgentID:        r.agentID,
		Status:         decision,
		Task:           segment,
		Summary:        reason,
		ToolID:         toolID,
		CommandSegment: segment,
		Decision:       decision,
		DecisionSource: source,
		Reason:         reason,
	})
}
