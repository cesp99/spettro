package acp

import (
	"encoding/json"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"spettro/internal/agent"
)

// Workflow runs are surfaced to editors as one long-lived tool call whose
// content is rewritten as the run progresses, rather than as a stream of
// separate calls. An editor then shows a single "workflow review-changes"
// entry that grows a phase tree in place — the ACP analogue of the TUI panel —
// while each sub-agent still gets its own tool call, so "follow the agent"
// navigation keeps working.

type acpWorkflowAgent struct {
	Instance string
	Label    string
	Phase    string
	Status   string
	Cached   bool
}

type acpWorkflow struct {
	callID      acpsdk.ToolCallId
	runID       string
	name        string
	description string
	phases      []string
	agents      []acpWorkflowAgent
	logs        []string
}

func (w *acpWorkflow) addPhase(title string) {
	if title == "" {
		return
	}
	for _, p := range w.phases {
		if p == title {
			return
		}
	}
	w.phases = append(w.phases, title)
}

// phaseOrder lists declared phases first, then any the script entered without
// declaring, then a bucket for agents dispatched outside a phase.
func (w *acpWorkflow) phaseOrder() []string {
	order := append([]string(nil), w.phases...)
	seen := map[string]bool{}
	for _, p := range order {
		seen[p] = true
	}
	loose := false
	for _, a := range w.agents {
		switch {
		case a.Phase == "":
			loose = true
		case !seen[a.Phase]:
			seen[a.Phase] = true
			order = append(order, a.Phase)
		}
	}
	if loose {
		order = append(order, "")
	}
	return order
}

