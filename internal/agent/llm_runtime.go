package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"spettro/internal/budget"
	"spettro/internal/config"
	"spettro/internal/provider"
	"spettro/internal/session"
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
	MaxTokens       int // max tokens per request; 0 = unlimited
	RequiredReads   []string
	ToolCallback    func(ToolTrace) // optional: called with status="running" before and final status after each tool
	ShellApproval   ShellApprovalCallback
}

type ShellApprovalDecision string

const (
	ShellApprovalAllowOnce   ShellApprovalDecision = "allow-once"
	ShellApprovalAllowAlways ShellApprovalDecision = "allow-always"
	ShellApprovalDeny        ShellApprovalDecision = "deny"
)

type ShellApprovalRequest struct {
	Command string
}

type ShellApprovalCallback func(context.Context, ShellApprovalRequest) (ShellApprovalDecision, error)

func (c LLMCoder) Execute(ctx context.Context, plan string, level config.PermissionLevel, approved bool) (RunResult, error) {
	if strings.TrimSpace(plan) == "" {
		return RunResult{}, fmt.Errorf("empty approved plan")
	}
	if level == config.PermissionAskFirst && !approved {
		return RunResult{}, fmt.Errorf("ask-first policy requires explicit approval")
	}

	systemPrompt := loadPromptOrFallback(c.CWD, "agents/coding.md", codingSystemPromptFallback)
	out, traces, tokens, err := runToolLoop(ctx, toolLoopConfig{
		SystemPrompt:    systemPrompt,
		UserTask:        plan,
		CWD:             c.CWD,
		MaxSteps:        24,
		RequireToolCall: true,
		AllowedTools:    []string{"repo-search", "file-read", "file-write", "shell-exec", "glob", "grep"},
		LogToolCalls:    true,
		ProviderManager: c.ProviderManager,
		ProviderName:    c.ProviderName,
		ModelName:       c.ModelName,
		MaxTokens:       c.MaxTokens,
		RequiredReads:   c.RequiredReads,
		ToolCallback:    c.ToolCallback,
		Permission:      level,
		ShellApproval:   c.ShellApproval,
	})
	if err != nil {
		return RunResult{}, err
	}
	main, _ := stripThinkTags(out)
	return RunResult{
		Content:    strings.TrimSpace(main),
		Tools:      traces,
		TokensUsed: tokens,
	}, nil
}

type toolLoopConfig struct {
	SystemPrompt    string
	UserTask        string
	CWD             string
	AgentID         string
	MaxSteps        int
	RequireToolCall bool
	AllowedTools    []string
	ToolPolicies    map[string]config.ToolSpec
	LogToolCalls    bool
	ProviderManager *provider.Manager
	ProviderName    func() string
	ModelName       func() string
	MaxTokens       int // max tokens per request; 0 = unlimited
	RequiredReads   []string
	Images          []string        // only used on first LLM call (chat use case)
	ToolCallback    func(ToolTrace) // optional: called with status="running" before and final status after each tool
	Permission      config.PermissionLevel
	ShellApproval   ShellApprovalCallback
	Manifest        *config.AgentManifest
	SessionDir      string
	DelegationDepth int
	ParentAgentID   string
	MaxWorkers      int
	MaxMicroagents  int
	MaxDepth        int
}

type toolCall struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

var (
	allowedToolCallKeys = map[string]struct{}{
		"tool":       {},
		"name":       {},
		"args":       {},
		"arguments":  {},
		"input":      {},
		"parameters": {},
		"tool_input": {},
		"function":   {},
		"type":       {},
		"id":         {},
		"call_id":    {},
	}
	allowedFunctionKeys = map[string]struct{}{
		"name":      {},
		"arguments": {},
	}
)

type toolRuntime struct {
	cwd           string
	mu            sync.Mutex
	shellMu       sync.Mutex
	readSet       map[string]struct{}
	requiredReads map[string]struct{}
	searcher      RepoSearcher
	permission    config.PermissionLevel
	shellApproval ShellApprovalCallback
	allowedShell  map[string]struct{}
	toolPolicies  map[string]config.ToolSpec
	logToolCalls  bool
	runtimeRules  []config.PermissionRule
	agentRules    []config.PermissionRule
	// sub-agent support
	manifest     *config.AgentManifest
	providerMgr  *provider.Manager
	providerName func() string
	modelName    func() string
	maxTokens    int
	toolCallback func(ToolTrace)
	sessionDir   string
	agentID      string
	parentID     string

	delegationDepth      int
	maxParallelWorkers   int
	maxParallelMicroagnt int
	maxDelegationDepth   int
}

// parallelResult holds the outcome of a single tool execution in a parallel batch.
type parallelResult struct {
	name   string
	args   string
	output string
	status string
}

