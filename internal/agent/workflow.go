package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"spettro/internal/config"
	"spettro/internal/provider"
	"spettro/internal/workflow"
)

// Workflows: deterministic multi-agent orchestration. Where Ultra fans one
// prompt template over a list of items, a workflow is a script that decides in
// ordinary control flow — loops, conditionals, staged pipelines — which
// sub-agents run and how their results combine. The model writes the script;
// Spettro executes it exactly as written.

const (
	workflowToolID = "workflow"
	// workflowKeyword opts a single turn into workflows. It is a one-shot
	// switch on purpose: injecting the tool and its guidance changes the
	// system prompt, and a persistent toggle would pay that cache cost on
	// every turn for a capability most turns do not need.
	workflowKeyword = "ultracode"
	// workflowMaxItems caps one parallel()/pipeline() call.
	workflowMaxItems = 4096
	// workflowMaxAgents is the runaway-loop backstop for a whole run.
	workflowMaxAgents = 1000
	// workflowRetryBase is the first backoff for a transient provider failure
	// inside a workflow agent; attempts double it.
	workflowRetryBase   = 3 * time.Second
	workflowMaxAttempts = 3
)

// workflowKeywordRe matches the activation keyword as a whole word, so
// "ultracode" in prose turns workflows on but "ultracoded" or a path like
// src/ultracode.go does not.
var workflowKeywordRe = regexp.MustCompile(`(?i)\bultracode\b`)

// WorkflowRequested reports whether a user message opts this turn into
// workflows. Detection lives here rather than in each host so the TUI, ACP,
// goal runs, Telegram and headless mode all honour the keyword identically.
func WorkflowRequested(task string) bool {
	return workflowKeywordRe.MatchString(task)
}

// workflowPromptSection is appended to the system prompt when workflows are
// active. Like the Ultra section it is fixed for the whole run, which keeps
// the prompt-cache prefix byte-stable.
const workflowPromptSection = `

WORKFLOWS are active for this turn (the user wrote "ultracode"). You have the workflow tool: it runs a JavaScript orchestration script you write, so the control flow around your sub-agents is deterministic instead of re-decided by you every step.

Use it when the work has structure worth encoding — fan out and verify, several independent attempts judged against each other, a sweep that loops until it stops finding things, a migration over a discovered work-list. For a single delegation use the agent tool; for a flat fan-out of one template over N items, ultra is still the simpler choice.

Scout inline first (list the files, scope the diff, find the call sites), then hand the discovered work-list to a script. You stay in the loop between workflows: read each result and decide the next phase yourself.

The script must begin with a pure object literal header and then use the provided globals:

export const meta = {
  name: 'review-changes',
  description: 'Review the diff, then adversarially verify each finding',
  phases: [{title: 'Review'}, {title: 'Verify'}],
}
phase('Review')
const results = await pipeline(DIMENSIONS,
  d => agent(` + "`Review the diff for ${d}`" + `, {label: 'review:' + d, phase: 'Review', schema: FINDINGS}),
  review => parallel(review.findings.map(f => () =>
    agent('Try to refute: ' + f.title, {phase: 'Verify', schema: VERDICT}))))
return results.flat().filter(Boolean)

Use opts.schema whenever a stage produces data the next stage consumes. Spettro appends the contract to the prompt, parses the answer back, and retries the agent with the parse error if it does not fit — hand-rolling JSON.parse over the text in the script gets none of that, and one malformed answer silently drops a result.

Globals: agent(prompt, opts) → the sub-agent's final text, or the parsed object when opts.schema is a JSON Schema, or null if it failed; parallel(thunks) → runs all concurrently and waits for every one (a barrier); pipeline(items, ...stages) → pushes each item through every stage independently with NO barrier between stages; phase(title); log(message); args (whatever the tool call passed); budget.remaining(); workflow(name, args) to run a saved workflow as a sub-step.

Prefer pipeline over parallel-then-parallel: a barrier is only right when a stage genuinely needs every previous result at once (dedup across the whole set, an early exit on zero findings). Give agent() a label and a phase so the user can follow the run.

Every agent is a fresh sub-agent that cannot see your context or the other agents: each prompt must be self-contained, with paths, constraints, and the expected output. Never give two concurrently running agents work that touches the same file — set isolation:"worktree" on the ones that edit.

Date.now(), Math.random() and argless new Date() are unavailable (they would break resume); pass timestamps in through args and vary work by index.`

