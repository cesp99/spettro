package tui_test

import (
	"strings"
	"testing"

	"spettro/internal/config"
	"spettro/internal/tui"
)

func TestHandleCommand_TasksAddAndList(t *testing.T) {
	m := tui.NewModelForTesting()

	next, _ := m.HandleCommandForTesting("/tasks add write docs for new tools")
	got := next.(tui.Model)
	if !strings.Contains(strings.ToLower(got.BannerForTesting()), "task added") {
		t.Fatalf("expected success banner after add, got %q", got.BannerForTesting())
	}

	next, _ = got.HandleCommandForTesting("/tasks list")
	got = next.(tui.Model)
	msgs := got.MessagesForTesting()
	if len(msgs) == 0 {
		t.Fatal("expected system message for tasks list")
	}
	last := msgs[len(msgs)-1].Content
	if !strings.Contains(last, "write docs for new tools") {
		t.Fatalf("expected listed task content, got %q", last)
	}
}

func TestHandleCommand_PermissionsAliasSetsLevel(t *testing.T) {
	m := tui.NewModelForTesting()
	next, _ := m.HandleCommandForTesting("/permissions restricted")
	got := next.(tui.Model)
	if !strings.Contains(strings.ToLower(got.BannerForTesting()), "permission set to restricted") {
		t.Fatalf("expected permission set banner, got %q", got.BannerForTesting())
	}
}

func TestHandleCommand_ModeSwitchDoesNotOverwriteNewerConfigOnDisk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatalf("save default config: %v", err)
	}

	m := tui.NewModelForTesting()
	if _, err := config.Update(func(cfg *config.UserConfig) error {
		cfg.Permission = config.PermissionRestricted
		return nil
	}); err != nil {
		t.Fatalf("update config externally: %v", err)
	}

	next, _ := m.HandleCommandForTesting("/mode")
	_ = next.(tui.Model)

	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Permission != config.PermissionRestricted {
		t.Fatalf("expected external permission change to survive mode switch, got %s", reloaded.Permission)
	}
}
