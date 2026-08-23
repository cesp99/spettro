package tui

import (
	"fmt"
	"strings"
	"testing"

	"spettro/internal/agent"
	"spettro/internal/config"
)

func wfStartTrace() agent.ToolTrace {
	return agent.ToolTrace{
		AgentID: "coding",
		Name:    "workflow",
		Status:  "running",
		Args: `{"run_id":"wf_1","workflow":"review-changes","description":"Review then verify",` +
			`"origin":"inline","phases":[{"title":"Review"},{"title":"Verify"}]}`,
	}
}

func wfAgentTrace(instance, label, phase, status string, cached bool) agent.ToolTrace {
	return agent.ToolTrace{
		AgentID: instance,
		Name:    "agent",
		Status:  status,
		Args: fmt.Sprintf(`{"agent":%q,"task":%q,"parent_agent_id":"coding","workflow":"review-changes",`+
			`"run_id":"wf_1","phase":%q,"cached":%t}`, instance, label, phase, cached),
	}
}

func newWorkflowModel(t *testing.T) Model {
	t.Helper()
	m := NewModelForTesting()
	m.manifest = config.DefaultAgentManifest()
	return m
}

func TestWorkflowPanelLifecycle(t *testing.T) {
	m := newWorkflowModel(t)
	if lines := m.workflowTreeLines(60); lines != nil {
		t.Fatalf("no workflow → no panel, got %v", lines)
	}

	m.applyToolTraceToObservability(wfStartTrace())
	if m.workflow == nil || m.workflow.Name != "review-changes" || m.workflow.Status != "running" {
		t.Fatalf("workflow not started: %+v", m.workflow)
	}
	// Declared phases exist before any agent runs — that is the point of
	// putting them in meta.
	if len(m.workflow.Phases) != 2 {
		t.Fatalf("declared phases lost: %+v", m.workflow.Phases)
	}
	joined := strings.Join(m.workflowTreeLines(70), "\n")
	for _, want := range []string{"review-changes", "Review", "Verify", "Review then verify"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("panel missing %q:\n%s", want, joined)
		}
	}

	m.applyToolTraceToObservability(wfAgentTrace("general-purpose#1", "review:bugs", "Review", "running", false))
	m.applyToolTraceToObservability(wfAgentTrace("general-purpose#2", "review:perf", "Review", "running", false))
	if len(m.workflow.Agents) != 2 {
		t.Fatalf("agents = %+v", m.workflow.Agents)
	}
	// A workflow member belongs to its phase, not to the loose agent list.
	if len(m.parallelAgents) != 0 {
		t.Fatalf("workflow agents must not double as parallel agents: %+v", m.parallelAgents)
	}

	m.applyToolTraceToObservability(wfAgentTrace("general-purpose#1", "review:bugs", "Review", "success", false))
	m.applyToolTraceToObservability(wfAgentTrace("general-purpose#2", "review:perf", "Review", "error", false))
	running, done, failed, _ := m.workflow.counts()
	if running != 0 || done != 1 || failed != 1 {
		t.Fatalf("counts = %d/%d/%d", running, done, failed)
	}
	if !strings.Contains(m.workflow.headline(), "1 failed") {
		t.Fatalf("headline = %q", m.workflow.headline())
	}

	m.applyToolTraceToObservability(agent.ToolTrace{
		AgentID: "coding", Name: "workflow", Status: "success",
		Args: `{"run_id":"wf_1","workflow":"review-changes","agents":2,"failed":1}`,
	})
	if m.workflow.Status != "done" {
		t.Fatalf("status = %q", m.workflow.Status)
	}
}

