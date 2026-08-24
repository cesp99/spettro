package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// The workflow panel is a phase tree, not a flat agent list: a workflow's
// whole point is that the structure was decided before the run, so the panel
// shows every declared phase from the start — including ones nothing has
// reached yet — and fills it in as agents land. A flat list would make a
// 3-phase, 20-agent run unreadable and would hide what is still to come.

// progressBar renders a fixed-width meter. Done and failed both count as
// finished — a failed agent is not still working — but failures are drawn in
// their own colour so a red-heavy bar reads as trouble at a glance.
func progressBar(width, done, failed, total int) string {
	if width < 4 {
		width = 4
	}
	if total <= 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("░", width))
	}
	doneCells := done * width / total
	failCells := failed * width / total
	if failed > 0 && failCells == 0 {
		failCells = 1
	}
	if doneCells+failCells > width {
		doneCells = width - failCells
	}
	if doneCells < 0 {
		doneCells = 0
	}
	rest := width - doneCells - failCells
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(colorSuccess).Render(strings.Repeat("█", doneCells)))
	b.WriteString(lipgloss.NewStyle().Foreground(colorError).Render(strings.Repeat("█", failCells)))
	b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("░", rest)))
	return b.String()
}

// truncateAgentName shortens an instance name while keeping its "#N" suffix.
// Workflow members share a long agent-type prefix ("general-purpose#7"), so a
// plain truncation clips off the only part that tells them apart.
func truncateAgentName(name string, width int) string {
	if width < 4 || lipgloss.Width(name) <= width {
		return truncateLabel(name, width)
	}
	hash := strings.LastIndexByte(name, '#')
	if hash <= 0 {
		return truncateLabel(name, width)
	}
	suffix := name[hash:]
	keep := width - lipgloss.Width(suffix) - 1
	if keep < 1 {
		return truncateLabel(name, width)
	}
	return name[:keep] + "…" + suffix
}

func agentStatusGlyph(status string) (string, lipgloss.Style) {
	switch status {
	case "running":
		return "▶", lipgloss.NewStyle().Foreground(colorToolRun)
	case "failed":
		return "✗", lipgloss.NewStyle().Foreground(colorError)
	default:
		return "✓", lipgloss.NewStyle().Foreground(colorSuccess)
	}
}

// workflowHeadline is the one-line summary shown in the panel title and in the
// compact footer block.
func (w *workflowRun) headline() string {
	running, done, failed, cached := w.counts()
	parts := []string{fmt.Sprintf("%d running", running), fmt.Sprintf("%d done", done)}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if cached > 0 {
		parts = append(parts, fmt.Sprintf("%d replayed", cached))
	}
	return strings.Join(parts, " · ")
}

// compactHeadline is the same counts in glyphs, for a panel too narrow to
// spell them out. Clipping "1 failed" to "1 fai…" would hide the one number
// that matters, so the narrow case gets its own form rather than a truncation.
func (w *workflowRun) compactHeadline() string {
	running, done, failed, _ := w.counts()
	out := fmt.Sprintf("%d▶ %d✓", running, done)
	if failed > 0 {
		out += fmt.Sprintf(" %d✗", failed)
	}
	return out
}

// workflowRow is one agent line, tagged so the row budget drops the least
// interesting work first.
type workflowRow struct {
	line string
	// prio orders survival when rows must be dropped: still running beats
	// failed beats succeeded. A failure is the row a reader most wants to see
	// among finished work, so it must outlive the successes around it.
	prio int
}

const (
	wfRowRunning = iota
	wfRowFailed
	wfRowDone
)

// workflowGroup is a phase header plus the agents under it.
type workflowGroup struct {
	header string
	rows   []workflowRow
}

