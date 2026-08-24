package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"spettro/internal/workflow"
)

// Trace names hosts key their workflow rendering off. The lifecycle trace is a
// single running→finished pair (so ACP clients get one tool call spanning the
// whole run), while progress traces are instantaneous timeline entries.
const (
	workflowTraceName         = "workflow"
	workflowProgressTraceName = "workflow-progress"
)

// workflowObserver turns engine events into ToolTraces. Hosts already
// understand traces — the TUI panel, the ACP bridge and the session log all
// consume the same stream — so a workflow needs no transport of its own.
type workflowObserver struct {
	rt    *toolRuntime
	runID string
	meta  workflow.Meta
}

func (o *workflowObserver) emit(name, status string, payload map[string]any, output string) {
	if o.rt.toolCallback == nil {
		return
	}
	payload["run_id"] = o.runID
	payload["workflow"] = o.meta.Name
	args, _ := json.Marshal(payload)
	o.rt.toolCallback(ToolTrace{
		AgentID: o.rt.traceID(),
		Name:    name,
		Status:  status,
		Args:    string(args),
		Output:  truncate(output, 600),
	})
}

// start opens the lifecycle trace. Phases are published up front, from the
// declared meta, so a host can draw the whole plan before the first agent runs
// instead of growing it one phase at a time.
func (o *workflowObserver) start(origin string) {
	phases := make([]map[string]string, 0, len(o.meta.Phases))
	for _, p := range o.meta.Phases {
		phases = append(phases, map[string]string{"title": p.Title, "detail": p.Detail})
	}
	o.emit(workflowTraceName, "running", map[string]any{
		"description": o.meta.Description,
		"phases":      phases,
		"origin":      origin,
	}, "")
}

func (o *workflowObserver) finish(res workflow.Result, err error) {
	status := "success"
	output := fmt.Sprintf("%d agents · %d failed · %d replayed", res.Agents, res.Failed, res.Cached)
	if err != nil {
		status = "error"
		output = err.Error()
	}
	o.emit(workflowTraceName, status, map[string]any{
		"agents": res.Agents,
		"failed": res.Failed,
		"cached": res.Cached,
		"tokens": res.Tokens,
	}, output)
}

func (o *workflowObserver) handle(ev workflow.Event) {
	switch ev.Kind {
	case workflow.EventPhase:
		o.emit(workflowProgressTraceName, "success", map[string]any{
			"kind":  "phase",
			"phase": ev.Phase,
		}, ev.Phase)
	case workflow.EventLog:
		o.emit(workflowProgressTraceName, "success", map[string]any{
			"kind":  "log",
			"phase": ev.Phase,
		}, ev.Message)
	case workflow.EventAgentStart:
		o.emitAgent(ev, "running", "")
	case workflow.EventAgentDone:
		o.emitAgent(ev, "success", ev.Output)
	case workflow.EventAgentError:
		o.emitAgent(ev, "error", ev.Message)
	}
}

// emitAgent publishes a workflow member's lifecycle as an "agent" trace — the
// same shape delegation and Ultra produce — with the workflow fields hosts use
// to group it under its phase.
func (o *workflowObserver) emitAgent(ev workflow.Event, status, output string) {
	if o.rt.toolCallback == nil {
		return
	}
	args, _ := json.Marshal(map[string]any{
		"agent":           ev.Instance,
		"task":            ev.Label,
		"parent_agent_id": o.rt.traceID(),
		"workflow":        o.meta.Name,
		"run_id":          o.runID,
		"phase":           ev.Phase,
		"index":           ev.Index,
		"cached":          ev.Cached,
	})
	o.rt.toolCallback(ToolTrace{
		AgentID: ev.Instance,
		Name:    "agent",
		Status:  status,
		Args:    string(args),
		Output:  truncate(output, 600),
	})
}

// renderWorkflowResult is what the model reads when the tool returns. It is
// the script's return value first — that is the answer the script computed —
// framed by enough run metadata to re-run, resume, or explain the run.
func renderWorkflowResult(runID, dir, origin string, meta workflow.Meta, res workflow.Result, mergeNotes []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<workflow_result name=%q run_id=%q>\n", meta.Name, runID)
	fmt.Fprintf(&b, "<summary>%d agents · %d failed · %d replayed from journal · %d tokens</summary>\n",
		res.Agents, res.Failed, res.Cached, res.Tokens)
	if len(res.Phases) > 0 {
		fmt.Fprintf(&b, "<phases>%s</phases>\n", strings.Join(res.Phases, " → "))
	}
	if len(res.Logs) > 0 {
		b.WriteString("<log>\n")
		for _, line := range res.Logs {
			b.WriteString(truncate(line, 300))
			b.WriteByte('\n')
		}
		b.WriteString("</log>\n")
	}
	b.WriteString("<returned>\n")
	b.WriteString(truncate(encodeWorkflowValue(res.Value), 24000))
	b.WriteString("\n</returned>\n")
	if len(mergeNotes) > 0 {
		b.WriteString("<unmerged>\n")
		for _, n := range mergeNotes {
			b.WriteString(truncate(n, 600))
			b.WriteByte('\n')
		}
		b.WriteString("</unmerged>\n")
	}
	b.WriteString("</workflow_result>")
	fmt.Fprintf(&b, "\nScript: %s · transcript: %s", origin, dir)
	b.WriteString(fmt.Sprintf("\nTo resume after an edit, re-run with script_path and resume_from_run_id=%q: unchanged calls replay from the journal instead of re-running.", runID))
	if len(mergeNotes) > 0 {
		b.WriteString("\nSome sub-agent workspaces did not merge back. Their work is on the branches listed above: resolve each one yourself (merge it, fix conflicts, commit), then delete the branch and its worktree.")
	}
	if res.Failed > 0 {
		b.WriteString("\nSome agents failed and resolved to null in the script. Check the returned value for gaps before trusting it, and re-dispatch what is missing.")
	}
	return b.String()
}

func encodeWorkflowValue(v any) string {
	if v == nil {
		return "(the script returned nothing)"
	}
	if s, ok := v.(string); ok {
		return s
	}
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(encoded)
}
