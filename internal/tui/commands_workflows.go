package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"spettro/internal/workflow"
)

const workflowsUsage = "usage: /workflows <list|show|run|where> [name] [args-json]"

const workflowsHelp = `workflow commands:
  /workflows                     list saved workflows (project first, then global)
  /workflows show <name>         print a saved workflow's header and source
  /workflows run <name> [json]   run a saved workflow, optionally with JSON args
  /workflows where               show the directories scanned for saved workflows

Workflows are JavaScript orchestration scripts: the model writes one, and
Spettro executes its phases, fan-outs and loops exactly as written. Write
"ultracode" in any message to give the agent the workflow tool for that turn.

Saved scripts live in .spettro/workflows/<name>.js (project) or
~/.spettro/workflows/<name>.js (global); a project script shadows a global
one with the same name.`

func (m Model) handleWorkflowsCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(input)
	if len(fields) <= 1 {
		return m.runWorkflowsList()
	}
	switch strings.ToLower(fields[1]) {
	case "list", "ls":
		return m.runWorkflowsList()
	case "show", "cat", "info":
		return m.runWorkflowsShow(fields[2:])
	case "run", "start":
		return m.runWorkflowsRun(input, fields[2:])
	case "where", "paths":
		var b strings.Builder
		b.WriteString("workflow search paths (first match wins):\n")
		for _, p := range workflow.SearchPaths(m.cwd) {
			b.WriteString("  " + p + "\n")
		}
		m.pushSystemMsg(strings.TrimRight(b.String(), "\n"))
		m.refreshViewport()
		return m, nil
	case "help":
		m.pushSystemMsg(workflowsHelp)
		m.refreshViewport()
		return m, nil
	default:
		m.showBanner(workflowsUsage, "info")
		return m, nil
	}
}

func (m Model) runWorkflowsList() (tea.Model, tea.Cmd) {
	saved := workflow.Discover(m.cwd)
	if len(saved) == 0 {
		m.pushSystemMsg("no saved workflows.\n\n" + workflowsHelp)
		m.refreshViewport()
		return m, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "saved workflows (%d):\n", len(saved))
	for _, s := range saved {
		if s.Err != nil {
			fmt.Fprintf(&b, "  %-24s [%s] header does not parse: %v\n", s.Name, s.Scope, s.Err)
			continue
		}
		fmt.Fprintf(&b, "  %-24s [%s] %s\n", s.Name, s.Scope, s.Meta.Description)
		if s.Meta.WhenToUse != "" {
			fmt.Fprintf(&b, "  %-24s      when: %s\n", "", s.Meta.WhenToUse)
		}
		if len(s.Meta.Phases) > 0 {
			titles := make([]string, 0, len(s.Meta.Phases))
			for _, p := range s.Meta.Phases {
				titles = append(titles, p.Title)
			}
			fmt.Fprintf(&b, "  %-24s      phases: %s\n", "", strings.Join(titles, " → "))
		}
	}
	b.WriteString("\nrun one with /workflows run <name>")
	m.pushSystemMsg(strings.TrimRight(b.String(), "\n"))
	m.refreshViewport()
	return m, nil
}

func (m Model) runWorkflowsShow(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.showBanner("usage: /workflows show <name>", "error")
		return m, nil
	}
	script, path, err := workflow.Load(m.cwd, args[0])
	if err != nil {
		m.showBanner(err.Error(), "error")
		return m, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", path)
	if meta, err := workflow.ParseMeta(script); err == nil {
		fmt.Fprintf(&b, "%s — %s\n", meta.Name, meta.Description)
		for _, p := range meta.Phases {
			fmt.Fprintf(&b, "  · %s%s\n", p.Title, optionalDetail(p.Detail))
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "header does not parse: %v\n\n", err)
	}
	b.WriteString("```javascript\n" + strings.TrimRight(script, "\n") + "\n```")
	m.pushSystemMsg(b.String())
	m.refreshViewport()
	return m, nil
}

func optionalDetail(detail string) string {
	if detail == "" {
		return ""
	}
	return " — " + detail
}

// runWorkflowsRun dispatches a saved workflow as an ordinary turn. The command
// does not execute the script itself: a workflow's results are only useful to
// someone who then acts on them, and that someone is the agent. Handing it the
// tool call to make keeps one execution path — the model reviews the result,
// re-dispatches failures, and integrates the outcome exactly as it would for a
// workflow it wrote itself.
func (m Model) runWorkflowsRun(input string, args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.showBanner("usage: /workflows run <name> [args-json]", "error")
		return m, nil
	}
	name := args[0]
	script, path, err := workflow.Load(m.cwd, name)
	if err != nil {
		m.showBanner(err.Error(), "error")
		return m, nil
	}
	meta, err := workflow.ParseMeta(script)
	if err != nil {
		m.showBanner(fmt.Sprintf("%s: %v", name, err), "error")
		return m, nil
	}

	rawArgs := restAfterFields(input, 3)
	if rawArgs != "" && !json.Valid([]byte(rawArgs)) {
		m.showBanner("workflow args must be valid JSON", "error")
		return m, nil
	}

	var prompt strings.Builder
	prompt.WriteString("ultracode: run the saved workflow ")
	prompt.WriteString(jsonQuote(name))
	prompt.WriteString(" — ")
	prompt.WriteString(meta.Description)
	prompt.WriteString(".\nCall the workflow tool with {\"name\": ")
	prompt.WriteString(jsonQuote(name))
	if rawArgs != "" {
		prompt.WriteString(", \"args\": ")
		prompt.WriteString(rawArgs)
	}
	prompt.WriteString("}. Do not rewrite the script; run it as saved, then review the result and act on it.")

	m.showBanner("running workflow "+name+" ("+filepath.Base(path)+")", "info")
	return m.handlePrompt(prompt.String())
}

func jsonQuote(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		return `"` + s + `"`
	}
	return string(encoded)
}

// restAfterFields returns whatever follows the first n whitespace-separated
// fields, with the original spacing intact. Rejoining strings.Fields would
// collapse runs of spaces inside a JSON string literal.
func restAfterFields(input string, n int) string {
	rest := strings.TrimSpace(input)
	for range n {
		_, after, found := strings.Cut(rest, " ")
		if !found {
			return ""
		}
		rest = strings.TrimSpace(after)
	}
	return rest
}
