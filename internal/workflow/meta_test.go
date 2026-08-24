package workflow

import (
	"strings"
	"testing"
)

func TestParseMetaFull(t *testing.T) {
	m, err := ParseMeta(`export const meta = {
  name: 'review-changes',
  description: 'Review the diff', // trailing comment with a } brace
  whenToUse: 'when a diff needs a second pair of eyes',
  phases: [
    { title: 'Review', detail: 'one agent per dimension' },
    { title: 'Verify', detail: 'refute each finding }' },
  ],
}
return 1`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Name != "review-changes" || m.Description != "Review the diff" {
		t.Fatalf("meta = %+v", m)
	}
	if m.WhenToUse == "" {
		t.Fatalf("whenToUse lost: %+v", m)
	}
	if len(m.Phases) != 2 || m.Phases[1].Title != "Verify" || m.Phases[1].Detail != "refute each finding }" {
		t.Fatalf("phases = %+v", m.Phases)
	}
}

func TestParseMetaRejectsComputedValues(t *testing.T) {
	cases := map[string]string{
		"function call": `export const meta = {name: buildName(), description: 'x'}`,
		"variable":      `export const meta = {name: NAME, description: 'x'}`,
		"template":      "export const meta = {name: `flow-${suffix}`, description: 'x'}",
	}
	for label, script := range cases {
		if _, err := ParseMeta(script); err == nil {
			t.Fatalf("%s: expected rejection", label)
		}
	}
}

func TestParseMetaRequiresFields(t *testing.T) {
	if _, err := ParseMeta(`export const meta = {description: 'x'}`); err == nil ||
		!strings.Contains(err.Error(), "meta.name is required") {
		t.Fatalf("want a name error, got %v", err)
	}
	if _, err := ParseMeta(`export const meta = {name: 'x'}`); err == nil ||
		!strings.Contains(err.Error(), "meta.description is required") {
		t.Fatalf("want a description error, got %v", err)
	}
	if _, err := ParseMeta(`export const meta = {name: 'x', description: 'y', phases: [{detail: 'no title'}]}`); err == nil ||
		!strings.Contains(err.Error(), "title is required") {
		t.Fatalf("want a phase title error, got %v", err)
	}
}

func TestParseMetaRequiresHeader(t *testing.T) {
	if _, err := ParseMeta("const meta = {name: 'x', description: 'y'}"); err == nil {
		t.Fatal("a script without the export header must be rejected")
	}
}

// The header token must be matched as a declaration, not as text: a script
// that merely mentions the phrase in a prompt string still needs a real one.
func TestParseMetaIgnoresTheTokenInsidePrompts(t *testing.T) {
	script := `export const meta = {name: 'x', description: 'y'}
await agent("rewrite this line: export const meta = {}")`
	m, err := ParseMeta(script)
	if err != nil || m.Name != "x" {
		t.Fatalf("meta = %+v, err = %v", m, err)
	}
	stripped := stripMetaExport(script)
	if strings.Count(stripped, "export const meta") != 1 {
		t.Fatalf("stripMetaExport touched the prompt string:\n%s", stripped)
	}
}
