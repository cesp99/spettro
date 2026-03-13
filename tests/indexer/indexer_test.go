package indexer_test

import (
	"os"
	"path/filepath"
	"testing"

	"spettro/internal/indexer"
)

func TestBuildSkipsGitAndSpettro(t *testing.T) {
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	mustWrite(t, filepath.Join(root, ".git", "x.txt"), "nope")
	mustWrite(t, filepath.Join(root, ".spettro", "index.json"), "{}")

	snap, err := indexer.Build(root)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 indexed file, got %d", len(snap.Entries))
	}
	if snap.Entries[0].Path != "main.go" {
		t.Fatalf("unexpected file path: %s", snap.Entries[0].Path)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
