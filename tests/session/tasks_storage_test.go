package session_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"spettro/internal/session"
)

func TestUpsertAndGetTodo(t *testing.T) {
	t.Parallel()
	globalDir := t.TempDir()
	sessionID := "s-1"

	item := session.Todo{
		ID:        "task-1",
		Content:   "implement task CRUD",
		Status:    "pending",
		UpdatedAt: time.Now(),
	}
	if _, err := session.UpsertTodo(globalDir, sessionID, item); err != nil {
		t.Fatalf("upsert todo: %v", err)
	}

	got, ok, err := session.GetTodo(globalDir, sessionID, "task-1")
	if err != nil {
		t.Fatalf("get todo: %v", err)
	}
	if !ok {
		t.Fatal("expected todo to exist")
	}
	if got.Content != "implement task CRUD" {
		t.Fatalf("unexpected todo content: %q", got.Content)
	}
}

func TestLoadTodosFallsBackToLegacyTodosFile(t *testing.T) {
	t.Parallel()
	globalDir := t.TempDir()
	sessionID := "legacy-s"
	dir := filepath.Join(globalDir, "sessions", sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := []byte(`[{"id":"old-1","content":"legacy","status":"pending","updated_at":"2026-01-01T00:00:00Z"}]`)
	if err := os.WriteFile(filepath.Join(dir, "todos.json"), raw, 0o644); err != nil {
		t.Fatalf("write legacy todos: %v", err)
	}

	todos, err := session.LoadTodos(globalDir, sessionID)
	if err != nil {
		t.Fatalf("load todos: %v", err)
	}
	if len(todos) != 1 || todos[0].ID != "old-1" {
		t.Fatalf("unexpected loaded todos: %#v", todos)
	}
}
