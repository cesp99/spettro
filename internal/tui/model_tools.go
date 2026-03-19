package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"spettro/internal/agent"
	"spettro/internal/config"
)

func stripThinking(content string) (main, thinking string) {
	var sb, tb strings.Builder
	remaining := content
	for {
		start := strings.Index(remaining, "<think>")
		if start == -1 {
			sb.WriteString(remaining)
			break
		}
		sb.WriteString(remaining[:start])
		remaining = remaining[start+len("<think>"):]
		end := strings.Index(remaining, "</think>")
		if end == -1 {
			tb.WriteString(remaining)
			break
		}
		tb.WriteString(remaining[:end])
		remaining = remaining[end+len("</think>"):]
	}
	return strings.TrimSpace(sb.String()), strings.TrimSpace(tb.String())
}

func waitForTool(ch chan agent.ToolTrace) tea.Cmd {
	return func() tea.Msg {
		t, ok := <-ch
		if !ok {
			return nil
		}
		return toolProgressMsg{trace: t}
	}
}

func waitForShellApproval(ch chan shellApprovalRequestMsg) tea.Cmd {
	return func() tea.Msg {
		req, ok := <-ch
		if !ok {
			return nil
		}
		return req
	}
}

func (m Model) renderApprovalPicker(title string, options []string, cursor int, mc lipgloss.Color) string {
	var sb strings.Builder
	sb.WriteString(styleMuted.Render("  "+title) + "\n")
	for i, opt := range options {
		if i == cursor {
			sb.WriteString(lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  › " + opt))
		} else {
			sb.WriteString(styleMuted.Render("    " + opt))
		}
		if i < len(options)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func formatToolLabel(name, argsJSON string) string {
	switch name {
	case "file-read":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
			return "Read " + args.Path
		}
		return "Read file"
	case "file-write":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
			return "Write " + args.Path
		}
		return "Write file"
	case "repo-search":
		var args struct {
			Query string `json:"query"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Query != "" {
			q := truncateLabel(args.Query, 50)
			return fmt.Sprintf("Search %q", q)
		}
		return "Search"
	case "shell-exec":
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Command != "" {
			cmd := truncateLabel(args.Command, 60)
			return "$ " + cmd
		}
		return "Run command"
	case "glob":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			p := truncateLabel(args.Pattern, 50)
			return fmt.Sprintf("Glob %q", p)
		}
		return "Glob"
	case "grep":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			p := truncateLabel(args.Pattern, 50)
			return fmt.Sprintf("Grep %q", p)
		}
		return "Grep"
	}
	return name
}

func formatRunningLabel(name, argsJSON string) string {
	switch name {
	case "file-read":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
			return "Reading " + args.Path + "…"
		}
		return "Reading…"
	case "file-write":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
			return "Writing " + args.Path + "…"
		}
		return "Writing…"
	case "repo-search":
		var args struct {
			Query string `json:"query"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Query != "" {
			q := truncateLabel(args.Query, 50)
			return fmt.Sprintf("Searching %q…", q)
		}
		return "Searching…"
	case "shell-exec":
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Command != "" {
			cmd := truncateLabel(args.Command, 60)
			return "Run $ " + cmd + "…"
		}
		return "Running…"
	case "glob":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			p := truncateLabel(args.Pattern, 50)
			return fmt.Sprintf("Globbing %q…", p)
		}
		return "Globbing…"
	case "grep":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			p := truncateLabel(args.Pattern, 50)
			return fmt.Sprintf("Grepping %q…", p)
		}
		return "Grepping…"
	}
	return name + "…"
}

func extractToolPath(name, argsJSON string) string {
	switch name {
	case "file-read", "file-write":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil {
			return args.Path
		}
	}
	return ""
}

func toolActionVerb(name string) string {
	switch name {
	case "file-read":
		return "Read"
	case "file-write":
		return "Write"
	case "repo-search":
		return "Search"
	case "shell-exec":
		return "Run"
	case "glob":
		return "Glob"
	case "grep":
		return "Grep"
	}
	return name
}

func toolNounCount(name string, count int) string {
	switch name {
	case "file-read", "file-write":
		if count == 1 {
			return "1 file"
		}
		return fmt.Sprintf("%d files", count)
	case "repo-search":
		if count == 1 {
			return "1 search"
		}
		return fmt.Sprintf("%d searches", count)
	case "shell-exec":
		if count == 1 {
			return "1 command"
		}
		return fmt.Sprintf("%d commands", count)
	case "glob", "grep":
		if count == 1 {
			return "1 pattern"
		}
		return fmt.Sprintf("%d patterns", count)
	}
	if count == 1 {
		return "1 call"
	}
	return fmt.Sprintf("%d calls", count)
}

