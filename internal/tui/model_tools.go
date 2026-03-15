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
			return "Running $ " + cmd + "…"
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
			if item.Status == "error" {
				label = errStyle.Render(label)
			} else {
				label = styleMuted.Render(label)
			}
			lines = append(lines, bullet+" "+label)
			if showTools {
				if p := extractToolPath(name, item.Args); p != "" {
					icon := "✓"
					if item.Status == "error" {
						icon = "✗"
					}
					lines = append(lines, styleMuted.Render(fmt.Sprintf("    ⎿  %s %s", p, icon)))
				}
				if out := trimToolOutput(item.Output, 20); out != "" {
					for _, ol := range strings.Split(out, "\n") {
						lines = append(lines, outputStyle.Render("       "+ol))
					}
				}
			}
		} else {
			label := fmt.Sprintf("%s %s", toolActionVerb(name), toolNounCount(name, count))
			if !showTools {
				label += "  " + styleMuted.Render("(ctrl+o to expand)")
			}
			lines = append(lines, bullet+" "+styleMuted.Render(label))
			if showTools {
				for _, gt := range group {
					var detail string
					if p := extractToolPath(gt.Name, gt.Args); p != "" {
						icon := "✓"
						if gt.Status == "error" {
							icon = "✗"
						}
						detail = fmt.Sprintf("    ⎿  %s %s", p, icon)
					} else {
						detail = "    ⎿  " + formatToolLabel(gt.Name, gt.Args)
					}
					lines = append(lines, styleMuted.Render(detail))
					if out := trimToolOutput(gt.Output, 8); out != "" {
						for _, ol := range strings.Split(out, "\n") {
							lines = append(lines, outputStyle.Render("       "+ol))
						}
					}
				}
			}
		}

		i = j
	}
	return strings.Join(lines, "\n")
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
	order := []string{"plan", "coding", "ask"}
	var primary []string
	for _, id := range order {
		if spec, ok := manifest.AgentByID(id); ok && spec.Enabled {
			primary = append(primary, id)
		}
	}
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
