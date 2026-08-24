package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// The Ultra swarm gets its own bordered block rather than sharing the plain
// delegation list. Two things made the old rendering hard to read: swarm
// members looked exactly like ordinary sub-agents, so a fan-out of twenty
// disappeared into a wall of identical rows; and only running members were
// drawn, so the list shrank as the swarm progressed and never showed what the
// swarm as a whole had done. This block keeps every member visible with its
// outcome, groups them under one header with a meter, and shows what each one
// is doing right now instead of the item it was handed at launch.

type swarmSummary struct {
	members []parallelAgentEntry
	types   []string
	running int
	done    int
	failed  int
}

func (m Model) swarmSummary() swarmSummary {
	var s swarmSummary
	seenType := map[string]bool{}
	for _, a := range m.parallelAgents {
		if a.Kind != "swarm" {
			continue
		}
		s.members = append(s.members, a)
		if t := swarmSpecID(a.ID); !seenType[t] {
			seenType[t] = true
			s.types = append(s.types, t)
		}
		switch a.Status {
		case "running":
			s.running++
		case "failed", "error":
			s.failed++
		default:
			s.done++
		}
	}
	return s
}

func (s swarmSummary) headline() string {
	parts := []string{fmt.Sprintf("%d running", s.running), fmt.Sprintf("%d done", s.done)}
	if s.failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.failed))
	}
	return strings.Join(parts, " · ")
}

// compactHeadline is the glyph form used when the panel is too narrow to spell
// the counts out; see workflowRun.compactHeadline.
func (s swarmSummary) compactHeadline() string {
	out := fmt.Sprintf("%d▶ %d✓", s.running, s.done)
	if s.failed > 0 {
		out += fmt.Sprintf(" %d✗", s.failed)
	}
	return out
}

// swarmTitleLine renders the header, falling back to glyph counts when the
// spelled-out ones will not fit.
func (s swarmSummary) titleLine(budget int) string {
	title := "ultra swarm"
	if len(s.types) == 1 {
		title += " · " + s.types[0]
	}
	prefix := "⚡ " + truncateLabel(title, max(10, budget/2))
	headline := s.headline()
	if lipgloss.Width(prefix)+1+lipgloss.Width(headline) > budget {
		headline = s.compactHeadline()
	}
	return lipgloss.NewStyle().Bold(true).Foreground(colorWarn).Render(prefix) + " " +
		styleMuted.Render(truncateLabel(headline, max(4, budget-lipgloss.Width(prefix)-1)))
}

// swarmMemberRow renders one member: status glyph, instance name in the
// agent's manifest colour, and its live activity (falling back to the item it
// was assigned when it has not called a tool yet).
func (m Model) swarmMemberRow(a parallelAgentEntry, budget int) string {
	agentColor := modeColor("")
	if spec, ok := m.manifest.AgentByID(swarmSpecID(a.ID)); ok {
		agentColor = modeColor(spec.Color)
	}
	icon, iconStyle := agentStatusGlyph(swarmStatus(a.Status))
	nameStyle := lipgloss.NewStyle().Foreground(agentColor)
	if a.Status != "running" {
		nameStyle = nameStyle.Faint(true)
	}
	name := truncateAgentName(a.ID, max(8, budget/3))
	detail := a.Task
	if a.Status == "running" {
		if live := m.latestAgentActivity(a.ID); live != "" {
			detail = live
		}
	}
	prefix := iconStyle.Render(icon+" ") + nameStyle.Render(name)
	return prefix + " " + styleMuted.Render(
		truncateLabel(strings.ReplaceAll(detail, "\n", " "), max(6, budget-lipgloss.Width(prefix)-1)))
}

// swarmStatus normalises the two spellings a failed member can arrive with.
func swarmStatus(status string) string {
	switch status {
	case "running":
		return "running"
	case "failed", "error":
		return "failed"
	}
	return "done"
}

// renderSwarmBlock is the footer form of the swarm view, shown under the
// transcript when the side panel is hidden.
func (m Model) renderSwarmBlock(width int) string {
	s := m.swarmSummary()
	if len(s.members) == 0 {
		return ""
	}
	budget := max(width-4, 24)
	lines := []string{
		s.titleLine(budget),
		progressBar(min(24, max(8, budget-14)), s.done, s.failed, len(s.members)) + " " +
			styleMuted.Render(fmt.Sprintf("%d/%d", s.done+s.failed, len(s.members))),
	}
	// A wide fan-out is capped so the swarm never crowds the conversation off
	// screen; running members are kept in preference to finished ones.
	const maxRows = 10
	rows := s.members
	if len(rows) > maxRows {
		rows = prioritiseRunning(rows, maxRows)
	}
	for _, a := range rows {
		lines = append(lines, m.swarmMemberRow(a, budget))
	}
	if len(rows) < len(s.members) {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("… %d more — ctrl+b for the full swarm", len(s.members)-len(rows))))
	}
	return lipgloss.NewStyle().
		Width(width-2).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// prioritiseRunning keeps the members worth watching when the block cannot
// show them all: everything still running first, in launch order, then the
// most recently finished.
func prioritiseRunning(members []parallelAgentEntry, limit int) []parallelAgentEntry {
	out := make([]parallelAgentEntry, 0, limit)
	for _, a := range members {
		if a.Status == "running" && len(out) < limit {
			out = append(out, a)
		}
	}
	for i := len(members) - 1; i >= 0 && len(out) < limit; i-- {
		if members[i].Status != "running" {
			out = append(out, members[i])
		}
	}
	return out
}