func TestWorkflowPanelProgressEvents(t *testing.T) {
	m := newWorkflowModel(t)
	m.applyToolTraceToObservability(wfStartTrace())
	m.applyToolTraceToObservability(agent.ToolTrace{
		AgentID: "coding", Name: "workflow-progress", Status: "success",
		Args:   `{"run_id":"wf_1","workflow":"review-changes","kind":"log","phase":"Review"}`,
		Output: "round 1: 3 found",
	})
	// A phase the script entered but meta never declared still has to appear.
	m.applyToolTraceToObservability(agent.ToolTrace{
		AgentID: "coding", Name: "workflow-progress", Status: "success",
		Args:   `{"run_id":"wf_1","workflow":"review-changes","kind":"phase","phase":"Synthesise"}`,
		Output: "Synthesise",
	})
	if len(m.workflow.Logs) != 1 || m.workflow.Logs[0].Message != "round 1: 3 found" {
		t.Fatalf("logs = %+v", m.workflow.Logs)
	}
	joined := strings.Join(m.workflowTreeLines(70), "\n")
	if !strings.Contains(joined, "round 1: 3 found") {
		t.Fatalf("log not rendered:\n%s", joined)
	}
	if !strings.Contains(joined, "Synthesise") {
		t.Fatalf("undeclared phase must still render:\n%s", joined)
	}
	// Declaring the same phase twice must not duplicate the group.
	m.applyToolTraceToObservability(agent.ToolTrace{
		AgentID: "coding", Name: "workflow-progress", Status: "success",
		Args: `{"run_id":"wf_1","workflow":"review-changes","kind":"phase","phase":"Review"}`,
	})
	if len(m.workflow.phaseOrder()) != 3 {
		t.Fatalf("phase order = %v", m.workflow.phaseOrder())
	}
}

func TestWorkflowPanelGroupsAgentsUnderPhases(t *testing.T) {
	m := newWorkflowModel(t)
	m.applyToolTraceToObservability(wfStartTrace())
	m.applyToolTraceToObservability(wfAgentTrace("general-purpose#1", "review:bugs", "Review", "success", false))
	m.applyToolTraceToObservability(wfAgentTrace("general-purpose#2", "refute:bugs", "Verify", "running", false))
	m.applyToolTraceToObservability(wfAgentTrace("general-purpose#3", "loose", "", "running", false))

	if got := len(m.workflow.agentsInPhase("Review")); got != 1 {
		t.Fatalf("Review holds %d agents", got)
	}
	if got := len(m.workflow.agentsInPhase("")); got != 1 {
		t.Fatalf("phaseless agents = %d", got)
	}
	joined := strings.Join(m.workflowTreeLines(70), "\n")
	if !strings.Contains(joined, "(no phase)") {
		t.Fatalf("agents dispatched outside a phase must still show:\n%s", joined)
	}
	reviewIdx := strings.Index(joined, "review:bugs")
	verifyIdx := strings.Index(joined, "refute:bugs")
	if reviewIdx < 0 || verifyIdx < 0 || reviewIdx > verifyIdx {
		t.Fatalf("phases must render in declared order:\n%s", joined)
	}
}

func TestWorkflowPanelMarksReplayedAgents(t *testing.T) {
	m := newWorkflowModel(t)
	m.applyToolTraceToObservability(wfStartTrace())
	m.applyToolTraceToObservability(wfAgentTrace("general-purpose#1", "review:bugs", "Review", "success", true))
	if _, _, _, cached := m.workflow.counts(); cached != 1 {
		t.Fatalf("cached count = %d", cached)
	}
	joined := strings.Join(m.workflowTreeLines(70), "\n")
	if !strings.Contains(joined, "replayed") {
		t.Fatalf("a journal replay must be labelled:\n%s", joined)
	}
}

func TestWorkflowClearedOnNextTurn(t *testing.T) {
	m := newWorkflowModel(t)
	m.applyToolTraceToObservability(wfStartTrace())
	m.startAgentActivity("coding", "next task")
	if m.workflow != nil {
		t.Fatal("a new turn must clear the previous workflow tree")
	}
}

func TestProgressBar(t *testing.T) {
	if got := stripANSIForTest(progressBar(10, 0, 0, 0)); got != strings.Repeat("░", 10) {
		t.Fatalf("empty bar = %q", got)
	}
	if got := stripANSIForTest(progressBar(10, 10, 0, 10)); got != strings.Repeat("█", 10) {
		t.Fatalf("full bar = %q", got)
	}
	// A single failure among many must still be visible rather than rounding
	// away to nothing.
	got := stripANSIForTest(progressBar(10, 0, 1, 100))
	if !strings.HasPrefix(got, "█") {
		t.Fatalf("a lone failure must still light a cell: %q", got)
	}
	if n := len([]rune(stripANSIForTest(progressBar(10, 5, 5, 10)))); n != 10 {
		t.Fatalf("bar width = %d, want exactly 10 cells", n)
	}
}