type workflowArgs struct {
	Script          string          `json:"script"`
	ScriptPath      string          `json:"script_path"`
	Name            string          `json:"name"`
	Args            json.RawMessage `json:"args"`
	ResumeFromRunID string          `json:"resume_from_run_id"`
	MaxConcurrency  int             `json:"max_concurrency"`
	BudgetTokens    int             `json:"budget_tokens"`
}

// runWorkflow is the workflow tool: resolve the script, run it, and hand the
// script's return value back to the model together with what the run did.
func (r *toolRuntime) runWorkflow(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var args workflowArgs
	if err := decodeJSONStrict(rawArgs, &args); err != nil {
		return "", fmt.Errorf("workflow args: %w", err)
	}
	if r.delegationDepth > 0 {
		return "", fmt.Errorf("workflow: only the top-level agent can run a workflow")
	}
	if r.manifest == nil || r.providerMgr == nil {
		return "", fmt.Errorf("workflow: sub-agent execution not configured")
	}
	// Same rule as Ultra: a workflow runs many sub-agents concurrently, and
	// ask-first would turn that into a wall of approval prompts.
	if r.perm() == config.PermissionAskFirst {
		return "", fmt.Errorf("workflow: requires restricted or yolo permission (current: ask-first)")
	}

	script, origin, err := r.resolveWorkflowScript(args)
	if err != nil {
		return "", err
	}
	meta, err := workflow.ParseMeta(script)
	if err != nil {
		return "", fmt.Errorf("workflow: %w", err)
	}

	runID := newWorkflowRunID()
	journal, jerr := workflow.OpenJournal(r.workflowRunDir(runID))
	if jerr != nil {
		// Persistence is a convenience; a run that cannot journal still runs.
		journal = nil
	}
	defer journal.Close()
	if journal != nil {
		_ = journal.WriteFile("script.js", script)
		if encoded, err := json.MarshalIndent(meta, "", "  "); err == nil {
			_ = journal.WriteFile("meta.json", string(encoded))
		}
		if args.ResumeFromRunID != "" {
			prior, err := r.findWorkflowRunDir(args.ResumeFromRunID, args.ScriptPath)
			if err != nil {
				return "", fmt.Errorf("workflow: %w", err)
			}
			if err := journal.LoadCache(prior); err != nil {
				return "", fmt.Errorf("workflow: resume from %s: %w", args.ResumeFromRunID, err)
			}
		}
	}

	var scriptArgs any
	if len(args.Args) > 0 {
		if err := json.Unmarshal(args.Args, &scriptArgs); err != nil {
			return "", fmt.Errorf("workflow: args is not valid JSON: %w", err)
		}
	}

	obs := &workflowObserver{rt: r, runID: runID, meta: meta}
	obs.start(origin)
	result, runErr := workflow.Run(ctx, script, workflow.Options{
		Runner:           &workflowRunner{rt: r, runID: runID},
		Observer:         obs.handle,
		MaxConcurrency:   args.MaxConcurrency,
		MaxAgents:        workflowMaxAgents,
		MaxItems:         workflowMaxItems,
		BudgetTokens:     args.BudgetTokens,
		Journal:          journal,
		DefaultAgentType: defaultWorkflowAgentType(r.manifest),
		Resolve: func(name string) (string, error) {
			src, _, err := workflow.Load(r.cwd, name)
			return src, err
		},
		Args: scriptArgs,
	})
	obs.finish(result, runErr)

	if journal != nil {
		if encoded, err := json.MarshalIndent(result.Value, "", "  "); err == nil {
			_ = journal.WriteFile("result.json", string(encoded))
		}
	}
	if runErr != nil {
		return "", fmt.Errorf("%w (run %s; transcript at %s)", runErr, runID, r.workflowRunDir(runID))
	}
	return renderWorkflowResult(runID, r.workflowRunDir(runID), origin, meta, result), nil
}

