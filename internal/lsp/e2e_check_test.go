package lsp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

// TestZeroConfigClangd exercises the full zero-config path against a real
// clangd when one is on PATH: no lsp.json, workspace manager auto-created,
// diagnostics returned for a broken C file.
func TestZeroConfigClangd(t *testing.T) {
	if _, err := lookPath("clangd"); err != nil {
		t.Skip("clangd not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.c"), []byte("int main() { return x; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := &lockedBuffer{}
	stderrSink = log
	t.Cleanup(func() { stderrSink = nil })
	m := ForWorkspace(dir)
	if m == nil {
		t.Fatal("expected auto-detected manager with zero config")
	}
	// clangd keeps handles on the workspace (its .cache/clangd index), which
	// makes the TempDir cleanup fail on Windows unless it is gone first.
	t.Cleanup(m.Shutdown)
	// The budget covers a cold clangd: the first parse on a CI runner pays for
	// process start, the initialize handshake and the toolchain probe (finding
	// the MSVC headers on Windows) before any diagnostic is published.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	out, err := m.DiagnosticsForFile(ctx, filepath.Join(dir, "bad.c"))
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		// Say which failure this is — nothing published at all (clangd too
		// slow, or unhappy about the toolchain it found) versus published
		// under a path that did not match ours — and hand over clangd's own
		// log. This test can only fail on a machine we are not sitting at.
		t.Fatalf("expected a diagnostic for undeclared identifier x after %s\nserver published: %v\nclangd log:\n%s",
			time.Since(start).Round(time.Millisecond), publishedDocs(m), log.String())
	}
	t.Log(out)
}

// lockedBuffer collects a server's stderr while it keeps writing.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns the log, trimmed in the middle when long: the head says what
// the server was configured to do and the tail says where it got stuck, and a
// full indexing run in between is not worth pasting into CI output.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	const half = 4 << 10
	s := b.buf.String()
	if len(s) > 2*half {
		s = s[:half] + fmt.Sprintf("\n...[%d bytes elided]...\n", len(s)-2*half) + s[len(s)-half:]
	}
	return s
}

// publishedDocs reports every document the running servers have published
// diagnostics for, as "<server key>: <path> (<n> diagnostics)".
func publishedDocs(m *Manager) []string {
	m.mu.Lock()
	clients := make(map[string]*Client, len(m.clients))
	for key, c := range m.clients {
		clients[key] = c
	}
	m.mu.Unlock()
	out := []string{}
	for key, c := range clients {
		for path, ds := range c.allDiagnostics() {
			out = append(out, fmt.Sprintf("%s: %s (%d diagnostics)", key, path, len(ds)))
		}
	}
	sort.Strings(out)
	return out
}