func (w *acpWorkflow) render() string {
	var b strings.Builder
	if w.description != "" {
		b.WriteString(w.description + "\n\n")
	}
	for _, phase := range w.phaseOrder() {
		title := phase
		if title == "" {
			title = "(no phase)"
		}
		var members []acpWorkflowAgent
		done, failed := 0, 0
		for _, a := range w.agents {
			if a.Phase != phase {
				continue
			}
			members = append(members, a)
			switch a.Status {
			case "error":
				failed++
			case "running":
			default:
				done++
			}
		}
		if len(members) == 0 {
			fmt.Fprintf(&b, "○ %s — pending\n", title)
			continue
		}
		fmt.Fprintf(&b, "▸ %s — %d/%d done", title, done+failed, len(members))
		if failed > 0 {
			fmt.Fprintf(&b, ", %d failed", failed)
		}
		b.WriteString("\n")
		for _, a := range members {
			glyph := "▶"
			switch a.Status {
			case "error":
				glyph = "✗"
			case "running":
			default:
				glyph = "✓"
			}
			label := a.Label
			if a.Cached {
				label = "replayed · " + label
			}
			fmt.Fprintf(&b, "    %s %s  %s\n", glyph, a.Instance, label)
		}
	}
	if len(w.logs) > 0 {
		b.WriteString("\nlog:\n")
		for _, line := range w.logs {
			b.WriteString("  " + line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Three different payloads travel under these trace names, and they disagree
// about the type of "cached": a member reports whether it was replayed (a
// bool), while the lifecycle's finish payload reports how many calls were
// replayed (a count). Decoding all three into one struct meant the finish
// payload failed to unmarshal, onWorkflowTool bailed out, and the workflow
// tool call was never closed — editors showed a run spinning forever with a
// duplicate completed call appended after it. Each payload gets its own type.

// acpWorkflowArgs is the shape common to every workflow trace: enough to tell
// which run a trace belongs to. Fields that only some payloads carry live in
// the specific types below.
type acpWorkflowArgs struct {
	RunID    string `json:"run_id"`
	Workflow string `json:"workflow"`
}

// acpWorkflowStartArgs is the lifecycle trace's opening payload. The finish
// payload deliberately decodes with this same type: its extra counters are
// summarised in the trace output, and ignoring them here keeps the finish path
// from depending on fields whose types have already drifted once.
type acpWorkflowStartArgs struct {
	acpWorkflowArgs
	Description string `json:"description"`
	Phases      []struct {
		Title string `json:"title"`
	} `json:"phases"`
}

// acpWorkflowProgressArgs is a phase() or log() notification.
type acpWorkflowProgressArgs struct {
	acpWorkflowArgs
	Kind  string `json:"kind"`
	Phase string `json:"phase"`
}

// acpWorkflowMemberArgs is one agent() call's lifecycle.
type acpWorkflowMemberArgs struct {
	acpWorkflowArgs
	Agent  string `json:"agent"`
	Task   string `json:"task"`
	Phase  string `json:"phase"`
	Cached bool   `json:"cached"`
}

// onWorkflowTool folds a workflow trace into the live tool call. It reports
// whether the trace was fully handled: workflow lifecycle and progress traces
// are (they have no standalone meaning), while a member's agent trace is only
// recorded here and still travels the normal path so the editor gets a tool
// call for the sub-agent itself.
func (t *turnState) onWorkflowTool(tr agent.ToolTrace) (handled bool) {
	switch tr.Name {
	case "workflow", "workflow-progress", "agent":
	default:
		return false
	}
	// Only the two fields every payload shares are required here. Decoding a
	// payload-specific field is done below, per trace kind, so a field one
	// payload spells differently can never disown the whole trace.
	var args acpWorkflowArgs
	if json.Unmarshal([]byte(tr.Args), &args) != nil || args.Workflow == "" {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if tr.Name == "workflow" {
		if tr.Status == "running" {
			var start acpWorkflowStartArgs
			_ = json.Unmarshal([]byte(tr.Args), &start)
			w := &acpWorkflow{
				callID:      t.nextToolCallIDLocked("wf"),
				runID:       args.RunID,
				name:        args.Workflow,
				description: start.Description,
			}
			for _, p := range start.Phases {
				w.addPhase(p.Title)
			}
			t.workflow = w
			t.sessionUpdate(acpsdk.StartToolCall(
				w.callID,
				"workflow "+w.name,
				acpsdk.WithStartKind(acpsdk.ToolKindThink),
				acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress),
				acpsdk.WithStartRawInput(rawJSON(tr.Args)),
				acpsdk.WithStartContent(toolOutputContent(w.render(), nil)),
			))
			return true
		}
		w := t.workflow
		if w == nil {
			return false
		}
		status := acpsdk.ToolCallStatusCompleted
		if tr.Status == "error" {
			status = acpsdk.ToolCallStatusFailed
		}
		body := w.render()
		if summary := strings.TrimSpace(tr.Output); summary != "" {
			body = summary + "\n\n" + body
		}
		t.sessionUpdate(acpsdk.UpdateToolCall(
			w.callID,
			acpsdk.WithUpdateStatus(status),
			acpsdk.WithUpdateContent(toolOutputContent(body, nil)),
			acpsdk.WithUpdateRawOutput(map[string]any{"output": tr.Output}),
		))
		t.workflow = nil
		return true
	}

	w := t.workflow
	if w == nil {
		return false
	}
	switch tr.Name {
	case "workflow-progress":
		var prog acpWorkflowProgressArgs
		_ = json.Unmarshal([]byte(tr.Args), &prog)
		switch prog.Kind {
		case "phase":
			w.addPhase(prog.Phase)
		case "log":
			if msg := strings.TrimSpace(tr.Output); msg != "" {
				w.logs = append(w.logs, msg)
			}
		}
		handled = true
	case "agent":
		var mem acpWorkflowMemberArgs
		if json.Unmarshal([]byte(tr.Args), &mem) != nil || mem.Agent == "" {
			return false
		}
		found := false
		for i := range w.agents {
			if w.agents[i].Instance == mem.Agent {
				w.agents[i].Status = tr.Status
				found = true
				break
			}
		}
		if !found {
			w.agents = append(w.agents, acpWorkflowAgent{
				Instance: mem.Agent, Label: mem.Task, Phase: mem.Phase,
				Status: tr.Status, Cached: mem.Cached,
			})
		}
		// Not handled: the sub-agent still deserves its own tool call.
		handled = false
	}
	t.sessionUpdate(acpsdk.UpdateToolCall(
		w.callID,
		acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusInProgress),
		acpsdk.WithUpdateContent(toolOutputContent(w.render(), nil)),
	))
	return handled
}
