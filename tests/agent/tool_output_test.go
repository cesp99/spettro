package agent_test

// Tests that verify the actual OUTPUT of each tool — what gets fed back to the LLM.
// Uses captureServer: a server that records the full request body on each call,
// so we can inspect what tool results were included in the next prompt.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"spettro/internal/agent"
	"spettro/internal/config"
	"spettro/internal/provider"
)

// captureServer is like scriptedServer but also records every request body.
type captureServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	requests []string // captured request bodies in order
}

func newCaptureServer(t *testing.T, responses []string) *captureServer {
	t.Helper()
	cs := &captureServer{}
	idx := 0
	cs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cs.mu.Lock()
		cs.requests = append(cs.requests, string(body))
		i := idx
		idx++
		cs.mu.Unlock()

		if i >= len(responses) {
			t.Errorf("unexpected extra request #%d", i+1)
			http.Error(w, "no more responses", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": responses[i]}, "finish_reason": "stop"},
			},
			"usage": map[string]any{"total_tokens": 30},
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	t.Cleanup(cs.srv.Close)
	return cs
}

// promptAt returns the decoded "messages[0].content" (the full user prompt) for request i.
func (cs *captureServer) promptAt(i int) string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if i >= len(cs.requests) {
		return ""
	}
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	json.Unmarshal([]byte(cs.requests[i]), &body) //nolint:errcheck
	if len(body.Messages) == 0 {
		return ""
	}
	return body.Messages[0].Content
}

func capturedCoder(cs *captureServer, dir string) agent.LLMCoder {
	pm := provider.NewManager()
	providerName := cs.srv.URL
	return agent.LLMCoder{
		ProviderManager: pm,
		ProviderName:    func() string { return providerName },
		ModelName:       func() string { return "fake-model" },
		CWD:             dir,
	}
}

// ── repo-search output ─────────────────────────────────────────────────────────

func TestToolOutput_RepoSearch_ListsFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)          //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module myapp\ngo 1.22\n"), 0o644) //nolint:errcheck

	cs := newCaptureServer(t, []string{
		`TOOL_CALL {"tool":"repo-search","args":{"query":""}}`,
		"FINAL\nDone.",
	})
	_, err := capturedCoder(cs, dir).Execute(context.Background(), "List files.", config.PermissionYOLO, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The second prompt (step 2) must contain the tool output listing both files.
	prompt2 := cs.promptAt(1)
	if !strings.Contains(prompt2, "main.go") {
		t.Errorf("repo-search output missing main.go in prompt:\n%s", prompt2)
	}
	if !strings.Contains(prompt2, "go.mod") {
		t.Errorf("repo-search output missing go.mod in prompt:\n%s", prompt2)
	}
}

func TestToolOutput_RepoSearch_ContentSearch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc HelloWorld() {}\n"), 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "other.go"), []byte("package main\n\nfunc Other() {}\n"), 0o644)     //nolint:errcheck

	cs := newCaptureServer(t, []string{
		`TOOL_CALL {"tool":"repo-search","args":{"query":"HelloWorld"}}`,
		"FINAL\nDone.",
	})
	_, err := capturedCoder(cs, dir).Execute(context.Background(), "Find HelloWorld.", config.PermissionYOLO, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	prompt2 := cs.promptAt(1)
	if !strings.Contains(prompt2, "hello.go") {
		t.Errorf("repo-search should have matched hello.go:\n%s", prompt2)
	}
	if strings.Contains(prompt2, "other.go") {
		t.Errorf("repo-search should NOT have matched other.go:\n%s", prompt2)
	}
}

func TestToolOutput_RepoSearch_NoMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644) //nolint:errcheck

	cs := newCaptureServer(t, []string{
		`TOOL_CALL {"tool":"repo-search","args":{"query":"xyzzy_not_here"}}`,
		"FINAL\nNothing found.",
	})
	_, err := capturedCoder(cs, dir).Execute(context.Background(), "Find xyzzy.", config.PermissionYOLO, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	prompt2 := cs.promptAt(1)
	if !strings.Contains(prompt2, "no matches") {
		t.Errorf("expected 'no matches' in tool output:\n%s", prompt2)
	}
}

// ── file-read output ───────────────────────────────────────────────────────────

func TestToolOutput_FileRead_ReturnsContent(t *testing.T) {
	dir := t.TempDir()
	content := "module myapp\ngo 1.22\n"
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644) //nolint:errcheck

	cs := newCaptureServer(t, []string{
		`TOOL_CALL {"tool":"file-read","args":{"path":"go.mod"}}`,
		"FINAL\nDone.",
	})
	_, err := capturedCoder(cs, dir).Execute(context.Background(), "Read go.mod.", config.PermissionYOLO, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	prompt2 := cs.promptAt(1)
	if !strings.Contains(prompt2, "module myapp") {
		t.Errorf("file-read output missing file content in next prompt:\n%s", prompt2)
	}
}

