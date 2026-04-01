package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"spettro/internal/compact"
	"spettro/internal/config"
	"spettro/internal/hooks"
	"spettro/internal/mcp"
	"spettro/internal/session"
)

func (m Model) handleTasksCommand(input string) (tea.Model, tea.Cmd) {
	m.ensureSession()
	fields := strings.Fields(input)
	if len(fields) == 1 || strings.EqualFold(fields[1], "list") {
		m.syncTodosFromSession()
		if len(m.todos) == 0 {
			m.pushSystemMsg("no tasks in this session")
			return m, nil
		}
		var rows []string
		for _, t := range m.todos {
			rows = append(rows, fmt.Sprintf("- [%s] %s (%s)", t.Status, t.Content, t.ID))
		}
		m.pushSystemMsg("tasks:\n" + strings.Join(rows, "\n"))
		return m, nil
	}
	globalDir := m.store.GlobalDir
	switch strings.ToLower(fields[1]) {
	case "add":
		content := strings.TrimSpace(strings.TrimPrefix(input, "/tasks add"))
		if content == "" {
			m.showBanner("usage: /tasks add <content>", "error")
			return m, nil
		}
		item := session.Todo{
			ID:      fmt.Sprintf("task-%d", time.Now().UnixMilli()),
			Content: content,
			Status:  "pending",
			Source:  "command",
		}
		if _, err := session.UpsertTodo(globalDir, m.sessionID, item); err != nil {
			m.showBanner("tasks add failed: "+err.Error(), "error")
			return m, nil
		}
		m.syncTodosFromSession()
		m.showBanner("task added", "success")
	case "done":
		if len(fields) < 3 {
			m.showBanner("usage: /tasks done <id>", "error")
			return m, nil
		}
		id := strings.TrimSpace(fields[2])
		item, ok, err := session.GetTodo(globalDir, m.sessionID, id)
		if err != nil {
			m.showBanner("tasks done failed: "+err.Error(), "error")
			return m, nil
		}
		if !ok {
			m.showBanner("task not found: "+id, "error")
			return m, nil
		}
		item.Status = "completed"
		if _, err := session.UpsertTodo(globalDir, m.sessionID, item); err != nil {
			m.showBanner("tasks done failed: "+err.Error(), "error")
			return m, nil
		}
		m.syncTodosFromSession()
		m.showBanner("task marked completed", "success")
	case "set":
		if len(fields) < 4 {
			m.showBanner("usage: /tasks set <id> <status>", "error")
			return m, nil
		}
		id, st := strings.TrimSpace(fields[2]), strings.TrimSpace(fields[3])
		item, ok, err := session.GetTodo(globalDir, m.sessionID, id)
		if err != nil {
			m.showBanner("tasks set failed: "+err.Error(), "error")
			return m, nil
		}
		if !ok {
			m.showBanner("task not found: "+id, "error")
			return m, nil
		}
		item.Status = st
		if _, err := session.UpsertTodo(globalDir, m.sessionID, item); err != nil {
			m.showBanner("tasks set failed: "+err.Error(), "error")
			return m, nil
		}
		m.syncTodosFromSession()
		m.showBanner("task updated", "success")
	case "show":
		if len(fields) < 3 {
			m.showBanner("usage: /tasks show <id>", "error")
			return m, nil
		}
		id := strings.TrimSpace(fields[2])
		item, ok, err := session.GetTodo(globalDir, m.sessionID, id)
		if err != nil {
			m.showBanner("tasks show failed: "+err.Error(), "error")
			return m, nil
		}
		if !ok {
			m.showBanner("task not found: "+id, "error")
			return m, nil
		}
		raw, _ := json.MarshalIndent(item, "", "  ")
		m.pushSystemMsg(string(raw))
	default:
		m.showBanner("usage: /tasks [list|add|done|set|show]", "info")
	}
	return m, nil
}

