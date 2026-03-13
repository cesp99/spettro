package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdateMain_SecondEnterExecutesSelectedCommand(t *testing.T) {
	ta := textarea.New()
	ta.Focus()
	ta.SetValue("/help ")

	m := Model{
		ta:       ta,
		cmdItems: []commandDef{{name: "/help", desc: "show help"}},
	}

	gotModel, _ := m.updateMain(tea.KeyMsg{Type: tea.KeyEnter})
	got := gotModel.(Model)

	if len(got.messages) == 0 || !strings.Contains(got.messages[len(got.messages)-1].Content, "commands:") {
		t.Fatalf("expected /help to execute on second enter; got messages: %+v", got.messages)
	}
	if strings.TrimSpace(got.ta.Value()) != "" {
		t.Fatalf("expected input cleared after execution, got %q", got.ta.Value())
	}
}