func runToolLoop(ctx context.Context, cfg toolLoopConfig) (string, []ToolTrace, int, error) {
	if cfg.ProviderManager == nil {
		return "", nil, 0, fmt.Errorf("missing provider manager")
	}
	if cfg.ProviderName == nil || cfg.ModelName == nil {
		return "", nil, 0, fmt.Errorf("missing provider/model selectors")
	}
	if strings.TrimSpace(cfg.UserTask) == "" {
		return "", nil, 0, fmt.Errorf("empty task")
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 8
	}

	var totalTokens int
	allowed := make(map[string]struct{}, len(cfg.AllowedTools))
	for _, t := range cfg.AllowedTools {
		allowed[t] = struct{}{}
		if spec, ok := cfg.ToolPolicies[t]; ok {
			for _, alias := range spec.Aliases {
				alias = strings.TrimSpace(alias)
				if alias != "" {
					allowed[alias] = struct{}{}
				}
			}
		}
	}
	runtime := toolRuntime{
		cwd:             cfg.CWD,
		readSet:         map[string]struct{}{},
		requiredReads:   map[string]struct{}{},
		permission:      cfg.Permission,
		shellApproval:   cfg.ShellApproval,
		allowedShell:    map[string]struct{}{},
		toolPolicies:    map[string]config.ToolSpec{},
		logToolCalls:    cfg.LogToolCalls,
		manifest:        cfg.Manifest,
		providerMgr:     cfg.ProviderManager,
		providerName:    cfg.ProviderName,
		modelName:       cfg.ModelName,
		maxTokens:       cfg.MaxTokens,
		toolCallback:    cfg.ToolCallback,
		sessionDir:      cfg.SessionDir,
		agentID:         cfg.AgentID,
		parentID:        cfg.ParentAgentID,
		delegationDepth: cfg.DelegationDepth,
	}
	if !cfg.LogToolCalls {
		runtime.logToolCalls = false
	}
	if cfg.Manifest != nil {
		runtime.runtimeRules = append(runtime.runtimeRules, cfg.Manifest.Runtime.PermissionRules...)
		if spec, ok := cfg.Manifest.AgentByID(cfg.AgentID); ok {
			runtime.agentRules = append(runtime.agentRules, spec.PermissionRules...)
		}
	}
	for id, spec := range cfg.ToolPolicies {
		runtime.toolPolicies[id] = spec
		for _, alias := range spec.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				runtime.toolPolicies[alias] = spec
			}
		}
	}
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 4
	}
	if cfg.MaxMicroagents <= 0 {
		cfg.MaxMicroagents = 2
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 2
	}
	runtime.maxParallelWorkers = cfg.MaxWorkers
	runtime.maxParallelMicroagnt = cfg.MaxMicroagents
	runtime.maxDelegationDepth = cfg.MaxDepth
	allowedShell, err := loadAllowedCommandSet(cfg.CWD)
	if err != nil {
		return "", nil, 0, err
	}
	runtime.allowedShell = allowedShell
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
			return "", traces, totalTokens, err
		}
		req := provider.Request{
			Prompt:    prompt,
			MaxTokens: cfg.MaxTokens,
		}
		if step == 1 && len(cfg.Images) > 0 {
			req.Images = cfg.Images
		}
		resp, err := cfg.ProviderManager.Send(ctx, cfg.ProviderName(), cfg.ModelName(), req)
		if err != nil {
			return "", traces, totalTokens, fmt.Errorf("agent call failed: %w", err)
		}
		totalTokens += resp.EstimatedTokens

		content := strings.TrimSpace(resp.Content)
		main, _ := stripThinkTags(content)
		main = strings.TrimSpace(main)
		if main == "" {
			continue
		}
		lastContent = main

		calls, parseErrs := parseAllToolCalls(main)
		if len(calls) > 0 || len(parseErrs) > 0 {
			if len(calls) > 0 {
				usedTool = true
			}
			results := runtime.parallelExec(ctx, calls, allowed, cfg.ToolCallback)
			history.WriteString(fmt.Sprintf("assistant(%d): %s\n", step, singleLine(main)))
			for _, res := range results {
				trace := ToolTrace{Name: res.name, Status: res.status, Args: res.args, Output: truncate(res.output, 600)}
				traces = append(traces, trace)
				if runtime.logToolCalls {
					history.WriteString(fmt.Sprintf("tool(%d)[%s]: %s\n", step, res.name, summarizeLoopToolResult(res.name, res.args, res.status, res.output)))
				}
			}
			if !runtime.logToolCalls {
				history.WriteString(fmt.Sprintf("tool(%d): %d calls completed\n", step, len(results)))
			}
			for _, perr := range parseErrs {
				history.WriteString(fmt.Sprintf("tool(%d): parse error: %s — fix the JSON and retry\n", step, perr))
			}
			history.WriteString("\n")
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
			return strings.TrimSpace(final), traces, totalTokens, nil
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
		return main, traces, totalTokens, nil
	}

	// Max steps exhausted: return whatever content we accumulated rather than discarding it.
	if lastContent != "" {
		return lastContent, traces, totalTokens, nil
	}
	return "", traces, totalTokens, fmt.Errorf("max tool steps reached without final answer")
}

func summarizeLoopToolResult(name, args, status, output string) string {
	var parts []string
	status = strings.TrimSpace(status)
	if status != "" {
		parts = append(parts, "status="+status)
	}
	if summary := summarizeLoopToolArgs(name, args); summary != "" {
		parts = append(parts, summary)
	}
	output = strings.TrimSpace(output)
	if output != "" {
		output = strings.Join(strings.Fields(output), " ")
		parts = append(parts, "output="+truncate(output, 240))
	}
	return strings.Join(parts, " | ")
}

func summarizeLoopToolArgs(name, args string) string {
	switch name {
	case "file-read", "file-write":
		var payload struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(args), &payload) == nil && payload.Path != "" {
			return "path=" + payload.Path
		}
	case "repo-search":
		var payload struct {
			Query string `json:"query"`
		}
		if json.Unmarshal([]byte(args), &payload) == nil && payload.Query != "" {
			return "query=" + truncate(payload.Query, 120)
		}
	case "shell-exec", "bash":
		var payload struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(args), &payload) == nil && payload.Command != "" {
			return "command=" + truncate(payload.Command, 120)
		}
	case "glob":
		var payload struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(args), &payload) == nil && payload.Pattern != "" {
			return "pattern=" + truncate(payload.Pattern, 120)
		}
	case "grep":
		var payload struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if json.Unmarshal([]byte(args), &payload) == nil {
			if payload.Path != "" {
				return "path=" + payload.Path + " pattern=" + truncate(payload.Pattern, 120)
			}
			if payload.Pattern != "" {
				return "pattern=" + truncate(payload.Pattern, 120)
			}
		}
	}
	return truncate(strings.TrimSpace(args), 120)
}