func TestToolOutput_FileRead_LineRange(t *testing.T) {
	dir := t.TempDir()
	lines := "line1\nline2\nline3\nline4\nline5\n"
	os.WriteFile(filepath.Join(dir, "lines.txt"), []byte(lines), 0o644) //nolint:errcheck

	cs := newCaptureServer(t, []string{
		`TOOL_CALL {"tool":"file-read","args":{"path":"lines.txt","start_line":2,"end_line":3}}`,
		"FINAL\nDone.",
	})
	_, err := capturedCoder(cs, dir).Execute(context.Background(), "Read lines 2-3.", config.PermissionYOLO, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	prompt2 := cs.promptAt(1)
	if !strings.Contains(prompt2, "line2") || !strings.Contains(prompt2, "line3") {
		t.Errorf("expected lines 2-3 in output:\n%s", prompt2)
	}
	if strings.Contains(prompt2, "line4") {
		t.Errorf("line4 should not appear in sliced output:\n%s", prompt2)
	}
}

func TestToolOutput_FileRead_MissingFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()

	cs := newCaptureServer(t, []string{
		`TOOL_CALL {"tool":"file-read","args":{"path":"nonexistent.txt"}}`,
		"FINAL\nFile not found.",
	})
	_, err := capturedCoder(cs, dir).Execute(context.Background(), "Read missing file.", config.PermissionYOLO, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	prompt2 := cs.promptAt(1)
	if !strings.Contains(strings.ToLower(prompt2), "error") && !strings.Contains(strings.ToLower(prompt2), "no such file") {
		t.Errorf("expected error message in prompt after missing file read:\n%s", prompt2)
	}
}

// ── file-write output ──────────────────────────────────────────────────────────

func TestToolOutput_FileWrite_ConfirmsCreated(t *testing.T) {
	dir := t.TempDir()

	cs := newCaptureServer(t, []string{
		`TOOL_CALL {"tool":"file-write","args":{"path":"new.txt","content":"hello\n"}}`,
		"FINAL\nDone.",
	})
	_, err := capturedCoder(cs, dir).Execute(context.Background(), "Write new.txt.", config.PermissionYOLO, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	prompt2 := cs.promptAt(1)
	if !strings.Contains(prompt2, "created") {
		t.Errorf("expected 'created' confirmation in tool output:\n%s", prompt2)
	}
}

func TestToolOutput_FileWrite_ConfirmsUpdated(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("original\n"), 0o644) //nolint:errcheck

	cs := newCaptureServer(t, []string{
		`TOOL_CALL {"tool":"file-read","args":{"path":"existing.txt"}}`,
		`TOOL_CALL {"tool":"file-write","args":{"path":"existing.txt","content":"updated\n"}}`,
		"FINAL\nDone.",
	})
	_, err := capturedCoder(cs, dir).Execute(context.Background(), "Update existing.txt.", config.PermissionYOLO, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The third prompt should contain "updated" confirmation from file-write.
	prompt3 := cs.promptAt(2)
	if !strings.Contains(prompt3, "updated") {
		t.Errorf("expected 'updated' confirmation in tool output:\n%s", prompt3)
	}
}

func TestToolOutput_FileWrite_RefusalIncludedInPrompt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("original\n"), 0o644) //nolint:errcheck

	cs := newCaptureServer(t, []string{
		`TOOL_CALL {"tool":"file-write","args":{"path":"existing.txt","content":"overwrite\n"}}`,
		"FINAL\nFailed.",
	})
	_, err := capturedCoder(cs, dir).Execute(context.Background(), "Overwrite without reading.", config.PermissionYOLO, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	prompt2 := cs.promptAt(1)
	if !strings.Contains(strings.ToLower(prompt2), "error") && !strings.Contains(strings.ToLower(prompt2), "refusing") && !strings.Contains(strings.ToLower(prompt2), "read") {
		t.Errorf("expected refusal error message in prompt:\n%s", prompt2)
	}
}

// ── glob and grep tool output ─────────────────────────────────────────────────
// These tests use LLMExplorer (read-only) and verify tool trace output directly.

func TestToolOutput_Glob_MatchesPattern(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)           //nolint:errcheck
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)                                          //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "sub", "util.go"), []byte("package sub\n"), 0o644)     //nolint:errcheck

	pm, provName, modelName := scriptedManager(t, []string{
		`TOOL_CALL {"tool":"glob","args":{"pattern":"**/*.go"}}`,
		"FINAL\nDone.",
	})
	exp := agent.LLMExplorer{
		ProviderManager: pm,
		ProviderName:    func() string { return provName },
		ModelName:       func() string { return modelName },
		CWD:             dir,
	}
	result, err := exp.Explore(context.Background(), "List Go files.")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	var tr *agent.ToolTrace
	for i := range result.Tools {
		if result.Tools[i].Name == "glob" {
			tr = &result.Tools[i]
			break
		}
	}
	if tr == nil {
		t.Fatal("no glob trace")
	}
	if tr.Status != "success" {
		t.Errorf("expected success, got %q", tr.Status)
	}
	if !strings.Contains(tr.Output, "main.go") {
		t.Errorf("expected main.go in glob output: %q", tr.Output)
	}
	if !strings.Contains(tr.Output, "util.go") {
		t.Errorf("expected util.go in glob output: %q", tr.Output)
	}
}

