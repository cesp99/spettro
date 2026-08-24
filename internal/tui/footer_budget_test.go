package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"spettro/internal/agent"
	"spettro/internal/config"
	"spettro/internal/session"
)

// footerModel is a model with a transcript worth reading, which is the thing
// the footer is not allowed to bury.
func footerModel(width, height int) Model {
	m := NewModelForTesting()
	m.manifest = config.DefaultAgentManifest()
	m.ready = true
	m.width, m.height = width, height
	m.mode = "coding"
	m = m.recalcLayout()
	return m
}

func footerSwarm(m *Model, members int) {
	for i := 1; i <= members; i++ {
		m.applyToolTraceToObservability(swarmTrace(fmt.Sprintf("code#%d", i),
			fmt.Sprintf("refactor internal/pkg%d/main.go", i), "running"))
	}
}

func footerWorkers(m *Model, n int) {
	for i := 1; i <= n; i++ {
		m.applyToolTraceToObservability(agent.ToolTrace{
			AgentID: "explore", Name: "agent", Status: "running",
			Args: fmt.Sprintf(`{"agent":"explore","task":"survey subsystem %d","parent_agent_id":"coding"}`, i),
		})
	}
}

func footerTodos(m *Model, n int) {
	for i := 1; i <= n; i++ {
		st := session.TaskStatusPending
		if i == 1 {
			st = session.TaskStatusInProgress
		}
		m.todos = append(m.todos, session.Todo{
			ID: fmt.Sprintf("t%d", i), Content: fmt.Sprintf("task %d — do the thing properly", i), Status: st,
		})
	}
}

// The footer is an annotation on the conversation, never a replacement for it.
// Whatever is running — a swarm, a workflow, a dozen delegations, a long todo
// list, or all of them at once — the region below the transcript stays inside
// one budget derived from the terminal height.
func TestFooterNeverBloatsTheUI(t *testing.T) {
	cases := []struct {
		name string
		seed func(*Model)
	}{
		{"swarm", func(m *Model) { footerSwarm(m, 20) }},
		{"workflow", func(m *Model) {
			m.applyToolTraceToObservability(wfStartTrace())
			for i := 1; i <= 20; i++ {
				m.applyToolTraceToObservability(wfAgentTrace(fmt.Sprintf("general-purpose#%d", i),
					fmt.Sprintf("verify:%d", i), "Verify", "running", false))
			}
		}},
		{"delegations", func(m *Model) { footerWorkers(m, 20) }},
		{"todos", func(m *Model) { footerTodos(m, 30) }},
		{"everything", func(m *Model) {
			m.applyToolTraceToObservability(wfStartTrace())
			for i := 1; i <= 8; i++ {
				m.applyToolTraceToObservability(wfAgentTrace(fmt.Sprintf("general-purpose#%d", i),
					fmt.Sprintf("verify:%d", i), "Verify", "running", false))
			}
			footerSwarm(m, 12)
			footerWorkers(m, 9)
			footerTodos(m, 25)
		}},
	}
	for _, tc := range cases {
		for _, height := range []int{20, 24, 30, 40, 60, 100} {
			m := footerModel(110, height)
			tc.seed(&m)
			got := m.renderParallelAgents()
			rows := 0
			if got != "" {
				rows = lipgloss.Height(got)
			}
			if budget := footerBudget(height); rows > budget {
				t.Fatalf("%s at height %d: footer took %d rows, budget is %d:\n%s",
					tc.name, height, rows, budget, got)
			}
			// A quarter of the screen is the most it may ever claim.
			if rows > height/4+1 {
				t.Fatalf("%s at height %d: footer took %d rows, more than a quarter of the screen",
					tc.name, height, rows)
			}
		}
	}
}

// Being small is not enough: the footer has to stay honest about what it hid,
// because a silent cap reads as "that is all there is".
func TestFooterOwnsUpToWhatItHides(t *testing.T) {
	m := footerModel(110, 30)
	m.applyToolTraceToObservability(wfStartTrace())
	for i := 1; i <= 12; i++ {
		m.applyToolTraceToObservability(wfAgentTrace(fmt.Sprintf("general-purpose#%d", i),
			fmt.Sprintf("verify:%d", i), "Verify", "running", false))
	}
	footerSwarm(&m, 10)
	footerWorkers(&m, 6)
	footerTodos(&m, 9)
	got := m.renderParallelAgents()
	for _, want := range []string{"more running", "ctrl+b", "agents", "todos"} {
		if !strings.Contains(got, want) {
			t.Fatalf("footer never mentions %q:\n%s", want, got)
		}
	}
}

// The one-line forms are what keep the reservations affordable, so they have
// to actually fit in one line and still carry a count.
func TestFooterOneLineForms(t *testing.T) {
	m := footerModel(110, 24)
	footerWorkers(&m, 7)
	footerTodos(&m, 5)

	active := make([]parallelAgentEntry, 0, len(m.parallelAgents))
	for _, a := range m.parallelAgents {
		if a.Status == "running" && a.Kind != "swarm" {
			active = append(active, a)
		}
	}
	deleg := m.delegationLines(active, 1)
	if len(deleg) != 1 || !strings.Contains(deleg[0], "7 running") {
		t.Fatalf("one-line delegation form is wrong: %q", deleg)
	}
	if todos := m.todoLines(1); len(todos) != 1 {
		t.Fatalf("one-line todo form took %d lines: %q", len(todos), todos)
	}
	// And every budget from 1 upward is honoured exactly.
	for rows := 1; rows <= 12; rows++ {
		if got := len(m.delegationLines(active, rows)); got > rows {
			t.Fatalf("delegations took %d lines of a %d-row budget", got, rows)
		}
		if got := len(m.todoLines(rows)); got > rows {
			t.Fatalf("todos took %d lines of a %d-row budget", got, rows)
		}
	}
}

// A finished swarm is history. It collapses to a single line so it stops
// costing the transcript anything while the next turn runs.
func TestSwarmBlockCollapsesWhenDone(t *testing.T) {
	m := footerModel(110, 40)
	footerSwarm(&m, 6)
	live := lipgloss.Height(m.renderSwarmBlock(90, 6))
	for i := 1; i <= 6; i++ {
		m.applyToolTraceToObservability(swarmTrace(fmt.Sprintf("code#%d", i),
			fmt.Sprintf("refactor internal/pkg%d/main.go", i), "success"))
	}
	done := m.renderSwarmBlock(90, 6)
	if h := lipgloss.Height(done); h != 3 { // one line plus the border
		t.Fatalf("a finished swarm takes %d rows, want 3:\n%s", h, done)
	}
	if live <= 3 {
		t.Fatalf("a running swarm should show more than the collapsed form, got %d rows", live)
	}
	if !strings.Contains(done, "6 done") {
		t.Fatalf("the collapsed swarm loses the result:\n%s", done)
	}
	// The side panel still has the whole thing.
	full := strings.Join(m.sidePanelSwarmLines(48), "\n")
	if !strings.Contains(full, "code#5") {
		t.Fatalf("the side panel must keep every member:\n%s", full)
	}
}