func buildLoopPrompt(cfg toolLoopConfig, history string, step int) string {
	toolList := strings.Join(cfg.AllowedTools, ", ")
	base := strings.TrimSpace(cfg.SystemPrompt)
	if base == "" {
		base = "You are an assistant."
	}
	commentGuidance := ""
	for _, tool := range cfg.AllowedTools {
		if tool == "comment" {
			commentGuidance = "\n- Use the comment tool to narrate meaningful progress in the chat.\n- Before major operations (file-write, shell/batch commands, sub-agent delegation), emit a short comment about what you are about to do.\n- After major operations, emit a short success/failure comment including what happened.\n- Prefer a small number of useful comments over narrating every single tool call.\n- Do not narrate with plain text when you still plan to continue; use comment for progress updates and FINAL only when actually done."
			break
		}
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
1) To call tools (all executed in parallel), output one TOOL_CALL per line:
TOOL_CALL {"name":"<tool-name>","arguments":{...}}
TOOL_CALL {"name":"<another>","arguments":{...}}
2) When done, output exactly:
FINAL
<your final answer>

Rules:
- Known aliases accepted by runtime: tool/args and function{name,arguments}.
- For the agent tool, arguments must include {"agent":"<handoff-id>","task":"..."}.
- Prefer reading/searching before writing.
- Never edit an existing file unless it has been read first.
- Creating a brand-new file without reading is allowed.
- Keep tool args minimal and valid JSON.
- If a tool fails, adapt and continue.
%s

Task:
%s
%s

Working directory:
%s

Current step: %d/%d

