package tui

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"spettro/internal/agent"
	"spettro/internal/config"
	"spettro/internal/conversation"
	"spettro/internal/session"
)

func (m *Model) stopAgent() {
	if m.cancelAgent != nil {
		m.cancelAgent()
		m.cancelAgent = nil
	}
	if m.pendingAuth != nil {
		select {
		case m.pendingAuth.response <- shellApprovalResponse{decision: agent.ShellApprovalDeny}:
		default:
		}
	}
	m.thinking = false
	m.toolCh = nil
	m.approvalCh = nil
	m.liveTools = nil
	m.currentTool = nil
	m.pendingAuth = nil
	m.approvalCursor = 0
}

func (m *Model) pushSystemMsg(content string) {
	m.messages = append(m.messages, ChatMessage{
		Role:    RoleSystem,
		Content: content,
		At:      time.Now(),
	})
}

func (m *Model) showBanner(text, kind string) {
	m.banner = text
	m.bannerKind = kind
}

func (m *Model) ensureSession() {
	if m.sessionID == "" {
		m.sessionID = session.NewID(m.cwd)
	}
}

func (m *Model) syncTodosFromSession() {
	if m.sessionID == "" {
		return
	}
	todos, err := session.LoadTodos(m.store.GlobalDir, m.sessionID)
	if err == nil {
		m.todos = todos
	}
}

func parseNumstat(text string, totals map[string][2]int) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		deleted, _ := strconv.Atoi(parts[1])
		path := strings.TrimSpace(parts[2])
		if strings.Contains(path, " -> ") {
			segs := strings.Split(path, " -> ")
			path = strings.TrimSpace(segs[len(segs)-1])
		}
		if path == "" {
			continue
		}
		curr := totals[path]
		totals[path] = [2]int{curr[0] + added, curr[1] + deleted}
	}
}

func normalizeWorkspacePath(cwd, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(cwd, p)
		if err == nil && !strings.HasPrefix(rel, "..") {
			p = rel
		}
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

func (m *Model) markSessionEdit(path string) {
	path = normalizeWorkspacePath(m.cwd, path)
	if path == "" {
		return
	}
	if m.sessionEdits == nil {
		m.sessionEdits = map[string]struct{}{}
	}
	m.sessionEdits[path] = struct{}{}
}

func (m *Model) trackSessionEditFromTrace(t agent.ToolTrace) {
	if t.Name != "file-write" || t.Status == "running" {
		return
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(t.Args), &args); err != nil {
		return
	}
	m.markSessionEdit(args.Path)
}

func (m *Model) refreshModifiedFiles() {
	if len(m.sessionEdits) == 0 {
		m.modifiedFiles = nil
		return
	}

	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = m.cwd
	out, err := cmd.Output()
	if err != nil {
		m.modifiedFiles = nil
		return
	}

	stat := make(map[string]modifiedFileEntry)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			segs := strings.Split(path, " -> ")
			path = strings.TrimSpace(segs[len(segs)-1])
		}
		if path == "" {
			continue
		}
		normPath := normalizeWorkspacePath(m.cwd, path)
		if normPath == "" {
			continue
		}
		if _, ok := m.sessionEdits[normPath]; !ok {
			continue
		}
		entry := stat[normPath]
		entry.Path = normPath
		entry.Untracked = strings.HasPrefix(line, "??")
		stat[normPath] = entry
	}

	numTotals := make(map[string][2]int)
	for _, args := range [][]string{{"diff", "--numstat"}, {"diff", "--cached", "--numstat"}} {
		d := exec.Command("git", args...)
		d.Dir = m.cwd
		data, derr := d.Output()
		if derr == nil {
			parseNumstat(string(data), numTotals)
		}
	}

	m.modifiedFiles = m.modifiedFiles[:0]
	for path, entry := range stat {
		if v, ok := numTotals[path]; ok {
			entry.Added, entry.Deleted = v[0], v[1]
		}
		m.modifiedFiles = append(m.modifiedFiles, entry)
	}
	sort.Slice(m.modifiedFiles, func(i, j int) bool {
		return m.modifiedFiles[i].Path < m.modifiedFiles[j].Path
	})
}

