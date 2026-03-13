package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestFormatShellApprovalPrompt_ChoicesUnderCommand(t *testing.T) {
	prompt := formatShellApprovalPrompt("echo hi")

	commandIdx := strings.Index(prompt, "Bash(echo hi)")
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

func TestUpdateShellApproval_Option4DisablesYesNoShortcuts(t *testing.T) {
	ta := textarea.New()
	ta.Focus()
	m := Model{
		ta: ta,
		pendingAuth: &shellApprovalRequestMsg{
			response: make(chan shellApprovalResponse, 1),
		},
	}

	gotModel, _ := m.updateShellApproval(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	got := gotModel.(Model)
	if !got.approvalAltMode {
		t.Fatal("expected alternative mode after pressing 4")
	}

	gotModel, _ = got.updateShellApproval(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got = gotModel.(Model)
	if got.pendingAuth == nil {
		t.Fatal("pending approval should not resolve when typing alternative text")
	}
	if strings.TrimSpace(got.ta.Value()) != "y" {
		t.Fatalf("expected typed text to stay in textarea, got %q", got.ta.Value())
	}
}
