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

	"spettro/internal/config"
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
		m.showBanner(fmt.Sprintf("current permission: %s", m.cfg.Permission), "info")
		return m, nil
	}
	if len(fields) != 2 {
		m.showBanner("usage: /permissions <yolo|restricted|ask-first>", "error")
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
