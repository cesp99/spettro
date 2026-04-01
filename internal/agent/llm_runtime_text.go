package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func stripThinkTags(content string) (main, thinking string) {
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

// stripFrontmatter removes a YAML frontmatter block (between --- delimiters)
// from content loaded from agent .md files. The system prompt is everything after
// the second --- marker.
func stripFrontmatter(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return content
	}
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx == -1 {
		return content
	}
	return strings.TrimSpace(rest[idx+4:])
}

func loadPromptOrFallback(cwd, relative, fallback string) string {
	if strings.TrimSpace(cwd) != "" && strings.TrimSpace(relative) != "" {
		p := filepath.Join(cwd, relative)
		if data, err := os.ReadFile(p); err == nil {
			text := strings.TrimSpace(string(data))
			if text != "" {
				return stripFrontmatter(text)
			}
		}
	}
	return fallback
}

func sliceLines(content string, start, end int) string {
	lines := strings.Split(content, "\n")
	if start < 1 {
		start = 1
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) || start > end {
		return ""
	}
	var b strings.Builder
	for i := start - 1; i < end; i++ {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, lines[i]))
	}
	return b.String()
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)"
}

func emptyIfBlank(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func isMajorOperationTool(name string) bool {
	switch name {
	case "file-write", "file-edit", "shell-exec", "bash", "bash-output", "agent", "enter-worktree", "exit-worktree":
		return true
	default:
		return false
	}
}

// stripLeakedToolCalls removes any lines that start with TOOL_CALL (which the LLM
// sometimes writes as plain text instead of executing), and trims stray blank lines.
func stripLeakedToolCalls(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), toolCallPrefix) {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
