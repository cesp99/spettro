package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestShellApprovalOptions_ContainExpectedChoices(t *testing.T) {
	if len(shellApprovalOptions) < 3 {
		t.Fatalf("expected at least 3 approval options, got %d", len(shellApprovalOptions))
	}
	joined := strings.Join(shellApprovalOptions, "\n")
	for _, want := range []string{"Allow once", "Allow always", "Deny"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("approval options missing %q: %v", want, shellApprovalOptions)
		}
	}
}

func TestUpdateShellApproval_Option3EntersAlternativeTextMode(t *testing.T) {
	ta := textarea.New()
	ta.Focus()
	m := Model{
		ta: ta,
		pendingAuth: &shellApprovalRequestMsg{
			response: make(chan shellApprovalResponse, 1),
		},
		approvalCursor: 3, // "Tell the agent what to do instead"
	}

	// Typing regular text should be forwarded to the textarea in alternative mode
	gotModel, _ := m.updateShellApproval(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := gotModel.(Model)
	if got.pendingAuth == nil {
		t.Fatal("pending approval should not resolve when typing alternative text")
	}
	if strings.TrimSpace(got.ta.Value()) != "y" {
		t.Fatalf("expected typed text to stay in textarea, got %q", got.ta.Value())
	}
}

func TestUpdateShellApproval_DownMovesCursor(t *testing.T) {
	ta := textarea.New()
	m := Model{
		ta: ta,
		pendingAuth: &shellApprovalRequestMsg{
			response: make(chan shellApprovalResponse, 1),
		},
		approvalCursor: 0,
	}

	gotModel, _ := m.updateShellApproval(tea.KeyMsg{Type: tea.KeyDown})
	got := gotModel.(Model)
	if got.approvalCursor != 1 {
		t.Fatalf("expected cursor 1 after down, got %d", got.approvalCursor)
	}
}