func (m *Model) applyToolTraceToObservability(t agent.ToolTrace) {
	if t.Name != "agent" {
		return
	}
	var args struct {
		Target        string `json:"target"`
		ID            string `json:"id"`
		Task          string `json:"task"`
		ParentAgentID string `json:"parent_agent_id"`
	}
	_ = json.Unmarshal([]byte(t.Args), &args)
	agentID := args.Target
	if agentID == "" {
		agentID = args.ID
	}
	if agentID == "" {
		return
	}
	task := args.Task
	if t.Status == "running" {
		instance := 0
		for _, a := range m.parallelAgents {
			if a.ID == agentID {
				instance++
			}
		}
		kind := "worker"
		if parent, ok := m.manifest.AgentByID(args.ParentAgentID); ok && parent.Mode == "worker" {
			kind = "microagent"
		}
		entry := parallelAgentEntry{
			ID:       agentID,
			Label:    agentID,
			Kind:     kind,
			Instance: instance + 1,
			Task:     task,
			Status:   "running",
		}
		m.parallelAgents = append(m.parallelAgents, entry)
		m.ensureSession()
		_ = session.AppendEvent(m.store.GlobalDir, m.sessionID, session.AgentEvent{
			AgentID:       agentID,
			AgentType:     kind,
			ParentAgentID: args.ParentAgentID,
			Task:          task,
			Status:        "running",
		})
		return
	}
	for i, a := range m.parallelAgents {
		if a.ID == agentID && a.Status == "running" {
			m.parallelAgents = append(m.parallelAgents[:i], m.parallelAgents[i+1:]...)
			break
		}
	}
	m.ensureSession()
	status := "done"
	if t.Status == "error" {
		status = "failed"
	}
	_ = session.AppendEvent(m.store.GlobalDir, m.sessionID, session.AgentEvent{
		AgentID:       agentID,
		AgentType:     "worker",
		ParentAgentID: args.ParentAgentID,
		Task:          task,
		Status:        status,
		Summary:       t.Output,
	})
}

func (m *Model) autoSave() {
	hasContent := false
	for _, msg := range m.messages {
		if msg.Role == RoleUser || msg.Role == RoleAssistant {
			hasContent = true
			break
		}
	}
	if !hasContent {
		return
	}
	m.ensureSession()
	msgs := make([]session.Message, len(m.messages))
	for i, msg := range m.messages {
		msgs[i] = session.Message{
			Role:     string(msg.Role),
			Content:  msg.Content,
			Thinking: msg.Thinking,
			Meta:     msg.Meta,
			At:       msg.At,
		}
	}
	_ = session.Save(m.store.GlobalDir, session.State{
		Metadata: session.Metadata{
			ID:          m.sessionID,
			ProjectPath: m.cwd,
			ProjectHash: session.ProjectHash(m.cwd),
			StartedAt:   msgs[0].At,
		},
		Messages: msgs,
		Todos:    m.todos,
	})
}

func (m *Model) refreshViewport() {
	m.autoSave()
	m.vp.SetContent(m.renderMessages())
	m.vp.GotoBottom()
}

func (m Model) renderPlanMessage(msg ChatMessage, mc lipgloss.Color) string {
	innerW := m.paneWidth() - 8
	if innerW < 10 {
		innerW = 10
	}

	header := lipgloss.NewStyle().
		Foreground(mc).Bold(true).
		Render("◈ plan")

	var bodyParts []string
	if len(msg.Tools) > 0 {
		bodyParts = append(bodyParts, renderToolGroups(msg.Tools, m.showTools, mc))
	}
	bodyParts = append(bodyParts, renderMarkdown(strings.TrimSpace(msg.Content), innerW))

	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(innerW+2).
		Padding(0, 1).
		Render(strings.Join(bodyParts, "\n"))

	return indent(header+"\n"+box, "  ")
}

