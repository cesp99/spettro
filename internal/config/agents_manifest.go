package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const AgentManifestFilename = "spettro.agents.toml"

type AgentManifest struct {
	Version      int             `toml:"version"`
	DefaultAgent string          `toml:"default_agent"`
	Metadata     AgentMetadata   `toml:"metadata"`
	Runtime      RuntimePolicy   `toml:"runtime"`
	Tools        []ToolSpec      `toml:"tools"`
	Agents       []AgentSpec     `toml:"agents"`
}

type AgentMetadata struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
}

type RuntimePolicy struct {
	DefaultPermission PermissionLevel `toml:"default_permission"`
	DefaultTimeoutSec int             `toml:"default_timeout_sec"`
	AllowNetworkTools bool            `toml:"allow_network_tools"`
	LogToolCalls      bool            `toml:"log_tool_calls"`
}

type ToolSpec struct {
	ID               string   `toml:"id"`
	Name             string   `toml:"name"`
	Description      string   `toml:"description"`
	Kind             string   `toml:"kind"`
	Enabled          bool     `toml:"enabled"`
	EntryPoint       string   `toml:"entry_point"`
	TimeoutSec       int      `toml:"timeout_sec"`
	RequiresApproval bool     `toml:"requires_approval"`
	PermittedActions []string `toml:"permitted_actions"`
}

type AgentSpec struct {
	ID               string          `toml:"id"`
	Name             string          `toml:"name"`
	Description      string          `toml:"description"`
	Skill            string          `toml:"skill"`
	Mode             string          `toml:"mode"`
	ModelProvider    string          `toml:"model_provider"`
	Model            string          `toml:"model"`
	SystemPrompt     string          `toml:"system_prompt"`
	PromptFile       string          `toml:"prompt_file"`
	AllowedTools     []string        `toml:"allowed_tools"`
	PermittedActions []string        `toml:"permitted_actions"`
	Permission       PermissionLevel `toml:"permission"`
	Temperature      float64         `toml:"temperature"`
	MaxTokens        int             `toml:"max_tokens"`
	MaxSteps         int             `toml:"max_steps"`
	Handoffs         []string        `toml:"handoffs"`
	Enabled          bool            `toml:"enabled"`
}

func DefaultAgentManifest() AgentManifest {
	return AgentManifest{
		Version:      1,
		DefaultAgent: "planning",
		Metadata: AgentMetadata{
			Name:        "Spettro default agents",
			Description: "Built-in fallback manifest when no spettro.agents.toml is present.",
		},
		Runtime: RuntimePolicy{
			DefaultPermission: PermissionAskFirst,
			DefaultTimeoutSec: 120,
			AllowNetworkTools: false,
			LogToolCalls:      true,
		},
		Tools: []ToolSpec{
			{
				ID:               "repo-search",
				Name:             "Repository Search",
				Description:      "Searches file names and content inside the project.",
				Kind:             "builtin",
				Enabled:          true,
				TimeoutSec:       30,
				RequiresApproval: false,
				PermittedActions: []string{"read", "search"},
			},
			{
				ID:               "file-write",
				Name:             "File Writer",
				Description:      "Creates and edits files in the repository workspace.",
				Kind:             "builtin",
				Enabled:          true,
				TimeoutSec:       60,
				RequiresApproval: true,
				PermittedActions: []string{"write"},
			},
			{
				ID:               "shell-exec",
				Name:             "Shell Executor",
				Description:      "Runs shell commands in the project directory.",
				Kind:             "builtin",
				Enabled:          true,
				TimeoutSec:       120,
				RequiresApproval: true,
				PermittedActions: []string{"execute", "git"},
			},
			{
				ID:               "provider-chat",
				Name:             "Provider Chat",
				Description:      "Sends prompts and images to the active LLM provider.",
				Kind:             "builtin",
				Enabled:          true,
				TimeoutSec:       120,
				RequiresApproval: false,
				PermittedActions: []string{"chat"},
			},
		},
		Agents: []AgentSpec{
			{
				ID:               "planning",
				Name:             "Planning Agent",
				Description:      "Plans changes and produces approved implementation steps.",
				Skill:            "architecture",
				Mode:             "planning",
				AllowedTools:     []string{"repo-search"},
				PermittedActions: []string{"read", "search", "plan"},
				Permission:       PermissionAskFirst,
				MaxSteps:         20,
				Enabled:          true,
				Handoffs:         []string{"coding", "chat"},
			},
			{
				ID:               "coding",
				Name:             "Coding Agent",
				Description:      "Executes approved plans with permission-aware actions.",
				Skill:            "implementation",
				Mode:             "coding",
				AllowedTools:     []string{"repo-search", "file-write", "shell-exec"},
				PermittedActions: []string{"read", "write", "execute", "git"},
				Permission:       PermissionRestricted,
				MaxSteps:         40,
				Enabled:          true,
				Handoffs:         []string{"planning", "chat"},
			},
			{
				ID:               "chat",
				Name:             "Chat Agent",
				Description:      "General assistant mode for questions, explanations, and Q&A.",
				Skill:            "conversation",
				Mode:             "chat",
				AllowedTools:     []string{"provider-chat", "repo-search"},
				PermittedActions: []string{"chat", "read", "search"},
				Permission:       PermissionAskFirst,
				MaxSteps:         10,
				Enabled:          true,
				Handoffs:         []string{"planning", "coding"},
			},
		},
	}
}

func AgentManifestPath(cwd string) string {
	return filepath.Join(cwd, AgentManifestFilename)
}

func LoadAgentManifestForProject(cwd string) (AgentManifest, error) {
	p := AgentManifestPath(cwd)
	m, err := LoadAgentManifest(p)
	if err == nil {
		return m, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return DefaultAgentManifest(), nil
	}
	return AgentManifest{}, err
}

