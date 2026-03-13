package agent

// White-box tests for unexported parser helpers.
// These must live in the same package because the tested functions are unexported.

import (
	"strings"
	"testing"
)

func TestParseToolCall_Valid(t *testing.T) {
	call, ok, err := parseToolCall(`TOOL_CALL {"tool":"file-read","args":{"path":"go.mod"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if call.Tool != "file-read" {
		t.Errorf("expected tool=file-read, got %q", call.Tool)
	}
}

func TestParseToolCall_NotACall(t *testing.T) {
	_, ok, err := parseToolCall("FINAL\nhello")
	if err != nil || ok {
		t.Errorf("expected ok=false, err=nil; got ok=%v err=%v", ok, err)
	}
}

func TestParseToolCall_BadJSON(t *testing.T) {
	_, ok, err := parseToolCall(`TOOL_CALL {bad json}`)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
	if !ok {
		t.Error("expected ok=true (starts with TOOL_CALL but has bad JSON)")
	}
}

func TestParseFinal(t *testing.T) {
	out, ok := parseFinal("FINAL\nSPETTRO.md created.")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if out != "SPETTRO.md created." {
		t.Errorf("unexpected content: %q", out)
	}
}

func TestParseFinal_NotFinal(t *testing.T) {
	_, ok := parseFinal("TOOL_CALL something")
	if ok {
		t.Error("expected ok=false")
	}
}

func TestStripLeakedToolCalls(t *testing.T) {
	input := "Here is a result.\nTOOL_CALL {\"tool\":\"file-read\",\"args\":{}}\nSome conclusion."
	out := stripLeakedToolCalls(input)
	if strings.Contains(out, "TOOL_CALL") {
		t.Errorf("TOOL_CALL line not stripped: %q", out)
	}
	if !strings.Contains(out, "Some conclusion.") {
		t.Errorf("expected conclusion to survive: %q", out)
	}
}
