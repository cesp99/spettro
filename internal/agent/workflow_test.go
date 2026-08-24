package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spettro/internal/config"
	"spettro/internal/provider"
	"spettro/internal/workflow"
)

func TestWorkflowRequested_WholeWordOnly(t *testing.T) {
	on := []string{
		"ultracode",
		"please ULTRACODE this refactor",
		"do it, ultracode.",
		"(ultracode)",
		"refactor the parser — ultracode",
	}
	for _, s := range on {
		if !WorkflowRequested(s) {
			t.Fatalf("%q should activate workflows", s)
		}
	}
	off := []string{
		"",
		"just do the thing",
		"open src/ultracoder.go",
		"rename ultracodes to something else",
		"the ultracode_helper function",
	}
	for _, s := range off {
		if WorkflowRequested(s) {
			t.Fatalf("%q should not activate workflows", s)
		}
	}
}

func TestResolveWorkflowTarget(t *testing.T) {
	manifest := config.DefaultAgentManifest()
	spec, err := resolveWorkflowTarget(&manifest, "")
	if err != nil {
		t.Fatalf("default target: %v", err)
	}
	if spec.ID != "general-purpose" {
		t.Fatalf("default target = %q, want general-purpose", spec.ID)
	}
	if spec, err = resolveWorkflowTarget(&manifest, "review"); err != nil || spec.ID != "review" {
		t.Fatalf("explicit target = %q, %v", spec.ID, err)
	}
	// An orchestrator would let a script nest a swarm inside a phase.
	if _, err := resolveWorkflowTarget(&manifest, "coding"); err == nil {
		t.Fatal("want an error for an orchestrator target")
	}
	if _, err := resolveWorkflowTarget(&manifest, "nope"); err == nil ||
		!strings.HasPrefix(err.Error(), "workflow:") {
		t.Fatalf("want a workflow-prefixed error, got %v", err)
	}
}

// A manifest that predates the general-purpose agent must still resolve a
// default rather than failing every unqualified agent() call.
func TestResolveWorkflowTargetFallsBackWithoutGeneralPurpose(t *testing.T) {
	manifest := config.DefaultAgentManifest()
	kept := manifest.Agents[:0]
	for _, a := range manifest.Agents {
		if a.ID != "general-purpose" {
			kept = append(kept, a)
		}
	}
	manifest.Agents = kept
	spec, err := resolveWorkflowTarget(&manifest, "")
	if err != nil {
		t.Fatalf("fallback target: %v", err)
	}
	if spec.ID != "code" {
		t.Fatalf("fallback target = %q, want code", spec.ID)
	}
}

