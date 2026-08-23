package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkflow(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".js"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAndLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()

	writeWorkflow(t, filepath.Join(project, ".spettro", SavedWorkflowsDir), "audit",
		"export const meta = {name: 'audit', description: 'project audit'}\nreturn 1")
	writeWorkflow(t, filepath.Join(home, ".spettro", SavedWorkflowsDir), "audit",
		"export const meta = {name: 'audit', description: 'global audit'}\nreturn 1")
	writeWorkflow(t, filepath.Join(home, ".spettro", SavedWorkflowsDir), "sweep",
		"export const meta = {name: 'sweep', description: 'global sweep'}\nreturn 1")
	writeWorkflow(t, filepath.Join(project, ".spettro", SavedWorkflowsDir), "broken", "no header here")

	found := Discover(project)
	if len(found) != 3 {
		t.Fatalf("found %d workflows: %+v", len(found), found)
	}
	byName := map[string]Saved{}
	for _, s := range found {
		byName[s.Name] = s
	}
	if byName["audit"].Meta.Description != "project audit" || byName["audit"].Scope != "project" {
		t.Fatalf("a project workflow must shadow the global one: %+v", byName["audit"])
	}
	if byName["sweep"].Scope != "global" {
		t.Fatalf("sweep = %+v", byName["sweep"])
	}
	if byName["broken"].Err == nil {
		t.Fatal("a workflow with an unparseable header must be listed with its error")
	}

	script, path, err := Load(project, "audit")
	if err != nil || !strings.Contains(script, "project audit") {
		t.Fatalf("load: %q %v", script, err)
	}
	if !strings.HasPrefix(path, project) {
		t.Fatalf("loaded path = %q, want the project copy", path)
	}
	if _, _, err := Load(project, "missing"); err == nil {
		t.Fatal("want an error for an unknown workflow")
	}
}

func TestLoadRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"../escape", "sub/flow", "..", `back\slash`} {
		if _, _, err := Load(t.TempDir(), name); err == nil || !strings.Contains(err.Error(), "invalid workflow name") {
			t.Fatalf("%q: want an invalid-name error, got %v", name, err)
		}
	}
}