Previous tool interaction log:
%s`, base, toolList, commentGuidance, cfg.UserTask, requiredReadsSection, cfg.CWD, step, cfg.MaxSteps, emptyIfBlank(history))
}

// parallelExec fires one goroutine per call and collects results in original order.
func (r *toolRuntime) parallelExec(ctx context.Context, calls []toolCall, allowed map[string]struct{}, callback func(ToolTrace)) []parallelResult {
	results := make([]parallelResult, len(calls))
	agentBudget := r.maxParallelWorkers
	if r.delegationDepth > 0 {
		agentBudget = r.maxParallelMicroagnt
	}
	agentCalls := 0
	var wg sync.WaitGroup
	for i, call := range calls {
		if call.Tool == "agent" {
			agentCalls++
			if agentCalls > agentBudget {
				results[i] = parallelResult{
					name:   call.Tool,
					args:   singleLine(string(call.Args)),
					output: fmt.Sprintf("error: delegation limit reached (max %d in parallel)", agentBudget),
					status: "error",
				}
				continue
			}
		}
		wg.Add(1)
		go func(idx int, c toolCall) {
			defer wg.Done()
			callArgs := singleLine(string(c.Args))
			if callback != nil && isMajorOperationTool(c.Tool) {
				msg := fmt.Sprintf("Starting %s (%s).", c.Tool, summarizeLoopToolArgs(c.Tool, callArgs))
				callback(ToolTrace{Name: "comment", Status: "success", Args: fmt.Sprintf(`{"message":%q}`, msg), Output: msg})
			}
			if callback != nil {
				callback(ToolTrace{Name: c.Tool, Args: callArgs, Status: "running"})
			}
			output, err := r.executeWithTimeout(ctx, c, allowed)
			status := "success"
			if err != nil {
				status = "error"
				output = "error: " + err.Error()
			}
			results[idx] = parallelResult{
				name:   c.Tool,
				args:   callArgs,
				output: output,
				status: status,
			}
			if callback != nil {
				callback(ToolTrace{Name: c.Tool, Status: status, Args: callArgs, Output: truncate(output, 600)})
				if isMajorOperationTool(c.Tool) {
					msg := fmt.Sprintf("Completed %s.", c.Tool)
					if err != nil {
						msg = fmt.Sprintf("Failed %s: %s", c.Tool, truncate(err.Error(), 180))
					}
					callback(ToolTrace{Name: "comment", Status: "success", Args: fmt.Sprintf(`{"message":%q}`, msg), Output: msg})
				}
			}
		}(i, call)
	}
	wg.Wait()
	return results
}

func (r *toolRuntime) executeWithTimeout(ctx context.Context, call toolCall, allowed map[string]struct{}) (string, error) {
	timeoutSec := 45
	if spec, ok := r.toolPolicies[call.Tool]; ok && spec.TimeoutSec > 0 {
		timeoutSec = spec.TimeoutSec
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	return r.execute(tctx, call, allowed)
}

func (r *toolRuntime) execute(ctx context.Context, call toolCall, allowed map[string]struct{}) (string, error) {
	if _, ok := allowed[call.Tool]; !ok {
		return "", fmt.Errorf("tool %q not allowed", call.Tool)
	}
	if spec, ok := r.toolPolicies[call.Tool]; ok {
		if evaluatePermissionRule("tool", spec.ID, r.runtimeRules, r.agentRules, spec.PermissionRules) == config.RuleDeny {
			return "", fmt.Errorf("tool %q denied by policy", call.Tool)
		}
		for _, fam := range toolPermissionFamilies(spec) {
			if evaluatePermissionRule(fam, spec.ID, r.runtimeRules, r.agentRules, spec.PermissionRules) == config.RuleDeny {
				return "", fmt.Errorf("tool %q denied by policy for permission %q", call.Tool, fam)
			}
		}
	}
	if call.Tool != "file-read" && call.Tool != "glob" && call.Tool != "grep" {
		if next, ok := r.nextRequiredRead(); ok {
			return "", fmt.Errorf("must read %q with file-read first", next)
		}
	}
	switch call.Tool {
	case "repo-search":
		var args struct {
			Query string `json:"query"`
		}
		if err := decodeJSONStrict(call.Args, &args); err != nil {
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
		if err := decodeJSONStrict(call.Args, &args); err != nil {
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
		r.mu.Lock()
		r.readSet[rel] = struct{}{}
		delete(r.requiredReads, rel)
		r.mu.Unlock()
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
		if err := decodeJSONStrict(call.Args, &args); err != nil {
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
			r.mu.Lock()
			_, alreadyRead := r.readSet[rel]
			r.mu.Unlock()
			if !alreadyRead {
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
		r.mu.Lock()
		r.readSet[rel] = struct{}{}
		r.mu.Unlock()
		if exists {
			return fmt.Sprintf("updated %s", rel), nil
		}
		return fmt.Sprintf("created %s", rel), nil
	case "shell-exec":
		return r.runShellTool(ctx, call.Tool, call.Args, "shell-exec")
	case "glob":
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"` // optional subdirectory
		}
		if err := decodeJSONStrict(call.Args, &args); err != nil {
			return "", fmt.Errorf("glob args: %w", err)
		}
		return r.runGlob(args.Pattern, args.Path)
	case "grep":
		var gargs grepArgs
		if err := decodeJSONStrict(call.Args, &gargs); err != nil {
			return "", fmt.Errorf("grep args: %w", err)
		}
		return r.runGrep(ctx, gargs)
	case "ls":
		var args struct {
			Path string `json:"path"`
		}
		if err := decodeJSONStrict(call.Args, &args); err != nil {
			return "", fmt.Errorf("ls args: %w", err)
		}
		dir := "."
		if args.Path != "" {
			abs, _, err := r.resolvePath(args.Path)
			if err != nil {
				return "", fmt.Errorf("ls: %w", err)
			}
			dir = abs
		} else {
			dir = r.cwd
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", fmt.Errorf("ls: %w", err)
		}
		var lines []string
		for _, e := range entries {
			if e.IsDir() {
				lines = append(lines, e.Name()+"/")
			} else {
				lines = append(lines, e.Name())
			}
		}
		return strings.Join(lines, "\n"), nil
	case "web-fetch":
		return r.runWebFetch(ctx, call.Args)
	case "todo-write":
		var args struct {
			Todos []interface{} `json:"todos"`
		}
		if err := decodeJSONStrict(call.Args, &args); err != nil {
			return "", fmt.Errorf("todo-write args: %w", err)
		}
		if strings.TrimSpace(r.sessionDir) == "" {
			return "", fmt.Errorf("todo-write requires an active session")
		}
		out := make([]session.Todo, 0, len(args.Todos))
		now := time.Now()
		for i, item := range args.Todos {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			if strings.TrimSpace(id) == "" {
				id = fmt.Sprintf("todo-%d", i+1)
			}
			content, _ := m["content"].(string)
			status, _ := m["status"].(string)
			if status == "" {
				status = "pending"
			}
			owner, _ := m["owner"].(string)
			source, _ := m["source"].(string)
			out = append(out, session.Todo{
				ID:        id,
				Content:   content,
				Status:    status,
				Owner:     owner,
				Source:    source,
				UpdatedAt: now,
			})
		}
		raw, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return "", fmt.Errorf("todo-write: marshal: %w", err)
		}
		todosPath := filepath.Join(r.sessionDir, "todos.json")
		if err := os.MkdirAll(filepath.Dir(todosPath), 0o700); err != nil {
			return "", fmt.Errorf("todo-write: mkdir: %w", err)
		}
		if err := os.WriteFile(todosPath, raw, 0o644); err != nil {
			return "", fmt.Errorf("todo-write: write: %w", err)
		}
		return fmt.Sprintf("wrote %d todos", len(out)), nil
	case "bash", "bash-output":
		return r.runShellTool(ctx, call.Tool, call.Args, "bash")
	case "comment":
		var args struct {
			Message string `json:"message"`
		}
		if err := decodeJSONStrict(call.Args, &args); err != nil {
			return "", fmt.Errorf("comment args: %w", err)
		}
		return args.Message, nil
	case "agent":
		var args struct {
			Agent          string `json:"agent"`
			Target         string `json:"target"`
			ID             string `json:"id"`
			Task           string `json:"task"`
			Constraints    string `json:"constraints"`
			ExpectedOutput string `json:"expected_output"`
			ParentAgentID  string `json:"parent_agent_id"`
		}
		if err := decodeJSONStrict(call.Args, &args); err != nil {
			return "", fmt.Errorf("agent args: %w", err)
		}
		target := strings.TrimSpace(args.Agent)
		if target == "" {
			target = strings.TrimSpace(args.Target)
		}
		if target == "" {
			target = strings.TrimSpace(args.ID)
		}
		if target == "" || strings.TrimSpace(args.Task) == "" {
			return "", fmt.Errorf("agent: target and task required")
		}
		if r.delegationDepth >= r.maxDelegationDepth {
			return "", fmt.Errorf("agent: delegation depth exceeded")
		}
		if r.manifest == nil || r.providerMgr == nil {
			return "", fmt.Errorf("agent: sub-agent execution not configured")
		}
		// Find agent spec
		var spec *config.AgentSpec
		for _, a := range r.manifest.Agents {
			if a.ID == target {
				s := a
				spec = &s
				break
			}
		}
		if spec == nil {
			return "", fmt.Errorf("agent: unknown agent %q", target)
		}
		if !spec.Enabled {
			return "", fmt.Errorf("agent: target %q is disabled", target)
		}
		if strings.TrimSpace(r.agentID) != "" {
			if caller, ok := r.manifest.AgentByID(r.agentID); ok {
				allowedHandoff := false
				for _, id := range caller.Handoffs {
					if id == target {
						allowedHandoff = true
						break
					}
				}
				if !allowedHandoff {
					return "", fmt.Errorf("agent: %q cannot delegate to %q (allowed handoffs: %s)", r.agentID, target, strings.Join(caller.Handoffs, ", "))
				}
				if !isDelegationRoleAllowed(caller.Role, spec.Role) {
					return "", fmt.Errorf("agent: role %q cannot delegate to role %q", caller.Role, spec.Role)
				}
				if spec.Mode == "orchestrator" {
					return "", fmt.Errorf("agent: delegation target %q must be worker/subagent role, got orchestrator mode", target)
				}
			}
		}
		// Create and run sub-agent
		subTask := strings.TrimSpace(args.Task)
		if strings.TrimSpace(args.Constraints) != "" {
			subTask += "\n\nConstraints:\n" + strings.TrimSpace(args.Constraints)
		}
		if strings.TrimSpace(args.ExpectedOutput) != "" {
			subTask += "\n\nExpected output:\n" + strings.TrimSpace(args.ExpectedOutput)
		}
		parentID := strings.TrimSpace(args.ParentAgentID)
		if parentID == "" {
			parentID = r.agentID
		}
		subAgent := LLMAgent{
			Spec:            *spec,
			ProviderManager: r.providerMgr,
			ProviderName:    r.providerName,
			ModelName:       r.modelName,
			CWD:             r.cwd,
			MaxTokens:       r.maxTokens,
			ToolCallback:    r.toolCallback,
			ShellApproval:   r.shellApproval,
			Manifest:        r.manifest,
			SessionDir:      r.sessionDir,
			DelegationDepth: r.delegationDepth + 1,
			ParentAgentID:   parentID,
		}
		result, err := subAgent.Run(ctx, subTask)
		if err != nil {
			return "", fmt.Errorf("agent %s: %w", target, err)
		}
		return marshalSubagentResult(target, result), nil
	default:
		return "", fmt.Errorf("unsupported tool %q", call.Tool)
	}
}