func summarizeToolArgs(name, argsJSON string) string {
	switch name {
	case "file-read":
		var args struct {
			Path      string `json:"path"`
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil {
			if args.Path == "" {
				return "Reads a file from the workspace."
			}
			if args.StartLine > 0 || args.EndLine > 0 {
				return fmt.Sprintf("Reads %s (lines %d-%d).", args.Path, args.StartLine, args.EndLine)
			}
			return fmt.Sprintf("Reads %s.", args.Path)
		}
	case "file-write":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
			return fmt.Sprintf("Writes %s.", args.Path)
		}
	case "repo-search":
		var args struct {
			Query string `json:"query"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil {
			if args.Query == "" {
				return "Scans the repository structure."
			}
			return fmt.Sprintf("Searches the repository for %q.", truncateLabel(args.Query, 80))
		}
	case "shell-exec", "bash":
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Command != "" {
			return fmt.Sprintf("Runs `%s`.", truncateLabel(args.Command, 120))
		}
	case "glob":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			return fmt.Sprintf("Finds files matching %q.", truncateLabel(args.Pattern, 100))
		}
	case "grep":
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			if args.Path != "" {
				return fmt.Sprintf("Searches %s for %q.", args.Path, truncateLabel(args.Pattern, 100))
			}
			return fmt.Sprintf("Searches file contents for %q.", truncateLabel(args.Pattern, 100))
		}
	case "agent":
		var args struct {
			Agent  string `json:"agent"`
			Target string `json:"target"`
			ID     string `json:"id"`
			Task   string `json:"task"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil {
			label := args.Agent
			if label == "" {
				label = args.Target
			}
			if label == "" {
				label = args.ID
			}
			if label == "" {
				label = "sub-agent"
			}
			if args.Task != "" {
				return fmt.Sprintf("Delegates to %s for %s.", label, truncateLabel(args.Task, 100))
			}
			return fmt.Sprintf("Delegates to %s.", label)
		}
	}
	if strings.TrimSpace(argsJSON) == "" {
		return ""
	}
	return truncateLabel(argsJSON, 120)
}

func sanitizeToolOutput(output string, maxLines int) string {
	output = stripToolCallLines(output)
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	if pretty, ok := formatSubagentEnvelope(output); ok {
		return trimToolOutput(pretty, maxLines)
	}
	return trimToolOutput(output, maxLines)
}

func formatSubagentEnvelope(output string) (string, bool) {
	var payload struct {
		Agent          string `json:"agent"`
		Status         string `json:"status"`
		Summary        string `json:"summary"`
		ToolTraceCount int    `json:"tool_trace_count"`
		TokensUsed     int    `json:"tokens_used"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return "", false
	}
	if strings.TrimSpace(payload.Agent) == "" && strings.TrimSpace(payload.Summary) == "" {
		return "", false
	}
	lines := []string{}
	if payload.Agent != "" {
		lines = append(lines, fmt.Sprintf("sub-agent: %s", payload.Agent))
	}
	if payload.Status != "" {
		lines = append(lines, fmt.Sprintf("status: %s", payload.Status))
	}
	if payload.ToolTraceCount > 0 || payload.TokensUsed > 0 {
		lines = append(lines, fmt.Sprintf("tools: %d  tokens: %d", payload.ToolTraceCount, payload.TokensUsed))
	}
	if strings.TrimSpace(payload.Summary) != "" {
		lines = append(lines, "summary:")
		lines = append(lines, payload.Summary)
	}
	return strings.Join(lines, "\n"), true
}

func stripToolCallLines(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "TOOL_CALL") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func renderToolGroups(tools []ToolItem, showTools bool, mc lipgloss.Color) string {
	if len(tools) == 0 {
		return ""
	}
	bullet := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  ●")
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	outputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4B5563")).Italic(true)
	var lines []string

	i := 0
	for i < len(tools) {
		j := i
		for j < len(tools) && tools[j].Name == tools[i].Name {
			j++
		}
		group := tools[i:j]
		count := len(group)
		name := group[0].Name

		if count == 1 {
			item := group[0]
			label := formatToolLabel(name, item.Args)
			if item.Status == "running" {
				label = formatRunningLabel(name, item.Args)
				label = styleMuted.Render(label)
			} else if item.Status == "error" {
				label = errStyle.Render(label)
			} else {
				label = styleMuted.Render(label)
			}
			lines = append(lines, bullet+" "+label)
			if showTools {
				if p := extractToolPath(name, item.Args); p != "" {
					icon := "✓"
					if item.Status == "running" {
						icon = ""
					} else if item.Status == "error" {
						icon = "✗"
					}
					line := fmt.Sprintf("    ⎿  %s", p)
					if icon != "" {
						line += " " + icon
					}
					lines = append(lines, styleMuted.Render(line))
				}
				if item.Status != "running" {
					if out := trimToolOutput(item.Output, 20); out != "" {
						for _, ol := range strings.Split(out, "\n") {
							lines = append(lines, outputStyle.Render("       "+ol))
						}
					}
				}
			}
		} else {
			label := fmt.Sprintf("%s %s", toolActionVerb(name), toolNounCount(name, count))
			if hasRunningTool(group) {
				label = formatRunningToolGroupLabel(name, count)
			}
			if !showTools {
				label += "  " + styleMuted.Render("(ctrl+o to expand)")
			}
			lines = append(lines, bullet+" "+styleMuted.Render(label))
			if showTools {
				for _, gt := range group {
					var detail string
					if p := extractToolPath(gt.Name, gt.Args); p != "" {
						icon := "✓"
						if gt.Status == "running" {
							icon = ""
						} else if gt.Status == "error" {
							icon = "✗"
						}
						detail = "    ⎿  " + p
						if icon != "" {
							detail += " " + icon
						}
					} else {
						if gt.Status == "running" {
							detail = "    ⎿  " + formatRunningLabel(gt.Name, gt.Args)
						} else {
							detail = "    ⎿  " + formatToolLabel(gt.Name, gt.Args)
						}
					}
					lines = append(lines, styleMuted.Render(detail))
					if gt.Status != "running" {
						if out := trimToolOutput(gt.Output, 8); out != "" {
							for _, ol := range strings.Split(out, "\n") {
								lines = append(lines, outputStyle.Render("       "+ol))
							}
						}
					}
				}
			}
		}

		i = j
	}
	return strings.Join(lines, "\n")
}

func hasRunningTool(items []ToolItem) bool {
	for _, item := range items {
		if item.Status == "running" {
			return true
		}
	}
	return false
}

func formatRunningToolGroupLabel(name string, count int) string {
	switch name {
	case "file-read":
		if count == 1 {
			return "Reading 1 file…"
		}
		return fmt.Sprintf("Reading %d files…", count)
	case "file-write":
		if count == 1 {
			return "Writing 1 file…"
		}
		return fmt.Sprintf("Writing %d files…", count)
	case "repo-search":
		if count == 1 {
			return "Searching 1 query…"
		}
		return fmt.Sprintf("Searching %d queries…", count)
	case "shell-exec", "bash":
		if count == 1 {
			return "Running 1 command…"
		}
		return fmt.Sprintf("Running %d commands…", count)
	case "glob":
		if count == 1 {
			return "Globbing 1 pattern…"
		}
		return fmt.Sprintf("Globbing %d patterns…", count)
	case "grep":
		if count == 1 {
			return "Grepping 1 pattern…"
		}
		return fmt.Sprintf("Grepping %d patterns…", count)
	}
	return fmt.Sprintf("%s %d call(s)…", toolActionVerb(name), count)
}

func trimToolOutput(output string, maxLines int) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	if len(lines) <= maxLines {
		return output
	}
	remaining := len(lines) - maxLines
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n  … %d more lines", remaining)
}

func toToolItems(traces []agent.ToolTrace) []ToolItem {
	if len(traces) == 0 {
		return nil
	}
	out := make([]ToolItem, 0, len(traces))
	for _, t := range traces {
		out = append(out, ToolItem{
			Name:   t.Name,
			Status: t.Status,
			Args:   t.Args,
			Output: t.Output,
		})
	}
	return out
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func truncateLabel(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func nextAgent(manifest config.AgentManifest, current string) string {
	primary := primaryAgentIDs(manifest)
	if len(primary) == 0 {
		primary = []string{"plan", "coding", "ask"}
	}
	for i, id := range primary {
		if id == current {
			return primary[(i+1)%len(primary)]
		}
	}
	return primary[0]
}

func primaryAgentIDs(manifest config.AgentManifest) []string {
	preferred := []string{"plan", "coding", "ask"}
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(manifest.Agents))
	for _, id := range preferred {
		if spec, ok := manifest.AgentByID(id); ok && spec.Enabled && spec.IsPrimaryRole() {
			ids = append(ids, id)
			seen[id] = struct{}{}
		}
	}
	for _, spec := range manifest.Agents {
		if !spec.Enabled || !spec.IsPrimaryRole() {
			continue
		}
		if _, ok := seen[spec.ID]; ok {
			continue
		}
		ids = append(ids, spec.ID)
		seen[spec.ID] = struct{}{}
	}
	return ids
}

func nextMode(mode string) string {
	switch mode {
	case "plan":
		return "coding"
	case "coding":
		return "ask"
	default:
		return "plan"
	}
}

func prevMode(mode string) string {
	switch mode {
	case "plan":
		return "ask"
	case "coding":
		return "plan"
	default:
		return "coding"
	}
}
