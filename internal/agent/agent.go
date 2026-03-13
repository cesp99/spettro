package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"spettro/internal/config"
	"spettro/internal/provider"
)

type PlanningAgent interface {
	Plan(context.Context, string) (string, error)
}

type CodingAgent interface {
	Execute(context.Context, string, config.PermissionLevel, bool) (string, error)
}

type ChatAgent interface {
	Reply(context.Context, string, []string) (provider.Response, error)
}

// CommitAgent is defined in committer.go.
// SearchAgent is defined in searcher.go.

type Planner struct{}

func (Planner) Plan(_ context.Context, userPrompt string) (string, error) {
	p := strings.TrimSpace(userPrompt)
	if p == "" {
		return "", fmt.Errorf("empty planning prompt")
	}

	return fmt.Sprintf(
		"# Generated Plan\n\n- Timestamp: %s\n- Objective: %s\n\n## Steps\n1. Analyze current files\n2. Propose edits\n3. Request approval\n4. Execute in coding mode\n",
		time.Now().UTC().Format(time.RFC3339),
		p,
	), nil
}

type Coder struct{}

func (Coder) Execute(_ context.Context, plan string, level config.PermissionLevel, approved bool) (string, error) {
	if strings.TrimSpace(plan) == "" {
		return "", fmt.Errorf("empty approved plan")
	}

	if level == config.PermissionAskFirst && !approved {
		return "", fmt.Errorf("ask-first policy requires explicit approval")
	}

	result := fmt.Sprintf("Executed plan with permission=%s.\nSummary: %s\n", level, compact(plan))
	return result, nil
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

func compact(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	const max = 180
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