// resolveWorkflowScript picks the script to run: inline source, a file path
// (how an edited script is re-run), or a saved workflow by name.
func (r *toolRuntime) resolveWorkflowScript(args workflowArgs) (script, origin string, err error) {
	switch {
	case strings.TrimSpace(args.ScriptPath) != "":
		data, err := os.ReadFile(args.ScriptPath)
		if err != nil {
			return "", "", fmt.Errorf("workflow: read script_path: %w", err)
		}
		return string(data), args.ScriptPath, nil
	case strings.TrimSpace(args.Name) != "":
		src, path, err := workflow.Load(r.cwd, args.Name)
		if err != nil {
			return "", "", fmt.Errorf("workflow: %w", err)
		}
		return src, path, nil
	case strings.TrimSpace(args.Script) != "":
		return args.Script, "inline", nil
	}
	return "", "", fmt.Errorf("workflow: pass script (inline source), script_path, or name")
}

// workflowRunDir is where a run's script, journal and result live. It sits
// under the session directory so /storage accounts for it and prunes it with
// the conversation it belongs to.
func (r *toolRuntime) workflowRunDir(runID string) string {
	base := r.sessionDir
	if strings.TrimSpace(base) == "" {
		base = filepath.Join(r.cwd, ".spettro")
	}
	return filepath.Join(base, "workflows", runID)
}

// findWorkflowRunDir locates a previous run by id.
//
// Run directories are session-scoped, but the run being resumed very often is
// not from this session — an editor opens a fresh one per prompt, and a
// crashed run is exactly the case you want to resume from a new session. So
// the current session is only the first place to look: the script's own
// directory is checked next (re-running <run>/script.js is the documented way
// to resume an edited script), then the sibling sessions, since run ids are
// unique. Not finding it is an error rather than an empty cache: silently
// replaying nothing would re-run every agent at full price under a flag that
// promised the opposite.
func (r *toolRuntime) findWorkflowRunDir(runID, scriptPath string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" || strings.ContainsAny(runID, `/\`) || strings.Contains(runID, "..") {
		return "", fmt.Errorf("invalid resume_from_run_id %q", runID)
	}
	var candidates []string
	if dir := r.workflowRunDir(runID); dir != "" {
		candidates = append(candidates, dir)
	}
	if scriptPath != "" {
		if parent := filepath.Dir(scriptPath); filepath.Base(parent) == runID {
			candidates = append(candidates, parent)
		}
	}
	if sessions := filepath.Dir(strings.TrimRight(r.sessionDir, string(filepath.Separator))); sessions != "" && r.sessionDir != "" {
		if entries, err := os.ReadDir(sessions); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					candidates = append(candidates, filepath.Join(sessions, e.Name(), "workflows", runID))
				}
			}
		}
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "journal.jsonl")); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("resume_from_run_id %q: no journal found for that run (looked under this session and its siblings)", runID)
}

// newWorkflowRunID mints a run id that sorts by start time and never collides.
// The random tail is not decoration: the timestamp has one-second resolution,
// so concurrent runs — or two spettro processes in the same project — would
// otherwise share a run directory and interleave their journals.
func newWorkflowRunID() string {
	return fmt.Sprintf("wf_%s_%s", time.Now().UTC().Format("20060102150405"), uuid.NewString()[:8])
}

// workflowRunner executes one agent() call as a Spettro sub-agent.
type workflowRunner struct {
	rt    *toolRuntime
	runID string
}