func TestWorkflowEffortMapping(t *testing.T) {
	cases := map[string]provider.ThinkingLevel{
		"low": provider.ThinkingLow, "medium": provider.ThinkingMedium,
		"high": provider.ThinkingHigh, "xhigh": provider.ThinkingXHigh,
		"x-high": provider.ThinkingXHigh, "max": provider.ThinkingMax,
		"off": provider.ThinkingOff, "NONE": provider.ThinkingOff,
	}
	for in, want := range cases {
		got, ok := workflowEffort(in)
		if !ok || got != want {
			t.Fatalf("workflowEffort(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := workflowEffort("turbo"); ok {
		t.Fatal("an unknown effort must fall through to the session level")
	}
	if _, ok := workflowEffort(""); ok {
		t.Fatal("an unset effort must fall through to the session level")
	}
}

func TestRunWorkflowRejectsSubagentsAndAskFirst(t *testing.T) {
	manifest := config.DefaultAgentManifest()
	nested := &toolRuntime{delegationDepth: 1, manifest: &manifest}
	if _, err := nested.runWorkflow(context.Background(), json.RawMessage(`{"script":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "only the top-level agent") {
		t.Fatalf("want a depth error, got %v", err)
	}
	top := &toolRuntime{manifest: &manifest, providerMgr: &provider.Manager{}, permission: config.PermissionAskFirst}
	if _, err := top.runWorkflow(context.Background(), json.RawMessage(`{"script":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "restricted or yolo") {
		t.Fatalf("want a permission error, got %v", err)
	}
}

func TestRunWorkflowRequiresAScript(t *testing.T) {
	manifest := config.DefaultAgentManifest()
	rt := &toolRuntime{manifest: &manifest, providerMgr: &provider.Manager{}, permission: config.PermissionYOLO}
	_, err := rt.runWorkflow(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "pass script") {
		t.Fatalf("want a missing-script error, got %v", err)
	}
	_, err = rt.runWorkflow(context.Background(), json.RawMessage(`{"script":"return 1"}`))
	if err == nil || !strings.Contains(err.Error(), "export const meta") {
		t.Fatalf("want a missing-header error, got %v", err)
	}
}

func TestRenderWorkflowResult(t *testing.T) {
	out := renderWorkflowResult("wf_1", "/tmp/run", "inline", workflow.Meta{Name: "audit"}, workflow.Result{
		Value:  map[string]any{"confirmed": []any{"a", "b"}},
		Agents: 7, Failed: 2, Cached: 1, Tokens: 4200,
		Logs:   []string{"round 1: 3 found"},
		Phases: []string{"Find", "Verify"},
	})
	for _, want := range []string{
		`name="audit"`, `run_id="wf_1"`,
		"7 agents · 2 failed · 1 replayed from journal · 4200 tokens",
		"Find → Verify", "round 1: 3 found", `"confirmed"`,
		"resume_from_run_id", "Some agents failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("result missing %q:\n%s", want, out)
		}
	}
}

func TestRenderWorkflowResultCleanRun(t *testing.T) {
	out := renderWorkflowResult("wf_2", "/tmp/run", "inline", workflow.Meta{Name: "sweep"}, workflow.Result{
		Value: "all clear", Agents: 3,
	})
	if strings.Contains(out, "Some agents failed") {
		t.Fatalf("a clean run must not warn about failures:\n%s", out)
	}
	if !strings.Contains(out, "all clear") {
		t.Fatalf("a string return value should pass through verbatim:\n%s", out)
	}
}

func TestWorkflowRunIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id := newWorkflowRunID()
		if seen[id] {
			t.Fatalf("duplicate run id %q", id)
		}
		seen[id] = true
	}
}

// The tool must be injected by the keyword alone, without a host flag, so
// every surface gets workflows for free.
func TestWorkflowToolHasDescriptionAndSchema(t *testing.T) {
	specs := buildToolSpecs([]string{workflowToolID})
	if len(specs) != 1 {
		t.Fatalf("workflow tool is not exposed to the model: %+v", specs)
	}
	var schema map[string]any
	if err := json.Unmarshal(specs[0].Schema, &schema); err != nil {
		t.Fatalf("schema does not parse: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"script", "script_path", "name", "args", "resume_from_run_id"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("schema is missing %q", key)
		}
	}
}

func TestFanOutToolsGrantsByModeAndDepth(t *testing.T) {
	base := []string{"file-read", "bash"}

	tools, prompt := fanOutTools(base, false, false, 0)
	if len(tools) != 2 || prompt != "" {
		t.Fatalf("no mode on should grant nothing: %v %q", tools, prompt)
	}

	tools, prompt = fanOutTools(base, false, true, 0)
	if !contains(tools, workflowToolID) || contains(tools, ultraToolID) {
		t.Fatalf("workflows alone should grant only the workflow tool: %v", tools)
	}
	if !strings.Contains(prompt, "WORKFLOWS are active") || strings.Contains(prompt, "ULTRA MODE") {
		t.Fatalf("wrong guidance: %q", prompt)
	}

	tools, prompt = fanOutTools(base, true, true, 0)
	if !contains(tools, workflowToolID) || !contains(tools, ultraToolID) {
		t.Fatalf("both modes should grant both tools: %v", tools)
	}
	if !strings.Contains(prompt, "ULTRA MODE") || !strings.Contains(prompt, "WORKFLOWS are active") {
		t.Fatalf("both guidance sections expected: %q", prompt)
	}

	// A sub-agent never orchestrates: that is what stops swarms of swarms.
	tools, prompt = fanOutTools(base, true, true, 1)
	if len(tools) != 2 || prompt != "" {
		t.Fatalf("sub-agents must get neither tool: %v %q", tools, prompt)
	}

	// Granting twice must not duplicate an already allow-listed tool.
	tools, _ = fanOutTools([]string{"workflow"}, false, true, 0)
	if len(tools) != 1 {
		t.Fatalf("duplicate grant: %v", tools)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestFindWorkflowRunDir(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	thisSession := filepath.Join(sessions, "session-a")
	otherSession := filepath.Join(sessions, "session-b")

	mkRun := func(sessionDir, runID string) string {
		dir := filepath.Join(sessionDir, "workflows", runID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "journal.jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	mine := mkRun(thisSession, "wf_mine")
	theirs := mkRun(otherSession, "wf_theirs")

	r := &toolRuntime{sessionDir: thisSession}

	if got, err := r.findWorkflowRunDir("wf_mine", ""); err != nil || got != mine {
		t.Fatalf("own session: %q %v", got, err)
	}
	// The common case: an editor opens a fresh session per prompt, so the run
	// being resumed lives under a sibling.
	if got, err := r.findWorkflowRunDir("wf_theirs", ""); err != nil || got != theirs {
		t.Fatalf("sibling session: %q %v", got, err)
	}
	// Re-running <run>/script.js is the documented resume path, so the
	// script's own directory counts even outside the session tree.
	loose := filepath.Join(root, "elsewhere", "wf_loose")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loose, "journal.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := r.findWorkflowRunDir("wf_loose", filepath.Join(loose, "script.js")); err != nil || got != loose {
		t.Fatalf("script-adjacent run: %q %v", got, err)
	}
	// A run that does not exist must be an error, not a silent full re-run.
	if _, err := r.findWorkflowRunDir("wf_missing", ""); err == nil ||
		!strings.Contains(err.Error(), "no journal found") {
		t.Fatalf("want a not-found error, got %v", err)
	}
	for _, bad := range []string{"", "../escape", "a/b"} {
		if _, err := r.findWorkflowRunDir(bad, ""); err == nil ||
			!strings.Contains(err.Error(), "invalid resume_from_run_id") {
			t.Fatalf("%q: want an invalid-id error, got %v", bad, err)
		}
	}
}