// workflowTreeLines renders the panel body: title, description, one group per
// phase with its own meter and agent rows, then the tail of the script's log.
//
// maxRows caps the output (0 = uncapped, which is what the side panel uses).
// The cap never drops a phase: a phase nobody has reached yet is exactly the
// information the panel exists to show, so trimming takes finished agent rows
// instead and reports how many it hid.
func (m Model) workflowTreeLines(width, maxRows int) []string {
	w := m.workflow
	if w == nil {
		return nil
	}
	budget := max(width, 24)

	var titleStyle lipgloss.Style
	var marker string
	switch w.Status {
	case "failed":
		titleStyle, marker = lipgloss.NewStyle().Bold(true).Foreground(colorError), "✗"
	case "running":
		titleStyle, marker = lipgloss.NewStyle().Bold(true).Foreground(colorToolRun), "◆"
	default:
		titleStyle, marker = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess), "✓"
	}

	running, done, failed, _ := w.counts()
	total := running + done + failed
	// The header is built to fit: in the side panel an unclamped one wraps
	// onto a second line and knocks the whole tree out of alignment.
	name := truncateLabel(w.Name, max(10, budget/2))
	headline := w.headline()
	prefix := marker + " workflow " + name
	if lipgloss.Width(prefix)+1+lipgloss.Width(headline) > budget {
		headline = w.compactHeadline()
	}
	title := titleStyle.Render(prefix) + " " +
		styleMuted.Render(truncateLabel(headline, max(4, budget-lipgloss.Width(prefix)-1)))
	head := []string{title}
	if w.Description != "" {
		head = append(head, styleMuted.Render("  "+truncateLabel(w.Description, budget-2)))
	}
	if total > 0 {
		head = append(head, "  "+progressBar(min(24, max(8, budget-24)), done, failed, total)+" "+
			styleMuted.Render(fmt.Sprintf("%d/%d", done+failed, total)))
	}

	var groups []workflowGroup
	for _, phase := range w.phaseOrder() {
		groups = append(groups, m.workflowPhaseGroup(w, phase, budget))
	}

	var tail []string
	if len(w.Logs) > 0 {
		tail = append(tail, "")
		for _, entry := range lastLogs(w.Logs, 4) {
			tail = append(tail, styleMuted.Render("  log ")+
				lipgloss.NewStyle().Foreground(colorText).Render(truncateLabel(entry.Message, budget-7)))
		}
	}
	if w.Status != "running" && w.Summary != "" {
		tail = append(tail, styleMuted.Render("  "+truncateLabel(w.Summary, budget-2)))
	}

	return assembleWorkflowTree(head, groups, tail, maxRows)
}

// assembleWorkflowTree flattens the tree, dropping rows only when it has to.
// Phase headers and running agents are never dropped; finished agents go
// first, then the log tail, and whatever was hidden is counted in a final line.
func assembleWorkflowTree(head []string, groups []workflowGroup, tail []string, maxRows int) []string {
	totalRows, runningRows := 0, 0
	for _, g := range groups {
		totalRows += len(g.rows)
		for _, r := range g.rows {
			if r.prio == wfRowRunning {
				runningRows++
			}
		}
	}
	fixed := len(head) + len(groups)
	full := fixed + totalRows + len(tail)
	if maxRows <= 0 || full <= maxRows {
		return flattenWorkflowTree(head, groups, tail, nil, 0)
	}
	// Reserve one row for the "… N hidden" notice.
	spare := maxRows - fixed - 1
	keepTail := len(tail)
	if spare-keepTail < runningRows {
		keepTail = 0
	}
	allowedFinished := max(spare-keepTail-runningRows, 0)
	hidden := (totalRows - runningRows - allowedFinished) + (len(tail) - keepTail)
	// Spend the finished-row budget on failures first, then successes, so a
	// trimmed panel still shows what went wrong.
	keep := map[int]bool{}
	budgetLeft := allowedFinished
	for _, prio := range []int{wfRowFailed, wfRowDone} {
		i := 0
		for _, g := range groups {
			for _, r := range g.rows {
				if r.prio != wfRowRunning {
					if r.prio == prio && budgetLeft > 0 {
						keep[i] = true
						budgetLeft--
					}
				}
				i++
			}
		}
	}
	return flattenWorkflowTree(head, groups, tail[:keepTail], keep, hidden)
}

