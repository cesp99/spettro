package agent

import (
	"context"
	"fmt"
	"strings"

	"spettro/internal/budget"
	"spettro/internal/provider"
)

const planSystemPrompt = `You are a software architect and planning agent.
Given a task description, create a detailed, actionable implementation plan.

Format your response as a markdown document with:
- ## Objective — one-sentence summary of what needs to be done
- ## Analysis — what must be understood before coding starts
- ## Steps — numbered list of concrete steps, each specifying file paths and what to change
- ## Notes — edge cases, gotchas, or follow-up work

Be specific about file names, function signatures, and data structures.
Do not write implementation code — only plan.`

// LLMPlanner uses the active provider to generate an implementation plan.
type LLMPlanner struct {
	ProviderManager *provider.Manager
	ProviderName    func() string
	ModelName       func() string
	CWD             string
}

func (p LLMPlanner) Plan(ctx context.Context, userPrompt string) (string, error) {
	prompt := strings.TrimSpace(userPrompt)
	if prompt == "" {
		return "", fmt.Errorf("empty planning prompt")
	}

	full := planSystemPrompt + "\n\n## Task\n" + prompt
	if p.CWD != "" {
		full += "\n\n## Working Directory\n" + p.CWD
	}

	if err := budget.Validate(full); err != nil {
		return "", err
	}

	resp, err := p.ProviderManager.Send(ctx, p.ProviderName(), p.ModelName(), provider.Request{
		Prompt: full,
	})
	if err != nil {
		return "", fmt.Errorf("llm planner: %w", err)
	}

	plan := strings.TrimSpace(resp.Content)
	if plan == "" {
		return "", fmt.Errorf("LLM returned empty plan")
	}

	return fmt.Sprintf("# Plan\n\n%s\n\n---\n*planned by %s / %s*\n",
		plan, p.ProviderName(), p.ModelName()), nil
}
