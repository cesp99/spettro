package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"spettro/internal/budget"
	"spettro/internal/config"
	"spettro/internal/provider"
)

const (
	toolCallPrefix = "TOOL_CALL"
	finalPrefix    = "FINAL"
)

const codingSystemPromptFallback = `You are a coding agent that can use tools.
Implement the task using minimal safe edits and verify your changes.
Never include chain-of-thought or <think> blocks in output.`

type LLMCoder struct {
	ProviderManager *provider.Manager
	ProviderName    func() string
	ModelName       func() string
	CWD             string
	MaxTokens       int // max tokens per request; 0 = budget.DefaultMax
	RequiredReads   []string
	ToolCallback    func(ToolTrace) // optional: called with status="running" before and final status after each tool
}

func (c LLMCoder) Execute(ctx context.Context, plan string, level config.PermissionLevel, approved bool) (RunResult, error) {
	if strings.TrimSpace(plan) == "" {
		return RunResult{}, fmt.Errorf("empty approved plan")
	}
	if level == config.PermissionAskFirst && !approved {
		return RunResult{}, fmt.Errorf("ask-first policy requires explicit approval")
	}

	systemPrompt := loadPromptOrFallback(c.CWD, "agents/coding.md", codingSystemPromptFallback)
	out, traces, err := runToolLoop(ctx, toolLoopConfig{
		SystemPrompt:    systemPrompt,
		UserTask:        plan,
		CWD:             c.CWD,
		MaxSteps:        24,
		RequireToolCall: true,
		AllowedTools:    []string{"repo-search", "file-read", "file-write", "shell-exec"},
		ProviderManager: c.ProviderManager,
		ProviderName:    c.ProviderName,
		ModelName:       c.ModelName,
		MaxTokens:       c.MaxTokens,
		RequiredReads:   c.RequiredReads,
		ToolCallback:    c.ToolCallback,
	})
	if err != nil {
		return RunResult{}, err
	}
	main, _ := stripThinkTags(out)
	return RunResult{
		Content: strings.TrimSpace(main),
		Tools:   traces,
	}, nil
}

type toolLoopConfig struct {
	SystemPrompt    string
	UserTask        string
	CWD             string
	MaxSteps        int
	RequireToolCall bool
	AllowedTools    []string
	ProviderManager *provider.Manager
	ProviderName    func() string
	ModelName       func() string
	MaxTokens       int // max tokens per request; 0 = budget.DefaultMax
	RequiredReads   []string
	ToolCallback    func(ToolTrace) // optional: called with status="running" before and final status after each tool
}

type toolCall struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

type toolRuntime struct {
	cwd           string
	readSet       map[string]struct{}
	requiredReads map[string]struct{}
	searcher      RepoSearcher
}

