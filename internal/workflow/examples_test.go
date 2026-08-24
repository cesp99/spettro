package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The example scripts are documentation users are told to copy and run, so a
// syntax error or a malformed header in one is a shipped bug. This compiles
// every one of them.
func TestShippedExamplesAreValid(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "examples", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read examples: %v", err)
	}
	found := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		found++
		t.Run(e.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			meta, err := Validate(string(data))
			if err != nil {
				t.Fatalf("%s does not validate: %v", e.Name(), err)
			}
			// The file name is how a user refers to it, so it has to match.
			if want := strings.TrimSuffix(e.Name(), ".js"); meta.Name != want {
				t.Fatalf("meta.name = %q, want %q to match the file name", meta.Name, want)
			}
			if len(meta.Phases) == 0 {
				t.Fatalf("%s declares no phases, so the panel cannot show its plan", e.Name())
			}
		})
	}
	if found == 0 {
		t.Fatal("no example workflows found — the docs reference them")
	}
}

func TestValidateRejectsBadScripts(t *testing.T) {
	if _, err := Validate("export const meta = {name: 'x', description: 'y'}\nreturn ((("); err == nil {
		t.Fatal("a syntax error must be reported by Validate, not at run time")
	}
	if _, err := Validate("return 1"); err == nil {
		t.Fatal("a missing header must be reported")
	}
}