// skipDirs are directories to skip when walking the workspace.
var skipDirs = map[string]bool{
	".git":         true,
	".spettro":     true,
	"vendor":       true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
}

// runGlob implements the glob tool using filepath.WalkDir with ** support.
func (r *toolRuntime) runGlob(pattern, subPath string) (string, error) {
	if strings.TrimSpace(pattern) == "" {
		return "", fmt.Errorf("glob: pattern is required")
	}
	root := r.cwd
	if strings.TrimSpace(subPath) != "" {
		abs, _, err := r.resolvePath(subPath)
		if err != nil {
			return "", fmt.Errorf("glob path: %w", err)
		}
		root = abs
	}

	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(r.cwd, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if matchGlobPattern(pattern, rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("glob walk: %w", err)
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return fmt.Sprintf("no files match %q", pattern), nil
	}
	return fmt.Sprintf("%d files:\n%s", len(matches), strings.Join(matches, "\n")), nil
}

// matchGlobPattern matches a slash-separated path against a glob pattern with ** support.
func matchGlobPattern(pattern, rel string) bool {
	patParts := strings.Split(pattern, "/")
	pathParts := strings.Split(rel, "/")
	return globMatch(patParts, pathParts)
}

func globMatch(patParts, pathParts []string) bool {
	if len(patParts) == 0 && len(pathParts) == 0 {
		return true
	}
	if len(patParts) == 0 {
		return false
	}
	if patParts[0] == "**" {
		// ** can match zero or more path components
		// Try matching rest of pattern against every suffix of path
		restPat := patParts[1:]
		// Zero-component match: skip ** entirely
		if globMatch(restPat, pathParts) {
			return true
		}
		// One or more components match
		for i := 1; i <= len(pathParts); i++ {
			if globMatch(restPat, pathParts[i:]) {
				return true
			}
		}
		return false
	}
	if len(pathParts) == 0 {
		return false
	}
	matched, err := filepath.Match(patParts[0], pathParts[0])
	if err != nil || !matched {
		return false
	}
	return globMatch(patParts[1:], pathParts[1:])
}

// typeExtensions maps type names to file extensions.
func typeExtensions(t string) []string {
	switch strings.ToLower(t) {
	case "go":
		return []string{".go"}
	case "ts":
		return []string{".ts", ".tsx"}
	case "js":
		return []string{".js", ".jsx", ".mjs"}
	case "py":
		return []string{".py"}
	case "rs":
		return []string{".rs"}
	case "md":
		return []string{".md"}
	case "toml":
		return []string{".toml"}
	case "json":
		return []string{".json"}
	case "yaml", "yml":
		return []string{".yaml", ".yml"}
	case "sh":
		return []string{".sh", ".bash"}
	default:
		return nil
	}
}

type grepArgs struct {
	Pattern         string `json:"pattern"`
	Glob            string `json:"glob"`
	Type            string `json:"type"`
	CaseInsensitive bool   `json:"case_insensitive"`
	Context         int    `json:"context"`
	OutputMode      string `json:"output_mode"`
	MaxResults      int    `json:"max_results"`
}

// runGrep implements the grep tool.
func (r *toolRuntime) runGrep(_ context.Context, args grepArgs) (string, error) {
	if strings.TrimSpace(args.Pattern) == "" {
		return "", fmt.Errorf("grep: pattern is required")
	}
	regexPattern := args.Pattern
	if args.CaseInsensitive {
		regexPattern = "(?i)" + regexPattern
	}
	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return "", fmt.Errorf("grep: invalid pattern: %w", err)
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 200
	}
	outputMode := args.OutputMode
	if outputMode == "" {
		outputMode = "content"
	}

	exts := typeExtensions(args.Type)

	type fileResult struct {
		path   string
		count  int
		blocks []string // for content mode
	}

	var results []fileResult
	totalMatches := 0
	truncated := false

	walkErr := filepath.WalkDir(r.cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if truncated {
			return nil
		}

		// Filter by type
		if len(exts) > 0 {
			ext := strings.ToLower(filepath.Ext(d.Name()))
			found := false
			for _, e := range exts {
				if ext == e {
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
		// Filter by glob
		if args.Glob != "" {
			matched, mErr := filepath.Match(args.Glob, d.Name())
			if mErr != nil || !matched {
				return nil
			}
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(r.cwd, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		lines := strings.Split(string(data), "\n")
		matchLines := make([]int, 0)
		for i, line := range lines {
			if re.MatchString(line) {
				matchLines = append(matchLines, i)
			}
		}
		if len(matchLines) == 0 {
			return nil
		}

		// Mark as read from search
		r.mu.Lock()
		r.readSet[rel] = struct{}{}
		r.mu.Unlock()

		fr := fileResult{path: rel, count: len(matchLines)}

		if outputMode == "content" {
			// Build context blocks
			included := make([]bool, len(lines))
			for _, mi := range matchLines {
				start := mi - args.Context
				if start < 0 {
					start = 0
				}
				end := mi + args.Context
				if end >= len(lines) {
					end = len(lines) - 1
				}
				for j := start; j <= end; j++ {
					included[j] = true
				}
			}

			var blockBuf bytes.Buffer
			prevIncluded := false
			for i, line := range lines {
				if included[i] {
					if !prevIncluded && blockBuf.Len() > 0 {
						blockBuf.WriteString("--\n")
					}
					fmt.Fprintf(&blockBuf, "%s:%d: %s\n", rel, i+1, line)
					prevIncluded = true
				} else {
					prevIncluded = false
				}
			}
			fr.blocks = []string{blockBuf.String()}
		}

		results = append(results, fr)
		totalMatches += len(matchLines)
		if totalMatches >= args.MaxResults {
			truncated = true
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("grep walk: %w", walkErr)
	}

	if len(results) == 0 {
		return fmt.Sprintf("no matches for %q", args.Pattern), nil
	}

	var sb strings.Builder
	switch outputMode {
	case "files_with_matches":
		for _, fr := range results {
			sb.WriteString(fr.path)
			sb.WriteString("\n")
		}
		header := fmt.Sprintf("%d matches in %d files:\n", totalMatches, len(results))
		out := header + sb.String()
		if truncated {
			out += fmt.Sprintf("(truncated at %d matches)\n", args.MaxResults)
		}
		return strings.TrimRight(out, "\n"), nil
	case "count":
		for _, fr := range results {
			fmt.Fprintf(&sb, "%s: %d\n", fr.path, fr.count)
		}
		header := fmt.Sprintf("%d matches in %d files:\n", totalMatches, len(results))
		out := header + sb.String()
		if truncated {
			out += fmt.Sprintf("(truncated at %d matches)\n", args.MaxResults)
		}
		return strings.TrimRight(out, "\n"), nil
	default: // "content"
		for _, fr := range results {
			for _, block := range fr.blocks {
				sb.WriteString(block)
			}
		}
		header := fmt.Sprintf("%d matches in %d files:\n", totalMatches, len(results))
		out := header + sb.String()
		if truncated {
			out += fmt.Sprintf("(truncated at %d matches)\n", args.MaxResults)
		}
		return strings.TrimRight(out, "\n"), nil
	}
}

func (r *toolRuntime) nextRequiredRead() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
		r.mu.Lock()
		r.readSet[strings.TrimSpace(parts[0])] = struct{}{}
		r.mu.Unlock()
	}
}

func decodeJSONStrict(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON content")
	}
	return nil
}

func normalizeToolArgs(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`), nil
	}
	// Support OpenAI-style stringified arguments for compatibility.
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return nil, fmt.Errorf("arguments must be valid JSON: %w", err)
		}
		trimmed = bytes.TrimSpace([]byte(encoded))
		if len(trimmed) == 0 {
			return json.RawMessage(`{}`), nil
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	return json.RawMessage(trimmed), nil
}

func firstUnknownKey(m map[string]json.RawMessage, allowed map[string]struct{}) string {
	for k := range m {
		if _, ok := allowed[k]; !ok {
			return k
		}
	}
	return ""
}

func extractStringField(raw json.RawMessage, field string) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string", field)
	}
	return strings.TrimSpace(value), nil
}

// parseAllToolCalls scans all lines of s and collects every TOOL_CALL entry.
func parseAllToolCalls(s string) (calls []toolCall, parseErrs []error) {
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(strings.TrimSpace(line), toolCallPrefix) {
			continue
		}
		call, hasCall, err := parseToolCall(strings.TrimSpace(line))
		if err != nil {
			parseErrs = append(parseErrs, err)
			continue
		}
		if hasCall {
			calls = append(calls, call)
		}
	}
	return calls, parseErrs
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
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return toolCall{}, true, fmt.Errorf("invalid tool call JSON: %w", err)
	}
	if unknown := firstUnknownKey(envelope, allowedToolCallKeys); unknown != "" {
		return toolCall{}, true, fmt.Errorf("unsupported tool call field %q", unknown)
	}

	toolFromTool, err := extractStringField(envelope["tool"], "tool")
	if err != nil {
		return toolCall{}, true, err
	}
	toolFromName, err := extractStringField(envelope["name"], "name")
	if err != nil {
		return toolCall{}, true, err
	}
	toolName := toolFromTool
	if toolName == "" {
		toolName = toolFromName
	} else if toolFromName != "" && toolFromName != toolFromTool {
		return toolCall{}, true, fmt.Errorf("conflicting tool names %q and %q", toolFromTool, toolFromName)
	}

	var fn map[string]json.RawMessage
	if fnRaw, ok := envelope["function"]; ok && len(bytes.TrimSpace(fnRaw)) > 0 && !bytes.Equal(bytes.TrimSpace(fnRaw), []byte("null")) {
		if err := json.Unmarshal(fnRaw, &fn); err != nil {
			return toolCall{}, true, fmt.Errorf("function must be an object")
		}
		if unknown := firstUnknownKey(fn, allowedFunctionKeys); unknown != "" {
			return toolCall{}, true, fmt.Errorf("unsupported function field %q", unknown)
		}
		fnName, err := extractStringField(fn["name"], "function.name")
		if err != nil {
			return toolCall{}, true, err
		}
		if toolName == "" {
			toolName = fnName
		} else if fnName != "" && fnName != toolName {
			return toolCall{}, true, fmt.Errorf("conflicting function name %q and tool name %q", fnName, toolName)
		}
	}

	if toolName == "" {
		return toolCall{}, true, fmt.Errorf("tool call missing tool name")
	}

	var argKeys []string
	for _, key := range []string{"args", "arguments", "input", "parameters", "tool_input"} {
		if rawArgs, ok := envelope[key]; ok && len(bytes.TrimSpace(rawArgs)) > 0 && !bytes.Equal(bytes.TrimSpace(rawArgs), []byte("null")) {
			argKeys = append(argKeys, key)
		}
	}
	if rawFnArgs, ok := fn["arguments"]; ok && len(bytes.TrimSpace(rawFnArgs)) > 0 && !bytes.Equal(bytes.TrimSpace(rawFnArgs), []byte("null")) {
		argKeys = append(argKeys, "function.arguments")
	}
	if len(argKeys) > 1 {
		return toolCall{}, true, fmt.Errorf("ambiguous arguments fields: %s", strings.Join(argKeys, ", "))
	}

	rawArgs := json.RawMessage(`{}`)
	switch {
	case len(argKeys) == 0:
		// leave defaults
	case argKeys[0] == "function.arguments":
		rawArgs = fn["arguments"]
	default:
		rawArgs = envelope[argKeys[0]]
	}
	normalizedArgs, err := normalizeToolArgs(rawArgs)
	if err != nil {
		return toolCall{}, true, err
	}

	return toolCall{Tool: toolName, Args: normalizedArgs}, true, nil
}

func parseFinal(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, finalPrefix) {
		return "", false
	}
	out := strings.TrimSpace(strings.TrimPrefix(trimmed, finalPrefix))
	out = strings.TrimPrefix(out, ":")
	out = strings.TrimSpace(out)
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
	case "file-write", "shell-exec", "bash", "bash-output", "agent":
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

func (r *toolRuntime) runShellTool(ctx context.Context, toolID string, rawArgs []byte, prefix string) (string, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("%s args: %w", prefix, err)
	}
	cmdText := strings.TrimSpace(args.Command)
	if cmdText == "" {
		return "", fmt.Errorf("%s: command is required", prefix)
	}
	if err := r.authorizeShellCommand(ctx, toolID, cmdText); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", cmdText)
	cmd.Dir = r.cwd
	out, err := cmd.CombinedOutput()
	text := truncate(string(out), 12000)
	if err != nil {
		return text, fmt.Errorf("command failed: %w", err)
	}
	return text, nil
}

func (r *toolRuntime) runWebFetch(ctx context.Context, rawArgs []byte) (string, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("web-fetch args: %w", err)
	}
	urlText := strings.TrimSpace(args.URL)
	if urlText == "" {
		return "", fmt.Errorf("web-fetch: url required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlText, nil)
	if err != nil {
		return "", fmt.Errorf("web-fetch: %w", err)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web-fetch: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024))
	if err != nil {
		return "", fmt.Errorf("web-fetch: read: %w", err)
	}
	return string(body), nil
}

type allowedCommandsFile struct {
	AllowedCommands []string `json:"allowed_commands"`
}

func isDelegationRoleAllowed(caller, target config.AgentRole) bool {
	switch caller {
	case config.AgentRolePrimary, config.AgentRoleOrchestrator:
		return target == config.AgentRoleWorker || target == config.AgentRoleSubagent
	case config.AgentRoleWorker, config.AgentRoleSubagent:
		return target == config.AgentRoleSubagent || target == config.AgentRoleWorker
	default:
		return false
	}
}

func marshalSubagentResult(agentID string, result RunResult) string {
	payload := map[string]any{
		"agent":            agentID,
		"status":           "ok",
		"summary":          truncate(strings.TrimSpace(result.Content), 1200),
		"tool_trace_count": len(result.Tools),
		"tokens_used":      result.TokensUsed,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("{\"agent\":%q,\"status\":\"ok\",\"summary\":%q}", agentID, truncate(strings.TrimSpace(result.Content), 1200))
	}
	return string(raw)
}

var alwaysAllowedCommandPrefixes = []string{
	"ls",
	"pwd",
	"cat",
	"head",
	"tail",
	"wc",
	"grep",
	"rg",
	"find",
	"stat",
	"git status",
	"git diff",
	"go test",
	"go build",
	"go vet",
	"make test",
	"make build",
}

func (r *toolRuntime) authorizeShellCommand(ctx context.Context, toolID, command string) error {
	command = strings.TrimSpace(command)
	normalized := normalizeCommand(command)
	if normalized == "" {
		return fmt.Errorf("shell-exec command is required")
	}

	segments := splitShellCommandSegments(command)
	if len(segments) == 0 {
		segments = []string{normalized}
	}
	needsApproval := r.permission != config.PermissionYOLO
	if spec, ok := r.toolPolicies[toolID]; ok && !spec.RequiresApproval {
		needsApproval = false
	}

	missingApprovals := make([]string, 0, len(segments))
	toolRules := []config.PermissionRule{}
	if spec, ok := r.toolPolicies[toolID]; ok {
		toolRules = append(toolRules, spec.PermissionRules...)
	}
	r.shellMu.Lock()
	defer r.shellMu.Unlock()
	for _, seg := range segments {
		segNorm := normalizeCommand(seg)
		if segNorm == "" {
			continue
		}
		if isBlockedCommand(segNorm) {
			return fmt.Errorf("blocked dangerous command")
		}
		if isAlwaysAllowedCommand(segNorm) {
			continue
		}
		switch evaluatePermissionRule("execute", segNorm, r.runtimeRules, r.agentRules, toolRules) {
		case config.RuleDeny:
			return fmt.Errorf("shell-exec denied by policy for command segment %q", segNorm)
		case config.RuleAllow:
			continue
		}
		r.mu.Lock()
		_, preapproved := r.allowedShell[segNorm]
		r.mu.Unlock()
		if preapproved {
			continue
		}
		missingApprovals = append(missingApprovals, segNorm)
	}
	if len(missingApprovals) == 0 || !needsApproval {
		return nil
	}

	if r.shellApproval == nil {
		return fmt.Errorf("shell-exec requires approval outside yolo mode")
	}

	decision, err := r.shellApproval(ctx, ShellApprovalRequest{Command: command})
	if err != nil {
		return fmt.Errorf("shell approval failed: %w", err)
	}
	switch decision {
	case ShellApprovalAllowOnce:
		return nil
	case ShellApprovalAllowAlways:
		r.mu.Lock()
		for _, seg := range missingApprovals {
			r.allowedShell[seg] = struct{}{}
		}
		r.mu.Unlock()
		if err := saveAllowedCommandSet(r.cwd, r.allowedShell); err != nil {
			return fmt.Errorf("persist allowed command: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("shell-exec denied by user")
	}
}

func normalizeCommand(command string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
}

func splitShellCommandSegments(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	var (
		segments                []string
		buf                     strings.Builder
		inSingle, inDouble, esc bool
		subDepth                int
	)
	flush := func() {
		seg := strings.TrimSpace(buf.String())
		if seg != "" {
			segments = append(segments, seg)
		}
		buf.Reset()
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if esc {
			buf.WriteByte(ch)
			esc = false
			continue
		}
		switch ch {
		case '\\':
			esc = true
			buf.WriteByte(ch)
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			buf.WriteByte(ch)
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			buf.WriteByte(ch)
		case '(':
			if !inSingle && !inDouble && i > 0 && command[i-1] == '$' {
				subDepth++
			}
			buf.WriteByte(ch)
		case ')':
			if !inSingle && !inDouble && subDepth > 0 {
				subDepth--
			}
			buf.WriteByte(ch)
		case ';':
			if inSingle || inDouble || subDepth > 0 {
				buf.WriteByte(ch)
				continue
			}
			flush()
		case '|':
			if inSingle || inDouble || subDepth > 0 {
				buf.WriteByte(ch)
				continue
			}
			if i+1 < len(command) && command[i+1] == '|' {
				flush()
				i++
				continue
			}
			flush()
		case '&':
			if inSingle || inDouble || subDepth > 0 {
				buf.WriteByte(ch)
				continue
			}
			if i+1 < len(command) && command[i+1] == '&' {
				flush()
				i++
				continue
			}
			buf.WriteByte(ch)
		case '\n':
			if inSingle || inDouble || subDepth > 0 {
				buf.WriteByte(ch)
				continue
			}
			flush()
		default:
			buf.WriteByte(ch)
		}
	}
	flush()
	return segments
}

func isAlwaysAllowedCommand(command string) bool {
	for _, prefix := range alwaysAllowedCommandPrefixes {
		if command == prefix || strings.HasPrefix(command, prefix+" ") {
			return true
		}
	}
	return false
}

func allowedCommandsPath(cwd string) string {
	return filepath.Join(cwd, ".spettro", "allowed_commands.json")
}

func loadAllowedCommandSet(cwd string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	data, err := os.ReadFile(allowedCommandsPath(cwd))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("read allowed commands: %w", err)
	}
	var parsed allowedCommandsFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode allowed commands: %w", err)
	}
	for _, cmd := range parsed.AllowedCommands {
		norm := normalizeCommand(cmd)
		if norm != "" {
			out[norm] = struct{}{}
		}
	}
	return out, nil
}

func saveAllowedCommandSet(cwd string, set map[string]struct{}) error {
	cmds := make([]string, 0, len(set))
	for cmd := range set {
		if strings.TrimSpace(cmd) != "" {
			cmds = append(cmds, cmd)
		}
	}
	sort.Strings(cmds)
	payload := allowedCommandsFile{AllowedCommands: cmds}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode allowed commands: %w", err)
	}

	path := allowedCommandsPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create .spettro dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("write allowed commands temp: %w", err)
	}
	return os.Rename(tmp, path)
}