// flattenWorkflowTree emits the lines. A nil keep set means "no trimming";
// otherwise a finished row survives only if its index is in the set.
func flattenWorkflowTree(head []string, groups []workflowGroup, tail []string, keep map[int]bool, hidden int) []string {
	lines := append([]string(nil), head...)
	i := 0
	for _, g := range groups {
		lines = append(lines, g.header)
		for _, r := range g.rows {
			if r.prio == wfRowRunning || keep == nil || keep[i] {
				lines = append(lines, r.line)
			}
			i++
		}
	}
	lines = append(lines, tail...)
	if hidden > 0 {
		lines = append(lines, styleMuted.Render(fmt.Sprintf("  … %d finished rows hidden — ctrl+b for the full tree", hidden)))
	}
	return lines
}

func lastLogs(logs []workflowLogEntry, n int) []workflowLogEntry {
	if len(logs) <= n {
		return logs
	}
	return logs[len(logs)-n:]
}

func (m Model) workflowPhaseGroup(w *workflowRun, phase string, budget int) workflowGroup {
	agents := w.agentsInPhase(phase)
	title := phase
	if title == "" {
		title = "(no phase)"
	}
	pDone, pFailed, pRunning := 0, 0, 0
	for _, a := range agents {
		switch a.Status {
		case "running":
			pRunning++
		case "failed":
			pFailed++
		default:
			pDone++
		}
	}
	// A phase with nothing running and nothing finished has not been reached
	// yet; it is still listed, dimmed, because knowing what is coming is half
	// the value of declaring phases up front.
	glyph, style := "○", lipgloss.NewStyle().Foreground(colorDim)
	switch {
	case pRunning > 0:
		glyph, style = "▸", lipgloss.NewStyle().Bold(true).Foreground(colorToolRun)
	case pFailed > 0:
		glyph, style = "▸", lipgloss.NewStyle().Bold(true).Foreground(colorWarn)
	case pDone > 0:
		glyph, style = "▸", lipgloss.NewStyle().Bold(true).Foreground(colorText)
	}
	head := "  " + style.Render(glyph+" "+truncateLabel(title, max(8, budget/2)))
	if len(agents) > 0 {
		head += "  " + progressBar(10, pDone, pFailed, len(agents)) + " " +
			styleMuted.Render(fmt.Sprintf("%d/%d", pDone+pFailed, len(agents)))
	}
	group := workflowGroup{header: head}
	for _, a := range agents {
		icon, iconStyle := agentStatusGlyph(a.Status)
		name := truncateAgentName(a.Instance, max(8, budget/3))
		detail := a.Label
		if a.Status == "failed" && a.Detail != "" {
			detail = a.Detail
		} else if a.Status == "running" {
			if live := m.latestAgentActivity(a.Instance); live != "" {
				detail = live
			}
		}
		if a.Cached {
			detail = "replayed · " + detail
		}
		prefix := "    " + iconStyle.Render(icon+" ") + lipgloss.NewStyle().Foreground(colorText).Render(name)
		prio := wfRowDone
		switch a.Status {
		case "running":
			prio = wfRowRunning
		case "failed":
			prio = wfRowFailed
		}
		group.rows = append(group.rows, workflowRow{
			prio: prio,
			line: prefix + " " + styleMuted.Render(
				truncateLabel(strings.ReplaceAll(detail, "\n", " "), max(6, budget-lipgloss.Width(prefix)-1))),
		})
	}
	return group
}

// renderWorkflowBlock is the footer form of the panel, shown under the
// transcript when the side panel is hidden.
func (m Model) renderWorkflowBlock(width int) string {
	// The footer competes with the transcript for rows, so a large run is
	// trimmed rather than allowed to push the conversation off screen.
	const maxRows = 14
	lines := m.workflowTreeLines(width-4, maxRows)
	if len(lines) == 0 {
		return ""
	}
	return lipgloss.NewStyle().
		Width(width-2).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}