func (m Model) renderMessages() string {
	if len(m.messages) == 0 {
		return styleMuted.Render("  no messages yet — type a prompt or /help")
	}

	mc := m.currentColor()
	var parts []string

	for _, msg := range m.messages {
		switch msg.Role {
		case RoleUser:
			prefix := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  › ")
			text := lipgloss.NewStyle().Foreground(colorText).Render(msg.Content)
			parts = append(parts, prefix+text)
		case RoleAssistant:
			if msg.Kind == "plan" {
				parts = append(parts, m.renderPlanMessage(msg, mc))
				continue
			}
			bullet := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  ●")
			body := renderMarkdown(msg.Content, m.paneWidth()-8)
			var entryLines []string
			if len(msg.Tools) > 0 {
				entryLines = append(entryLines, renderToolGroups(msg.Tools, m.showTools, mc))
			}
			if strings.TrimSpace(msg.Content) != "" {
				entryLines = append(entryLines, prefixBlockWithBullet(bullet, body))
			}
			if m.showTools && msg.Thinking != "" {
				thinkStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
				entryLines = append(entryLines, thinkStyle.Render("  ┌─ thinking ─┐\n"+indent(msg.Thinking, "  │ ")+"\n  └────────────┘"))
			}
			if msg.Meta != "" {
				entryLines = append(entryLines, styleMuted.Render("  "+msg.Meta))
			}
			parts = append(parts, strings.Join(entryLines, "\n"))
		case RoleSystem:
			s := lipgloss.NewStyle().
				Foreground(colorMuted).
				PaddingLeft(4).
				Width(m.paneWidth() - 4).
				Render(msg.Content)
			parts = append(parts, s)
		}
	}

	if m.thinking && (len(m.liveTools) > 0 || m.currentTool != nil) {
		bullet := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  ●")
		var liveLines []string
		for _, t := range m.liveTools {
			label := formatToolLabel(t.Name, t.Args)
			suffix := lipgloss.NewStyle().Foreground(colorSuccess).Render(" ✓")
			if t.Status == "error" {
				suffix = lipgloss.NewStyle().Foreground(colorError).Render(" ✗")
			}
			liveLines = append(liveLines, bullet+" "+styleMuted.Render(label)+suffix)
		}
		if m.currentTool != nil {
			label := formatRunningLabel(m.currentTool.Name, m.currentTool.Args)
			liveLines = append(liveLines, bullet+" "+styleMuted.Render(label)+" "+m.spin.View())
			if p := extractToolPath(m.currentTool.Name, m.currentTool.Args); p != "" {
				liveLines = append(liveLines, styleMuted.Render("    ⎿  "+p))
			}
		}
		if len(liveLines) > 0 {
			parts = append(parts, strings.Join(liveLines, "\n"))
		}
	}

	if m.banner != "" {
		var bs lipgloss.Style
		prefix := "  • "
		switch m.bannerKind {
		case "error":
			bs = styleError
			prefix = "  ✗ "
		case "warn":
			bs = styleWarn
			prefix = "  ! "
		case "success":
			bs = styleSuccess
			prefix = "  ✓ "
		default:
			bs = styleMuted
		}
		parts = append(parts, bs.Render(prefix+m.banner))
	}

	return strings.Join(parts, "\n\n")
}

func (m Model) recalcLayout() Model {
	eyesH := len(eyesActing)
	headerH := 1
	sepH := 2
	statusH := 1

	inputH := 6
	if m.showPlanApproval {
		inputH += 2 + len(planApprovalOptions)
	} else if m.pendingAuth != nil {
		inputH += 2 + len(shellApprovalOptions)
	}
	if len(m.cmdItems) > 0 {
		inputH += 4 + len(m.cmdItems)
	}
	if len(m.mentionItems) > 0 {
		inputH += 5 + len(m.mentionItems)
	}

	parallelH := 0
	if m.sidePanelWidth() <= 0 {
		if pa := m.renderParallelAgents(); pa != "" {
			parallelH = lipgloss.Height(pa)
		}
	}

	fixed := headerH + eyesH + sepH + inputH + statusH + parallelH
	contentH := m.height - fixed
	if contentH < 3 {
		contentH = 3
	}
	vpW := m.paneWidth() - 2
	if vpW < 10 {
		vpW = 10
	}

	m.vp.Width = vpW
	m.vp.Height = contentH
	m.ta.SetWidth(m.paneWidth() - 6)

	return m
}

func (m Model) loadSessionSummary(sel session.Summary) (session.State, error) {
	if !sel.Legacy {
		return session.Load(m.store.GlobalDir, sel.ID)
	}
	conv, err := conversation.Load(sel.Path)
	if err != nil {
		return session.State{}, err
	}
	state := session.StateFromLegacy(conv, m.cwd)
	if state.Metadata.ID == "" {
		state.Metadata.ID = session.NewID(m.cwd)
	}
	_ = session.Save(m.store.GlobalDir, state)
	return state, nil
}

func (m Model) updateResume(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.showResume = false
	case "up", "ctrl+p", "shift+tab":
		if m.resumeCursor > 0 {
			m.resumeCursor--
		}
	case "down", "ctrl+n", "tab":
		if m.resumeCursor < len(m.resumeItems)-1 {
			m.resumeCursor++
		}
	case "enter":
		if len(m.resumeItems) > 0 {
			sel := m.resumeItems[m.resumeCursor]
			state, err := m.loadSessionSummary(sel)
			if err != nil {
				m.showResume = false
				m.showBanner("failed to load conversation: "+err.Error(), "error")
				return m, nil
			}
			m.sessionID = state.Metadata.ID
			m.todos = state.Todos
			m.parallelAgents = nil
			m.messages = make([]ChatMessage, 0, len(state.Messages))
			for _, cm := range state.Messages {
				m.messages = append(m.messages, ChatMessage{
					Role:     Role(cm.Role),
					Content:  cm.Content,
					Thinking: cm.Thinking,
					Meta:     cm.Meta,
					At:       cm.At,
				})
			}
			m.showResume = false
			m.refreshViewport()
			m.showBanner(fmt.Sprintf("resumed conversation from %s", state.Metadata.StartedAt.Format("2006-01-02 15:04")), "success")
		}
	}
	return m, nil
}