func LoadAgentManifest(path string) (AgentManifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return AgentManifest{}, err
	}
	defer f.Close()
	return DecodeAgentManifest(f)
}

func DecodeAgentManifest(r io.Reader) (AgentManifest, error) {
	var manifest AgentManifest
	decoder := toml.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return AgentManifest{}, fmt.Errorf("decode agent manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return AgentManifest{}, err
	}
	return manifest, nil
}

func (m AgentManifest) Validate() error {
	if m.Version <= 0 {
		return fmt.Errorf("agent manifest: version must be > 0")
	}
	if len(m.Tools) == 0 {
		return fmt.Errorf("agent manifest: at least one tool is required")
	}
	if len(m.Agents) == 0 {
		return fmt.Errorf("agent manifest: at least one agent is required")
	}
	if strings.TrimSpace(m.DefaultAgent) == "" {
		return fmt.Errorf("agent manifest: default_agent is required")
	}
	if m.Runtime.DefaultTimeoutSec <= 0 {
		return fmt.Errorf("agent manifest: runtime.default_timeout_sec must be > 0")
	}

	if err := validatePermissionLevel(m.Runtime.DefaultPermission); err != nil {
		return fmt.Errorf("agent manifest: invalid runtime.default_permission: %w", err)
	}

	toolIDs := map[string]struct{}{}
	for _, tool := range m.Tools {
		id := strings.TrimSpace(tool.ID)
		if id == "" {
			return fmt.Errorf("agent manifest: tool id is required")
		}
		if _, exists := toolIDs[id]; exists {
			return fmt.Errorf("agent manifest: duplicate tool id %q", id)
		}
		toolIDs[id] = struct{}{}

		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("agent manifest: tool %q name is required", id)
		}
		switch tool.Kind {
		case "builtin", "mcp", "script", "http":
		default:
			return fmt.Errorf("agent manifest: tool %q has unsupported kind %q", id, tool.Kind)
		}
		if tool.TimeoutSec <= 0 {
			return fmt.Errorf("agent manifest: tool %q timeout_sec must be > 0", id)
		}
		if len(tool.PermittedActions) == 0 {
			return fmt.Errorf("agent manifest: tool %q must declare permitted_actions", id)
		}
		if (tool.Kind == "script" || tool.Kind == "http" || tool.Kind == "mcp") && strings.TrimSpace(tool.EntryPoint) == "" {
			return fmt.Errorf("agent manifest: tool %q requires entry_point for kind %q", id, tool.Kind)
		}
	}

	agentIDs := map[string]struct{}{}
	for _, agent := range m.Agents {
		id := strings.TrimSpace(agent.ID)
		if id == "" {
			return fmt.Errorf("agent manifest: agent id is required")
		}
		if _, exists := agentIDs[id]; exists {
			return fmt.Errorf("agent manifest: duplicate agent id %q", id)
		}
		agentIDs[id] = struct{}{}

		if strings.TrimSpace(agent.Name) == "" {
			return fmt.Errorf("agent manifest: agent %q name is required", id)
		}
		if strings.TrimSpace(agent.Mode) == "" {
			return fmt.Errorf("agent manifest: agent %q mode is required", id)
		}
		if agent.MaxSteps <= 0 {
			return fmt.Errorf("agent manifest: agent %q max_steps must be > 0", id)
		}
		if len(agent.AllowedTools) == 0 {
			return fmt.Errorf("agent manifest: agent %q must declare allowed_tools", id)
		}
		if err := validatePermissionLevel(agent.Permission); err != nil {
			return fmt.Errorf("agent manifest: invalid permission for agent %q: %w", id, err)
		}
		for _, toolID := range agent.AllowedTools {
			if _, exists := toolIDs[toolID]; !exists {
				return fmt.Errorf("agent manifest: agent %q references unknown tool %q", id, toolID)
			}
		}
	}

	if _, exists := agentIDs[m.DefaultAgent]; !exists {
		return fmt.Errorf("agent manifest: default_agent %q not found in agents", m.DefaultAgent)
	}

	for _, agent := range m.Agents {
		for _, handoff := range agent.Handoffs {
			if _, exists := agentIDs[handoff]; !exists {
				return fmt.Errorf("agent manifest: agent %q handoff references unknown agent %q", agent.ID, handoff)
			}
		}
	}

	return nil
}

func validatePermissionLevel(level PermissionLevel) error {
	switch level {
	case PermissionYOLO, PermissionRestricted, PermissionAskFirst:
		return nil
	default:
		return fmt.Errorf("unsupported permission level %q", level)
	}
}

func (m AgentManifest) AgentByID(id string) (AgentSpec, bool) {
	for _, a := range m.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return AgentSpec{}, false
}

func (m AgentManifest) EnabledAgents() []AgentSpec {
	out := make([]AgentSpec, 0, len(m.Agents))
	for _, a := range m.Agents {
		if a.Enabled {
			out = append(out, a)
		}
	}
	return out
}

func (m AgentManifest) EnabledToolsForAgent(agentID string) []ToolSpec {
	agent, ok := m.AgentByID(agentID)
	if !ok {
		return nil
	}

	allowed := map[string]struct{}{}
	for _, id := range agent.AllowedTools {
		allowed[id] = struct{}{}
	}

	out := make([]ToolSpec, 0)
	for _, t := range m.Tools {
		if !t.Enabled {
			continue
		}
		if _, ok := allowed[t.ID]; ok {
			out = append(out, t)
		}
	}

	slices.SortFunc(out, func(a, b ToolSpec) int {
		return strings.Compare(a.ID, b.ID)
	})

	return out
}