func runToolLoop(ctx context.Context, cfg toolLoopConfig) (string, []ToolTrace, error) {
	if cfg.ProviderManager == nil {
		return "", nil, fmt.Errorf("missing provider manager")
	}
	if cfg.ProviderName == nil || cfg.ModelName == nil {
		return "", nil, fmt.Errorf("missing provider/model selectors")
	}
	if strings.TrimSpace(cfg.UserTask) == "" {
		return "", nil, fmt.Errorf("empty task")
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 8
	}

	allowed := make(map[string]struct{}, len(cfg.AllowedTools))
	for _, t := range cfg.AllowedTools {
		allowed[t] = struct{}{}
	}
	runtime := toolRuntime{
		cwd:           cfg.CWD,
		readSet:       map[string]struct{}{},
		requiredReads: map[string]struct{}{},
	}
	for _, p := range cfg.RequiredReads {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p != "" {
			runtime.requiredReads[p] = struct{}{}
		}
	}
	usedTool := false
	var traces []ToolTrace
	var lastContent string // last non-empty response, used as fallback if max steps hit

	var history strings.Builder
	for step := 1; step <= cfg.MaxSteps; step++ {
		prompt := buildLoopPrompt(cfg, history.String(), step)
		if err := budget.Validate(cfg.MaxTokens, prompt); err != nil {
			return "", traces, err
		}
		resp, err := cfg.ProviderManager.Send(ctx, cfg.ProviderName(), cfg.ModelName(), provider.Request{
			Prompt:    prompt,
			MaxTokens: cfg.MaxTokens,
		})
		if err != nil {
			return "", traces, fmt.Errorf("agent call failed: %w", err)
		}

		content := strings.TrimSpace(resp.Content)
		main, _ := stripThinkTags(content)
		main = strings.TrimSpace(main)
		if main == "" {
			continue
		}
		lastContent = main

		call, hasCall, err := parseToolCall(main)
		if err != nil {
			// Malformed tool call JSON: feed the error back so the LLM can correct it.
			history.WriteString(fmt.Sprintf("assistant(%d): %s\n", step, singleLine(main)))
			history.WriteString(fmt.Sprintf("tool(%d): error: %s — fix the JSON and retry\n\n", step, err.Error()))
			continue
		}
		if hasCall {
			usedTool = true
			callArgs := singleLine(string(call.Args))
			if cfg.ToolCallback != nil {
				cfg.ToolCallback(ToolTrace{Name: call.Tool, Args: callArgs, Status: "running"})
			}
			result, err := runtime.execute(ctx, call, allowed)
			status := "success"
			if err != nil {
				status = "error"
				result = "error: " + err.Error()
			}
			trace := ToolTrace{
				Name:   call.Tool,
				Status: status,
				Args:   callArgs,
				Output: truncate(result, 600),
			}
			traces = append(traces, trace)
			if cfg.ToolCallback != nil {
				cfg.ToolCallback(trace)
			}
			history.WriteString(fmt.Sprintf("assistant(%d): %s\n", step, singleLine(main)))
			history.WriteString(fmt.Sprintf("tool(%d): %s\n\n", step, truncate(result, 6000)))
			continue
		}

		if final, ok := parseFinal(main); ok {
			if next, ok := runtime.nextRequiredRead(); ok {
				history.WriteString(fmt.Sprintf("assistant(%d): %s\n", step, singleLine(main)))
				history.WriteString(fmt.Sprintf("system: you must read %q with file-read before FINAL.\n\n", next))
				continue
			}
			if cfg.RequireToolCall && !usedTool {
				// LLM tried to finalize without using any tools: nudge it and retry.
				history.WriteString(fmt.Sprintf("assistant(%d): %s\n", step, singleLine(main)))
				history.WriteString(fmt.Sprintf("system: you must use at least one tool before writing FINAL.\n\n"))
				continue
			}
			return strings.TrimSpace(final), traces, nil
		}

		// Plain text without FINAL prefix and without a tool call.
		if cfg.RequireToolCall {
			history.WriteString(fmt.Sprintf("assistant(%d): %s\n", step, singleLine(main)))
			if !usedTool {
				history.WriteString("system: use TOOL_CALL before providing the final answer.\n\n")
			} else {
				history.WriteString("system: output FINAL on its own line followed by your final answer. Do not write TOOL_CALL as text.\n\n")
			}
			continue
		}
		return main, traces, nil
	}

	// Max steps exhausted: return whatever content we accumulated rather than discarding it.
	if lastContent != "" {
		return lastContent, traces, nil
	}
	return "", traces, fmt.Errorf("max tool steps reached without final answer")
}

