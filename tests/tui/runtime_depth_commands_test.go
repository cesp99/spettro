package tui_test

import (
	"strings"
	"testing"

	"spettro/internal/tui"
)

func TestHandleCommand_CompactAutoToggle(t *testing.T) {
	m := tui.NewModelForTesting()
	next, _ := m.HandleCommandForTesting("/compact auto off")
	got := next.(tui.Model)
	if !strings.Contains(strings.ToLower(got.BannerForTesting()), "auto compact disabled") {
		t.Fatalf("expected auto compact disabled banner, got %q", got.BannerForTesting())
	}
	next, _ = got.HandleCommandForTesting("/compact auto status")
	got = next.(tui.Model)
	if !strings.Contains(strings.ToLower(got.BannerForTesting()), "auto compact") {
		t.Fatalf("expected status banner, got %q", got.BannerForTesting())
	}
}

func TestHandleCommand_CompactPolicyMessage(t *testing.T) {
	m := tui.NewModelForTesting()
	next, _ := m.HandleCommandForTesting("/compact policy")
	got := next.(tui.Model)
	msgs := got.MessagesForTesting()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "compact policy:") {
		t.Fatalf("expected compact policy output, got %#v", msgs)
	}
}

func TestHandleCommand_HooksNoConfig(t *testing.T) {
	m := tui.NewModelForTesting()
	next, _ := m.HandleCommandForTesting("/hooks")
	got := next.(tui.Model)
	msgs := got.MessagesForTesting()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "no hooks configured") {
		t.Fatalf("expected no hooks message, got %#v", msgs)
	}
}

func TestHandleCommand_PermissionsDebugToggle(t *testing.T) {
	m := tui.NewModelForTesting()
	next, _ := m.HandleCommandForTesting("/permissions debug on")
	got := next.(tui.Model)
	if !strings.Contains(strings.ToLower(got.BannerForTesting()), "debug enabled") {
		t.Fatalf("expected debug enabled banner, got %q", got.BannerForTesting())
	}
}
