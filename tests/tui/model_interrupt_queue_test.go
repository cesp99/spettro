package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"spettro/internal/tui"
)

func TestUpdateMain_EnterWhileThinkingQueuesPrompt(t *testing.T) {
	m := tui.NewModelForTesting()
	m.SetThinkingForTesting(true)
	m.SetTextareaValueForTesting("please review the latest change")

	gotModel, _ := m.UpdateMainForTesting(tea.KeyMsg{Type: tea.KeyEnter})
	got := gotModel.(tui.Model)

	if got.PendingPromptCountForTesting() != 1 {
		t.Fatalf("expected one queued prompt, got %d", got.PendingPromptCountForTesting())
	}
	if strings.TrimSpace(got.TextareaValueForTesting()) != "" {
		t.Fatalf("expected textarea to reset after queueing, got %q", got.TextareaValueForTesting())
	}
	msgs := got.MessagesForTesting()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "queued request") {
		t.Fatalf("expected queued request system message, got %+v", msgs)
	}
}

func TestUpdateMain_EscWhileThinkingPreservesProgressAndAsksInstead(t *testing.T) {
	m := tui.NewModelForTesting()
	m.SetThinkingForTesting(true)
	m.SetActiveAgentForTesting("coding")
	m.SetLiveToolsForTesting([]tui.ToolItem{{
		Name:   "file-read",
		Status: "success",
		Args:   `{"path":"internal/tui/model.go"}`,
	}}, &tui.ToolItem{
		Name:   "grep",
		Status: "running",
		Args:   `{"pattern":"approval"}`,
	})

	gotModel, _ := m.UpdateMainForTesting(tea.KeyMsg{Type: tea.KeyEsc})
	got := gotModel.(tui.Model)

	if !got.AwaitingInsteadForTesting() {
		t.Fatal("expected esc interrupt to wait for replacement instruction")
	}
	if got.BannerForTesting() != "what should I do instead?" {
		t.Fatalf("unexpected banner: %q", got.BannerForTesting())
	}
	msgs := got.MessagesForTesting()
	if len(msgs) == 0 {
		t.Fatal("expected interrupt summary message")
	}
	last := msgs[len(msgs)-1].Content
	if !strings.Contains(last, "Progress kept:") || !strings.Contains(last, "Read internal/tui/model.go") {
		t.Fatalf("expected preserved progress summary, got %q", last)
	}
}

func TestUpdateShellApproval_DenyInterruptsAndAsksInstead(t *testing.T) {
	m := tui.NewModelForTesting()
	m.SetThinkingForTesting(true)
	m.SetPendingShellApprovalForTesting(2)

	gotModel, _ := m.UpdateShellApprovalForTesting(tea.KeyMsg{Type: tea.KeyEnter})
	got := gotModel.(tui.Model)

	if !got.AwaitingInsteadForTesting() {
		t.Fatal("expected denial to enter replacement-instruction mode")
	}
	if got.HasPendingShellApprovalForTesting() {
		t.Fatal("expected pending shell approval to resolve")
	}
	if got.BannerForTesting() != "what should I do instead?" {
		t.Fatalf("unexpected banner: %q", got.BannerForTesting())
	}
}