func TestToolOutput_Grep_FindsMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\n\nfunc HelloWorld() {}\n"), 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "bar.go"), []byte("package bar\n\nfunc Other() {}\n"), 0o644)     //nolint:errcheck

	pm, provName, modelName := scriptedManager(t, []string{
		`TOOL_CALL {"tool":"grep","args":{"pattern":"func HelloWorld","type":"go"}}`,
		"FINAL\nFound it.",
	})
	exp := agent.LLMExplorer{
		ProviderManager: pm,
		ProviderName:    func() string { return provName },
		ModelName:       func() string { return modelName },
		CWD:             dir,
	}
	result, err := exp.Explore(context.Background(), "Find HelloWorld.")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	var tr *agent.ToolTrace
	for i := range result.Tools {
		if result.Tools[i].Name == "grep" {
			tr = &result.Tools[i]
			break
		}
	}
	if tr == nil {
		t.Fatal("no grep trace")
	}
	if tr.Status != "success" {
		t.Errorf("expected success, got %q (output: %q)", tr.Status, tr.Output)
	}
	if !strings.Contains(tr.Output, "HelloWorld") {
		t.Errorf("expected HelloWorld in grep output: %q", tr.Output)
	}
	if strings.Contains(tr.Output, "Other") {
		t.Errorf("Other() should not appear in grep output: %q", tr.Output)
	}
}

func TestToolOutput_Grep_FilesWithMatchesMode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc A() {}\n"), 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\nfunc B() {}\n"), 0o644) //nolint:errcheck

	pm, provName, modelName := scriptedManager(t, []string{
		`TOOL_CALL {"tool":"grep","args":{"pattern":"func ","type":"go","output_mode":"files_with_matches"}}`,
		"FINAL\nDone.",
	})
	exp := agent.LLMExplorer{
		ProviderManager: pm,
		ProviderName:    func() string { return provName },
		ModelName:       func() string { return modelName },
		CWD:             dir,
	}
	result, err := exp.Explore(context.Background(), "Find files with functions.")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	var tr *agent.ToolTrace
	for i := range result.Tools {
		if result.Tools[i].Name == "grep" {
			tr = &result.Tools[i]
			break
		}
	}
	if tr == nil || tr.Status != "success" {
		t.Fatalf("expected success grep trace, got: %+v", tr)
	}
	// files_with_matches should list .go paths
	if !strings.Contains(tr.Output, ".go") {
		t.Errorf("expected .go paths in output: %q", tr.Output)
	}
}

func TestToolOutput_Grep_NoMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x\n"), 0o644) //nolint:errcheck

	pm, provName, modelName := scriptedManager(t, []string{
		`TOOL_CALL {"tool":"grep","args":{"pattern":"XYZZY_NOT_HERE","type":"go"}}`,
		"FINAL\nNothing.",
	})
	exp := agent.LLMExplorer{
		ProviderManager: pm,
		ProviderName:    func() string { return provName },
		ModelName:       func() string { return modelName },
		CWD:             dir,
	}
	result, err := exp.Explore(context.Background(), "Find XYZZY.")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	var tr *agent.ToolTrace
	for i := range result.Tools {
		if result.Tools[i].Name == "grep" {
			tr = &result.Tools[i]
			break
		}
	}
	if tr == nil || tr.Status != "success" {
		t.Fatalf("expected success grep trace: %+v", tr)
	}
	if !strings.Contains(tr.Output, "no matches") {
		t.Errorf("expected 'no matches' in output: %q", tr.Output)
	}
}
