package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"spettro/internal/tui"
)

func TestSidePanelWidth_StaysVisibleWhenEnabledWithoutActivity(t *testing.T) {
	m := tui.NewModelForTesting()
	m.SetDimensionsForTesting(140, 40)
	m.SetSidePanelVisibleForTesting(true)

	if got := m.SidePanelWidthForTesting(); got == 0 {
		t.Fatal("expected activity panel to stay visible when enabled")
	}
}

func TestViewSidePanel_ShowsExpandedContextWithoutToolCallSyntax(t *testing.T) {
	m := tui.NewModelForTesting()
	m.SetDimensionsForTesting(140, 40)
	m.SetSidePanelVisibleForTesting(true)
	m.SetShowToolsForTesting(true)
	body := tui.SanitizeToolOutputForTesting("matched internal/tui/model.go\nTOOL_CALL {\"tool\":\"grep\"}", 24)
	m.AddActivityForTesting(
		"tool",
		"grep",
		"coding",
		`Grep "approval"`,
		`Searches file contents for "approval".`,
		"Searches file contents for \"approval\".\n\n"+body,
		"done",
	)

	view := m.ViewSidePanelForTesting(m.SidePanelWidthForTesting())
	if !strings.Contains(view, `Searches file contents for "approval".`) {
		t.Fatalf("expected expanded tool context in side panel, got: %s", view)
	}
	if strings.Contains(view, "TOOL_CALL") {
		t.Fatalf("expected TOOL_CALL syntax to be hidden, got: %s", view)
	}
}

func TestViewSidePanel_IsHeightBounded(t *testing.T) {
	m := tui.NewModelForTesting()
	m.SetDimensionsForTesting(140, 32)
	m.SetSidePanelVisibleForTesting(true)
	m.SetShowToolsForTesting(true)
	m.AddActivityForTesting(
		"message",
		"assistant",
		"coding",
		"Assistant response",
		"Long summary",
		strings.Repeat("line of context\n", 80),
		"done",
	)

	view := m.ViewSidePanelForTesting(m.SidePanelWidthForTesting())
	if got := lipgloss.Height(view); got > 32 {
		t.Fatalf("expected side panel height to stay within terminal, got %d lines", got)
	}
}

func TestViewSidePanel_ShowsGitBranchAndChanges(t *testing.T) {
	m := tui.NewModelForTesting()
	m.SetDimensionsForTesting(140, 40)
	m.SetSidePanelVisibleForTesting(true)
	m.SetGitBranchForTesting("feature/activity-panel")
	m.AddModifiedFileForTesting("internal/tui/model_view.go", 32, 10, false, true, true)
	m.AddModifiedFileForTesting("tests/tui/activity_panel_test.go", 18, 0, false, true, false)
	m.AddModifiedFileForTesting("tmp/new-file.txt", 0, 0, true, false, false)
	m.AddActivityForTesting(
		"tool",
		"grep",
		"coding",
		`Grep "panel"`,
		`Search panel references`,
		"Search panel references",
		"done",
	)

	view := m.ViewSidePanelForTesting(m.SidePanelWidthForTesting())
	for _, want := range []string{
		"⎇",
		"feature/activity-panel",
		"spettro-tui-tests",
		"+50",
		"-10",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in side panel, got: %s", want, view)
		}
	}
	if strings.Contains(view, "internal/tui/model_view.go") {
		t.Fatalf("expected git file list to stay hidden, got: %s", view)
	}
}
