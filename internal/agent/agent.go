package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"spettro/internal/config"
	"spettro/internal/provider"
)

// Legacy interfaces — kept for backward compatibility with existing tests and callers.

type PlanningAgent interface {
	Plan(context.Context, string) (RunResult, error)
}

type CodingAgent interface {
	Execute(context.Context, string, config.PermissionLevel, bool) (RunResult, error)
}

type ChatAgent interface {
	Reply(context.Context, string, []string) (provider.Response, error)
}

// CommitAgent is defined in committer.go.
// SearchAgent is defined in searcher.go.
// ExploreAgent is defined in explorer.go.

type ExploreAgent interface {
	Explore(context.Context, string) (RunResult, error)
}

type ToolTrace struct {
	Name   string
	Status string
	Args   string
	Output string
}

type RunResult struct {
	Content     string
	Tools       []ToolTrace
	TokensUsed  int // total tokens consumed across all LLM calls in the run
}

// Legacy stub types — kept so existing tests compile.

type Planner struct{}

func (Planner) Plan(_ context.Context, userPrompt string) (RunResult, error) {
	p := strings.TrimSpace(userPrompt)
	if p == "" {
		return RunResult{}, fmt.Errorf("empty planning prompt")
	}

	return RunResult{
		Content: fmt.Sprintf(
			"# Generated Plan\n\n- Timestamp: %s\n- Objective: %s\n\n## Steps\n1. Analyze current files\n2. Propose edits\n3. Request approval\n4. Execute in coding mode\n",
			time.Now().UTC().Format(time.RFC3339),
			p,
		),
	}, nil
}

type Coder struct{}

func (Coder) Execute(_ context.Context, plan string, level config.PermissionLevel, approved bool) (RunResult, error) {
	if strings.TrimSpace(plan) == "" {
		return RunResult{}, fmt.Errorf("empty approved plan")
	}

	if level == config.PermissionAskFirst && !approved {
		return RunResult{}, fmt.Errorf("ask-first policy requires explicit approval")
	}

	return RunResult{
		Content: fmt.Sprintf("Executed plan with permission=%s.\nSummary: %s\n", level, compact(plan)),
	}, nil
}

type Chatter struct {
	ProviderManager *provider.Manager
	ProviderName    func() string
	ModelName       func() string
}

func (c Chatter) Reply(ctx context.Context, prompt string, images []string) (provider.Response, error) {
	return c.ProviderManager.Send(ctx, c.ProviderName(), c.ModelName(), provider.Request{
		Prompt: prompt,
		Images: images,
	})
}

// LLMAgent is the unified agent runner. It reads the agent's system prompt from
// the PromptFile specified in the spec (stripping frontmatter), and runs the
// standard tool loop with the tools, permissions, and limits from the spec.
type LLMAgent struct {
	Spec            config.AgentSpec
	ProviderManager *provider.Manager
	ProviderName    func() string
	ModelName       func() string
	CWD             string
	MaxTokens       int
	RequiredReads   []string
	Images          []string // only used on first LLM call (chat use case)
	ToolCallback    func(ToolTrace)
	ShellApproval   ShellApprovalCallback
	Manifest        *config.AgentManifest // for sub-agent spawning via agent tool
}

func (a LLMAgent) Run(ctx context.Context, task string) (RunResult, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return RunResult{}, fmt.Errorf("empty task")
	}
	systemPrompt := loadPromptOrFallback(a.CWD, a.Spec.PromptFile, a.Spec.Description)
	requireToolCall := a.Spec.Mode != "ask" && len(a.Spec.AllowedTools) > 0
	maxSteps := a.Spec.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}
	out, traces, tokens, err := runToolLoop(ctx, toolLoopConfig{
		SystemPrompt:    systemPrompt,
		UserTask:        task,
		CWD:             a.CWD,
		MaxSteps:        maxSteps,
		RequireToolCall: requireToolCall,
		AllowedTools:    a.Spec.AllowedTools,
		ProviderManager: a.ProviderManager,
		ProviderName:    a.ProviderName,
		ModelName:       a.ModelName,
		MaxTokens:       a.MaxTokens,
		RequiredReads:   a.RequiredReads,
		Images:          a.Images,
		ToolCallback:    a.ToolCallback,
		Permission:      a.Spec.Permission,
		ShellApproval:   a.ShellApproval,
		Manifest:        a.Manifest,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("%s agent: %w", a.Spec.ID, err)
	}
	out = strings.TrimSpace(out)
	out = stripLeakedToolCalls(out)
	main, _ := stripThinkTags(out)
	return RunResult{
		Content:    strings.TrimSpace(main),
		Tools:      traces,
		TokensUsed: tokens,
	}, nil
}

func compact(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	const max = 180
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