func (m Model) viewResume() string {
	mc := m.currentColor()
	title := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ resume conversation")

	var rows []string
	for i, s := range m.resumeItems {
		isSelected := i == m.resumeCursor
		timeStr := s.StartedAt.Format("2006-01-02 15:04")
		preview := s.Preview
		if preview == "" {
			preview = "(empty)"
		}
		var prefix string
		var timeStyle, previewStyle lipgloss.Style
		if isSelected {
			prefix = lipgloss.NewStyle().Foreground(mc).Bold(true).Render("› ")
			timeStyle = lipgloss.NewStyle().Foreground(colorText).Bold(true)
			previewStyle = lipgloss.NewStyle().Foreground(colorMuted)
		} else {
			prefix = "  "
			timeStyle = lipgloss.NewStyle().Foreground(colorMuted)
			previewStyle = lipgloss.NewStyle().Foreground(colorDim)
		}
		rows = append(rows, prefix+timeStyle.Render(timeStr)+"  "+previewStyle.Render(preview))
	}
	if len(rows) == 0 {
		rows = append(rows, styleMuted.Render("  no saved conversations"))
	}

	hint := styleMuted.Render("↑↓ navigate  enter load  esc close")
	dialogWidth := 72
	if m.width < dialogWidth+4 {
		dialogWidth = m.width - 4
	}
	if dialogWidth < 30 {
		dialogWidth = 30
	}

	maxRows := m.height - 12
	if maxRows < 4 {
		maxRows = 4
	}
	if len(rows) > maxRows {
		start := m.resumeCursor - maxRows/2
		if start < 0 {
			start = 0
		}
		if start+maxRows > len(rows) {
			start = len(rows) - maxRows
		}
		rows = rows[start : start+maxRows]
	}

	dialog := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(dialogWidth).
		Padding(1, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			title, "",
			strings.Join(rows, "\n"),
			"",
			hint,
		))

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		dialog,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(colorDim),
	)
}

func (m Model) updateTrust(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "ctrl+p", "shift+tab":
		if m.trustCursor > 0 {
			m.trustCursor--
		}
	case "down", "ctrl+n", "tab":
		if m.trustCursor < 2 {
			m.trustCursor++
		}
	case "enter":
		switch m.trustCursor {
		case 0:
			m.showTrust = false
			m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
			m.refreshViewport()
		case 1:
			_ = config.AddTrusted(m.cwd)
			m.showTrust = false
			m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
			m.refreshViewport()
		case 2:
			return m, tea.Quit
		}
	case "1", "y", "Y":
		m.showTrust = false
		m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
		m.refreshViewport()
	case "2":
		_ = config.AddTrusted(m.cwd)
		m.showTrust = false
		m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
		m.refreshViewport()
	case "3", "n", "N", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) viewTrust() string {
	mc := m.currentColor()
	title := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ confirm folder trust")
	pathStyle := lipgloss.NewStyle().Foreground(colorText).Bold(true)
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24"))

	options := []string{
		"Yes, trust this session",
		"Yes, and remember this folder",
		"No, exit",
	}

	var optLines []string
	for i, opt := range options {
		var prefix string
		var style lipgloss.Style
		if i == m.trustCursor {
			prefix = lipgloss.NewStyle().Foreground(mc).Bold(true).Render("› ")
			style = lipgloss.NewStyle().Foreground(colorText).Bold(true)
		} else {
			prefix = "  "
			style = lipgloss.NewStyle().Foreground(colorMuted)
		}
		optLines = append(optLines, prefix+style.Render(fmt.Sprintf("%d  %s", i+1, opt)))
	}

	inner := lipgloss.JoinVertical(lipgloss.Left,
		title, "",
		pathStyle.Render("  "+m.cwd),
		"",
		warnStyle.Render("  Spettro may read files and run commands in this folder."),
		styleMuted.Render("  Only trust folders you own and control."),
		"",
		strings.Join(optLines, "\n"),
		"",
		styleMuted.Render("  ↑↓ navigate  enter confirm  1/2/3 direct select"),
	)

	dialogWidth := 64
	if m.width < dialogWidth+4 {
		dialogWidth = m.width - 4
	}
	if dialogWidth < 30 {
		dialogWidth = 30
	}

	dialog := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(dialogWidth).
		Padding(1, 2).
		Render(inner)

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		dialog,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(colorDim),
	)
}