func (m Model) handleMCPCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(input)
	if len(fields) < 2 {
		m.showBanner("usage: /mcp <list|read|auth>", "info")
		return m, nil
	}
	switch strings.ToLower(fields[1]) {
	case "list":
		serverID := ""
		if len(fields) >= 3 {
			serverID = fields[2]
		}
		rows, err := mcp.ListResources(m.cwd, serverID)
		if err != nil {
			m.showBanner("mcp list failed: "+err.Error(), "error")
			return m, nil
		}
		if len(rows) == 0 {
			m.pushSystemMsg("no mcp resources configured")
			return m, nil
		}
		raw, _ := json.MarshalIndent(rows, "", "  ")
		m.pushSystemMsg(string(raw))
	case "read":
		if len(fields) < 4 {
			m.showBanner("usage: /mcp read <server_id> <resource_id>", "error")
			return m, nil
		}
		out, err := mcp.ReadResource(m.cwd, fields[2], fields[3])
		if err != nil {
			m.showBanner("mcp read failed: "+err.Error(), "error")
			return m, nil
		}
		m.pushSystemMsg(truncateLabel(out, 6000))
	case "auth":
		if len(fields) < 4 {
			m.showBanner("usage: /mcp auth <server_id> <token>", "error")
			return m, nil
		}
		err := mcp.SaveAuth(m.cwd, mcp.AuthState{
			ServerID:  fields[2],
			Token:     fields[3],
			UpdatedAt: time.Now(),
		})
		if err != nil {
			m.showBanner("mcp auth failed: "+err.Error(), "error")
			return m, nil
		}
		m.showBanner("mcp auth updated", "success")
	default:
		m.showBanner("usage: /mcp <list|read|auth>", "info")
	}
	return m, nil
}

