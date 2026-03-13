package app

import (
	"strings"
	"testing"
)

func TestFormatShellApprovalPrompt_ChoicesUnderCommand(t *testing.T) {
	prompt := formatShellApprovalPrompt("ls -la")

	commandIdx := strings.Index(prompt, "Bash(ls -la)")
	choiceIdx := strings.Index(prompt, "1) yes")
	if commandIdx == -1 || choiceIdx == -1 {
		t.Fatalf("prompt missing expected parts: %q", prompt)
	}
	if choiceIdx <= commandIdx {
		t.Fatalf("choices should appear after command, got: %q", prompt)
	}
	if !strings.Contains(prompt, "4) tell the agent what to do instead") {
		t.Fatalf("prompt missing alternative option: %q", prompt)
	}
}