func buildLoopPrompt(cfg toolLoopConfig, history string, step int) string {
	toolList := strings.Join(cfg.AllowedTools, ", ")
	base := strings.TrimSpace(cfg.SystemPrompt)
	if base == "" {
		base = "You are an assistant."
	}
	requiredReadsSection := ""
	if len(cfg.RequiredReads) > 0 {
		paths := make([]string, 0, len(cfg.RequiredReads))
		for _, p := range cfg.RequiredReads {
			p = filepath.ToSlash(strings.TrimSpace(p))
			if p != "" {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		if len(paths) > 0 {
			requiredReadsSection = "\nRequired first reads (must be done with file-read before anything else):\n- " + strings.Join(paths, "\n- ")
		}
	}
	return fmt.Sprintf(`%s

You can use tools iteratively.
Allowed tools: %s

Output protocol (strict):
1) To call one tool, output exactly:
TOOL_CALL {"tool":"<tool-name>","args":{...}}
2) When done, output exactly:
FINAL
<your final answer>

Rules:
- No chain-of-thought, no thinking trace, no <think> tags.
- Prefer reading/searching before writing.
- Never edit an existing file unless it has been read first.
- Creating a brand-new file without reading is allowed.
- Keep tool args minimal and valid JSON.
- If a tool fails, adapt and continue.

Task:
%s
%s

Working directory:
%s

Current step: %d/%d

Previous tool interaction log:
%s`, base, toolList, cfg.UserTask, requiredReadsSection, cfg.CWD, step, cfg.MaxSteps, emptyIfBlank(history))
}

func (r *toolRuntime) execute(ctx context.Context, call toolCall, allowed map[string]struct{}) (string, error) {
	if _, ok := allowed[call.Tool]; !ok {
		return "", fmt.Errorf("tool %q not allowed", call.Tool)
	}
	if call.Tool != "file-read" {
		if next, ok := r.nextRequiredRead(); ok {
			return "", fmt.Errorf("must read %q with file-read first", next)
		}
	}
	switch call.Tool {
	case "repo-search":
		var args struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return "", fmt.Errorf("repo-search args: %w", err)
		}
		out, err := r.searcher.Search(ctx, r.cwd, strings.TrimSpace(args.Query))
		if err != nil {
			return "", err
		}
		r.markReadFromSearch(out)
		return truncate(out, 8000), nil
	case "file-read":
		var args struct {
			Path      string `json:"path"`
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return "", fmt.Errorf("file-read args: %w", err)
		}
		abs, rel, err := r.resolvePath(args.Path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", err
		}
		r.readSet[rel] = struct{}{}
		delete(r.requiredReads, rel)
		content := string(data)
		if args.StartLine > 0 {
			content = sliceLines(content, args.StartLine, args.EndLine)
		}
		return truncate(content, 12000), nil
	case "file-write":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Append  bool   `json:"append"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return "", fmt.Errorf("file-write args: %w", err)
		}
		abs, rel, err := r.resolvePath(args.Path)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(args.Path) == "" {
			return "", fmt.Errorf("file-write path is required")
		}
		_, statErr := os.Stat(abs)
		exists := statErr == nil
		if exists {
			if _, ok := r.readSet[rel]; !ok {
				return "", fmt.Errorf("refusing write: read %q first", rel)
			}
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		if args.Append {
			f, err := os.OpenFile(abs, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return "", err
			}
			defer f.Close()
			if _, err := f.WriteString(args.Content); err != nil {
				return "", err
			}
		} else {
			if err := os.WriteFile(abs, []byte(args.Content), 0o644); err != nil {
				return "", err
			}
		}
		r.readSet[rel] = struct{}{}
		if exists {
			return fmt.Sprintf("updated %s", rel), nil
		}
		return fmt.Sprintf("created %s", rel), nil
	case "shell-exec":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return "", fmt.Errorf("shell-exec args: %w", err)
		}
		cmdText := strings.TrimSpace(args.Command)
		if cmdText == "" {
			return "", fmt.Errorf("shell-exec command is required")
		}
		if isBlockedCommand(cmdText) {
			return "", fmt.Errorf("blocked dangerous command")
		}
		cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, "bash", "-lc", cmdText)
		cmd.Dir = r.cwd
		out, err := cmd.CombinedOutput()
		text := truncate(string(out), 12000)
		if err != nil {
			return text, fmt.Errorf("command failed: %w", err)
		}
		return text, nil
	default:
		return "", fmt.Errorf("unsupported tool %q", call.Tool)
	}
}

func (r *toolRuntime) nextRequiredRead() (string, bool) {
	if len(r.requiredReads) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(r.requiredReads))
	for k := range r.requiredReads {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys[0], true
}

func (r *toolRuntime) resolvePath(p string) (abs, rel string, err error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(r.cwd, p))
	}
	rel, err = filepath.Rel(r.cwd, abs)
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path outside workspace is not allowed")
	}
	rel = filepath.ToSlash(rel)
	return abs, rel, nil
}

func (r *toolRuntime) markReadFromSearch(out string) {
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 {
			continue
		}
		if !regexp.MustCompile(`^\d+$`).MatchString(parts[1]) {
			continue
		}
		r.readSet[strings.TrimSpace(parts[0])] = struct{}{}
	}
}

func parseToolCall(s string) (toolCall, bool, error) {
	if !strings.HasPrefix(s, toolCallPrefix) {
		return toolCall{}, false, nil
	}
	raw := strings.TrimSpace(strings.TrimPrefix(s, toolCallPrefix))
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var call toolCall
	if err := json.Unmarshal([]byte(raw), &call); err != nil {
		return toolCall{}, true, fmt.Errorf("invalid tool call JSON: %w", err)
	}
	if strings.TrimSpace(call.Tool) == "" {
		return toolCall{}, true, fmt.Errorf("tool call missing tool name")
	}
	return call, true, nil
}

func parseFinal(s string) (string, bool) {
	if !strings.HasPrefix(s, finalPrefix) {
		return "", false
	}
	out := strings.TrimSpace(strings.TrimPrefix(s, finalPrefix))
	return out, true
}

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

func loadPromptOrFallback(cwd, relative, fallback string) string {
	if strings.TrimSpace(cwd) != "" && strings.TrimSpace(relative) != "" {
		p := filepath.Join(cwd, relative)
		if data, err := os.ReadFile(p); err == nil {
			text := strings.TrimSpace(string(data))
			if text != "" {
				return text
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

func isBlockedCommand(cmd string) bool {
	l := strings.ToLower(cmd)
	blocked := []string{
		"git reset --hard",
		"git checkout --",
		"rm -rf /",
	}
	for _, b := range blocked {
		if strings.Contains(l, b) {
			return true
		}
	}
	return false
}
