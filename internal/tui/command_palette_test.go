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

func TestUpdateMain_UpArrowRecallsPreviousInput(t *testing.T) {
	ta := textarea.New()
	ta.Focus()

	m := Model{
		ta:           ta,
		inputHistory: []string{"first prompt", "second prompt"},
		historyIndex: -1,
	}

	gotModel, _ := m.updateMain(tea.KeyMsg{Type: tea.KeyUp})
	got := gotModel.(Model)

	if got.ta.Value() != "second prompt" {
		t.Fatalf("expected latest prompt recalled, got %q", got.ta.Value())
	}

	gotModel, _ = got.updateMain(tea.KeyMsg{Type: tea.KeyUp})
	got = gotModel.(Model)

	if got.ta.Value() != "first prompt" {
		t.Fatalf("expected older prompt after second up, got %q", got.ta.Value())
	}
}

func TestUpdateMain_DownArrowRestoresDraftAfterHistory(t *testing.T) {
	ta := textarea.New()
	ta.Focus()
	ta.SetValue("draft")

	m := Model{
		ta:           ta,
		inputHistory: []string{"first prompt", "second prompt"},
		historyIndex: -1,
	}

	gotModel, _ := m.updateMain(tea.KeyMsg{Type: tea.KeyUp})
	got := gotModel.(Model)
	gotModel, _ = got.updateMain(tea.KeyMsg{Type: tea.KeyDown})
	got = gotModel.(Model)

	if got.ta.Value() != "draft" {
		t.Fatalf("expected original draft restored, got %q", got.ta.Value())
	}
	if got.historyBrowsing {
		t.Fatal("expected history browsing mode to exit")
	}
}
