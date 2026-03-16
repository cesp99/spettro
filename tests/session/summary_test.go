package session_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spettro/internal/conversation"
	"spettro/internal/session"
)

func TestListUsesGlobalSessionsOnlyAndSortsByLastChat(t *testing.T) {
	globalDir := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	older := session.State{
		Metadata: session.Metadata{
			ID:          "session-" + session.ProjectHash(cwd) + "-older",
			ProjectPath: cwd,
			ProjectHash: session.ProjectHash(cwd),
			StartedAt:   time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 3, 15, 10, 5, 0, 0, time.UTC),
		},
		Messages: []session.Message{
			{Role: "user", Content: "older first message", At: time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)},
			{Role: "assistant", Content: "reply", At: time.Date(2026, 3, 15, 10, 1, 0, 0, time.UTC)},
		},
	}
	if err := session.Save(globalDir, older); err != nil {
		t.Fatalf("save older session: %v", err)
	}

	newer := session.State{
		Metadata: session.Metadata{
			ID:          "session-" + session.ProjectHash(cwd) + "-newer",
			ProjectPath: cwd,
			ProjectHash: session.ProjectHash(cwd),
			StartedAt:   time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 3, 16, 8, 30, 0, 0, time.UTC),
		},
		Messages: []session.Message{
			{Role: "user", Content: "newer first message", At: time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)},
			{Role: "assistant", Content: "reply", At: time.Date(2026, 3, 16, 8, 30, 0, 0, time.UTC)},
		},
	}
	if err := session.Save(globalDir, newer); err != nil {
		t.Fatalf("save newer session: %v", err)
	}

	legacyDir := conversation.ProjectDir(globalDir, cwd)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	legacy := conversation.Conversation{
		ID:        "legacy-conv",
		StartedAt: time.Date(2026, 3, 16, 9, 0, 0, 0, time.UTC),
		Messages: []conversation.Message{
			{Role: "user", Content: "legacy message", At: time.Date(2026, 3, 16, 9, 0, 0, 0, time.UTC)},
		},
	}
	raw, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy conversation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, legacy.ID+".json"), raw, 0o644); err != nil {
		t.Fatalf("write legacy conversation: %v", err)
	}

	items, err := session.List(globalDir, cwd)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected only global sessions, got %d items", len(items))
	}
	if items[0].ID != newer.Metadata.ID {
		t.Fatalf("expected newest updated session first, got %s", items[0].ID)
	}
	if items[0].Preview != "newer first message" {
		t.Fatalf("expected first user message as preview, got %q", items[0].Preview)
	}
	if items[1].ID != older.Metadata.ID {
		t.Fatalf("expected older session second, got %s", items[1].ID)
	}
}

func TestListIncludesMatchingSessionsRegardlessOfFolderName(t *testing.T) {
	globalDir := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	state := session.State{
		Metadata: session.Metadata{
			ID:          "20260314-170237",
			ProjectPath: cwd,
			ProjectHash: session.ProjectHash(cwd),
			StartedAt:   time.Date(2026, 3, 14, 17, 1, 45, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 3, 16, 3, 5, 7, 0, time.UTC),
		},
		Messages: []session.Message{
			{Role: "user", Content: "resume me", At: time.Date(2026, 3, 14, 17, 1, 45, 0, time.UTC)},
		},
	}
	if err := session.Save(globalDir, state); err != nil {
		t.Fatalf("save session: %v", err)
	}

	items, err := session.List(globalDir, cwd)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 matching session, got %d", len(items))
	}
	if items[0].ID != state.Metadata.ID {
		t.Fatalf("expected session %s, got %s", state.Metadata.ID, items[0].ID)
	}
}