func (m Model) handleSkillsCommand() (tea.Model, tea.Cmd) {
	var skills []string
	localAgents := filepath.Join(m.cwd, "agents")
	_ = filepath.WalkDir(localAgents, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			rel, _ := filepath.Rel(m.cwd, path)
			skills = append(skills, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(skills)
	if len(skills) == 0 {
		m.pushSystemMsg("no local skills/prompts found in agents/")
		return m, nil
	}
	m.pushSystemMsg("available local skills/prompts:\n- " + strings.Join(skills, "\n- "))
	return m, nil
}

func (m Model) handlePlanCommand(input string) (tea.Model, tea.Cmd) {
	task := strings.TrimSpace(strings.TrimPrefix(input, "/plan"))
	if task == "" {
		m.mode = "plan"
		m.persistUIState()
		m.showBanner("switched to plan mode", "success")
		return m, nil
	}
	spec, ok := m.manifest.AgentByID("plan")
	if !ok {
		m.showBanner("plan agent not found", "error")
		return m, nil
	}
	return m.runAgent(spec, task, nil, nil)
}

func (m Model) handlePermissionsCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(input)
	if len(fields) == 1 {
		m.pushSystemMsg(m.permissionSummary())
		return m, nil
	}
	if strings.EqualFold(fields[1], "debug") {
		if len(fields) == 2 {
			if m.cfg.ShowPermissionDebug {
				m.showBanner("permission debug: on", "info")
			} else {
				m.showBanner("permission debug: off", "info")
			}
			return m, nil
		}
		switch strings.ToLower(fields[2]) {
		case "on":
			m.cfg.ShowPermissionDebug = true
		case "off":
			m.cfg.ShowPermissionDebug = false
		default:
			m.showBanner("usage: /permissions debug <on|off>", "error")
			return m, nil
		}
		_ = config.Save(m.cfg)
		if m.cfg.ShowPermissionDebug {
			m.showBanner("permission debug enabled", "success")
		} else {
			m.showBanner("permission debug disabled", "success")
		}
		return m, nil
	}
	if len(fields) != 2 {
		m.showBanner("usage: /permissions <yolo|restricted|ask-first> | /permissions debug <on|off>", "error")
		return m, nil
	}
	level := config.PermissionLevel(fields[1])
	switch level {
	case config.PermissionYOLO, config.PermissionRestricted, config.PermissionAskFirst:
		m.cfg.Permission = level
		_ = config.Save(m.cfg)
		m.showBanner(fmt.Sprintf("permission set to %s", level), "success")
	default:
		m.showBanner("invalid permission", "error")
	}
	return m, nil
}

func (m Model) handleHooksCommand() (tea.Model, tea.Cmd) {
	cfg, err := hooks.LoadEffective(m.cwd)
	if err != nil {
		m.showBanner("hooks load failed: "+err.Error(), "error")
		return m, nil
	}
	if len(cfg.Rules) == 0 {
		m.pushSystemMsg("no hooks configured (project: .spettro/hooks.json, global: ~/.spettro/hooks.json)")
		return m, nil
	}
	var rows []string
	for _, r := range cfg.Rules {
		status := "enabled"
		if !r.Enabled {
			status = "disabled"
		}
		matcher := strings.TrimSpace(r.Matcher)
		if matcher == "" {
			matcher = "*"
		}
		rows = append(rows, fmt.Sprintf("- [%s] %s id=%s matcher=%s source=%s cmd=%q", status, r.Event, r.ID, matcher, r.Source, r.Command))
	}
	if len(cfg.Issues) > 0 {
		rows = append(rows, "", "validation warnings:")
		for _, issue := range cfg.Issues {
			rows = append(rows, fmt.Sprintf("- [%s] %s: %s", issue.Source, issue.ID, issue.Message))
		}
	}
	m.pushSystemMsg("hooks:\n" + strings.Join(rows, "\n"))
	return m, nil
}

func (m Model) permissionSummary() string {
	var rows []string
	rows = append(rows, fmt.Sprintf("current permission: %s", m.cfg.Permission))
	if m.cfg.ShowPermissionDebug {
		rows = append(rows, "permission debug: on")
	} else {
		rows = append(rows, "permission debug: off")
	}
	rows = append(rows, fmt.Sprintf("runtime permission rules: %d", len(m.manifest.Runtime.PermissionRules)))
	if spec, ok := m.manifest.AgentByID(m.mode); ok {
		rows = append(rows, fmt.Sprintf("agent %s rules: %d", spec.ID, len(spec.PermissionRules)))
	}
	if len(m.recentApprovals) == 0 {
		rows = append(rows, "recent approvals: none")
		return strings.Join(rows, "\n")
	}
	rows = append(rows, "", "recent approvals:")
	for i := len(m.recentApprovals) - 1; i >= 0 && i >= len(m.recentApprovals)-5; i-- {
		ev := m.recentApprovals[i]
		seg := strings.TrimSpace(ev.CommandSegment)
		if seg == "" {
			seg = strings.TrimSpace(ev.Task)
		}
		if seg == "" {
			seg = "(unknown command)"
		}
		rows = append(rows, fmt.Sprintf("- %s [%s] via %s: %s", ev.Decision, ev.ToolID, ev.DecisionSource, truncateLabel(seg, 90)))
	}
	return strings.Join(rows, "\n")
}

func (m Model) handleCompactCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(input)
	if len(fields) == 1 {
		return m.runCompact("")
	}
	if len(fields) >= 2 && strings.EqualFold(fields[1], "auto") {
		if len(fields) == 2 || strings.EqualFold(fields[2], "status") {
			state := "off"
			if m.cfg.AutoCompactEnabled {
				state = "on"
			}
			m.showBanner(fmt.Sprintf("auto compact: %s (failures: %d/%d)", state, m.autoCompactFailures, m.cfg.AutoCompactMaxFailures), "info")
			return m, nil
		}
		switch strings.ToLower(fields[2]) {
		case "on":
			m.cfg.AutoCompactEnabled = true
			m.autoCompactFailures = 0
			_ = config.Save(m.cfg)
			m.showBanner("auto compact enabled", "success")
		case "off":
			m.cfg.AutoCompactEnabled = false
			_ = config.Save(m.cfg)
			m.showBanner("auto compact disabled", "success")
		default:
			m.showBanner("usage: /compact auto <status|on|off>", "error")
		}
		return m, nil
	}
	if len(fields) >= 2 && strings.EqualFold(fields[1], "policy") {
		window := m.contextWindow()
		if window == 0 {
			window = contextWindowDefault(m.cfg.ActiveProvider)
		}
		eval := compact.Evaluate(window, compact.Config{
			AutoEnabled:      m.cfg.AutoCompactEnabled,
			AutoThresholdPct: m.cfg.AutoCompactThresholdPct,
			MaxFailures:      m.cfg.AutoCompactMaxFailures,
		}, compact.State{
			TokensUsed:          m.totalTokensUsed,
			ConsecutiveFailures: m.autoCompactFailures,
		})
		reason := strings.TrimSpace(eval.AutoDisabledReason)
		if reason == "" {
			reason = "none"
		}
		m.pushSystemMsg(fmt.Sprintf(
			"compact policy:\n- context window: %d\n- effective window: %d\n- warning threshold: %d\n- error threshold: %d\n- auto threshold: %d\n- blocking limit: %d\n- auto enabled: %t\n- consecutive failures: %d/%d\n- auto disabled reason: %s",
			window,
			eval.EffectiveWindow,
			eval.WarningThreshold,
			eval.ErrorThreshold,
			eval.AutoCompactThreshold,
			eval.BlockingLimit,
			m.cfg.AutoCompactEnabled,
			m.autoCompactFailures,
			m.cfg.AutoCompactMaxFailures,
			reason,
		))
		return m, nil
	}
	focus := strings.TrimSpace(strings.TrimPrefix(input, fields[0]))
	return m.runCompact(focus)
}