func (w *workflowRunner) RunAgent(ctx context.Context, req workflow.Request) (workflow.Response, error) {
	r := w.rt
	spec, err := resolveWorkflowTarget(r.manifest, req.AgentType)
	if err != nil {
		return workflow.Response{}, err
	}
	if p := r.perm(); p != "" {
		spec.Permission = p
	}
	cwd := r.cwd
	var ws *agentWorkspace
	if req.Isolation == "worktree" {
		ws, err = r.newSubagentWorkspace(ctx, req.Instance)
		if err != nil {
			return workflow.Response{}, fmt.Errorf("workflow: %w", err)
		}
		cwd = ws.subCWD
	}

	sub := LLMAgent{
		Spec:            spec,
		InstanceID:      req.Instance,
		PermissionFn:    r.permissionFn,
		ProviderManager: r.providerMgr,
		ProviderName:    r.providerName,
		ModelName:       r.modelName,
		CWD:             cwd,
		MaxTokens:       r.maxTokens,
		Thinking:        r.thinkingLevel,
		ToolCallback:    r.toolCallback,
		Checkpoint:      r.checkpoint,
		ShellApproval:   r.shellApproval,
		AskUser:         r.askUser,
		Manifest:        r.manifest,
		SandboxState:    r.sandboxState,
		SessionDir:      r.sessionDir,
		DelegationDepth: r.delegationDepth + 1,
		ParentAgentID:   r.agentID,
	}
	// A script may pin a call to a cheaper or stronger model than the session
	// is on ("scan with the fast one, judge with the strong one"), and to a
	// different reasoning effort. Both are per-call overrides; unset means
	// inherit, which is almost always what a script wants.
	if req.Model != "" {
		model := req.Model
		sub.ModelName = func() string { return model }
	}
	if level, ok := workflowEffort(req.Effort); ok {
		sub.Thinking = level
	}

	resp, runErr := w.runWithRetries(ctx, sub, req.Prompt)
	if ws != nil {
		// A failed member's work is preserved rather than merged; a successful
		// one is folded back. Both run on an uncancellable context so a
		// cancelled workflow still cleans its worktrees up.
		mergeCtx := context.WithoutCancel(ctx)
		if runErr != nil || ctx.Err() != nil {
			if kept := ws.abandon(mergeCtx); kept != nil && runErr != nil {
				runErr = fmt.Errorf("%w (work preserved on branch %s at %s)", runErr, kept.Branch, kept.Path)
			}
		} else if m := ws.finalize(mergeCtx); m.Status == "conflict" || m.Status == "error" {
			resp.Text += fmt.Sprintf("\n\n[workspace merge %s: branch %q kept at %s]", m.Status, m.Branch, m.Path)
		}
	}
	return resp, runErr
}

// runWithRetries retries transient provider failures per agent, matching the
// Ultra backoff so a rate-limited swarm behaves the same either way.
func (w *workflowRunner) runWithRetries(ctx context.Context, sub LLMAgent, prompt string) (workflow.Response, error) {
	var lastErr error
	for attempt := range workflowMaxAttempts {
		if attempt > 0 {
			if !ultraSleep(ctx, workflowRetryBase<<(attempt-1)) {
				return workflow.Response{}, ctx.Err()
			}
		}
		result, err := sub.Run(ctx, prompt)
		if err == nil {
			return workflow.Response{Text: strings.TrimSpace(result.Content), Tokens: result.TokensUsed}, nil
		}
		lastErr = err
		if ctx.Err() != nil || !provider.Classify(err).Transient() {
			break
		}
	}
	return workflow.Response{}, lastErr
}

// resolveWorkflowTarget picks the manifest agent an agent() call runs as. It
// mirrors the ultra rule — workers and subagents only, never an orchestrator —
// so a script cannot smuggle a nested swarm in through a phase.
//
// The default is the general-purpose subagent rather than the code worker:
// workflow stages are usually "read this and judge it", and a specialist would
// be the wrong shape for most of them. Manifests predating that agent fall
// back to whatever ultra would have picked.
func resolveWorkflowTarget(manifest *config.AgentManifest, agentType string) (config.AgentSpec, error) {
	target := strings.TrimSpace(agentType)
	if target == "" {
		target = "general-purpose"
		if _, ok := manifest.AgentByID(target); !ok {
			target = ""
		}
	}
	spec, err := resolveUltraTarget(manifest, target)
	if err != nil {
		return spec, fmt.Errorf("workflow: %s", strings.TrimPrefix(err.Error(), "ultra: "))
	}
	return spec, nil
}

// defaultWorkflowAgentType is the agent an unqualified agent() call resolves
// to, resolved once so instance names in the panel read "general-purpose#7"
// instead of a generic "agent#7".
func defaultWorkflowAgentType(manifest *config.AgentManifest) string {
	spec, err := resolveWorkflowTarget(manifest, "")
	if err != nil {
		return ""
	}
	return spec.ID
}

func workflowEffort(effort string) (provider.ThinkingLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "off", "none":
		return provider.ThinkingOff, true
	case "low":
		return provider.ThinkingLow, true
	case "medium":
		return provider.ThinkingMedium, true
	case "high":
		return provider.ThinkingHigh, true
	case "xhigh", "x-high":
		return provider.ThinkingXHigh, true
	case "max":
		return provider.ThinkingMax, true
	}
	return "", false
}
