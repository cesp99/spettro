package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestJournalResumeReplaysUnchangedCalls(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "run-1")
	script := header(`
		const a = await agent('alpha')
		const b = await agent('beta')
		return a + '|' + b
	`)

	j1, err := OpenJournal(first)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	r1 := echoRunner(0)
	if _, err := Run(context.Background(), script, Options{Runner: r1, Journal: j1}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := j1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if r1.count() != 2 {
		t.Fatalf("first run made %d calls, want 2", r1.count())
	}

	// Resume with one call edited: the unchanged one replays, the new one runs.
	second := filepath.Join(dir, "run-2")
	j2, err := OpenJournal(second)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	if err := j2.LoadCache(first); err != nil {
		t.Fatalf("load cache: %v", err)
	}
	r2 := echoRunner(0)
	res, err := Run(context.Background(), header(`
		const a = await agent('alpha')
		const b = await agent('beta-edited')
		return a + '|' + b
	`), Options{Runner: r2, Journal: j2})
	if err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if r2.count() != 1 || r2.calls[0].Prompt != "beta-edited" {
		t.Fatalf("resumed run executed %d calls (%v), want only the edited one", r2.count(), r2.calls)
	}
	if res.Cached != 1 {
		t.Fatalf("cached = %d, want 1", res.Cached)
	}
	if res.Value != "done:alpha|done:beta-edited" {
		t.Fatalf("value = %#v", res.Value)
	}
}

func TestJournalDoesNotCacheFailures(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "run-1")
	var attempt atomic.Int32
	runner := &fakeRunner{fn: func(req Request) (Response, error) {
		if attempt.Add(1) == 1 {
			return Response{}, fmt.Errorf("transient")
		}
		return Response{Text: "recovered"}, nil
	}}
	j1, _ := OpenJournal(first)
	if _, err := Run(context.Background(), header(`return await agent('flaky')`), Options{Runner: runner, Journal: j1}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	_ = j1.Close()

	j2, _ := OpenJournal(filepath.Join(dir, "run-2"))
	if err := j2.LoadCache(first); err != nil {
		t.Fatalf("load cache: %v", err)
	}
	res, err := Run(context.Background(), header(`return await agent('flaky')`), Options{Runner: runner, Journal: j2})
	if err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if res.Value != "recovered" {
		t.Fatalf("a failed call must be retried on resume, got %#v", res.Value)
	}
}

func TestJournalReplaysIdenticalPromptsIndependently(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "run-1")
	script := header(`return (await parallel([() => agent('same'), () => agent('same')])).length`)

	j1, _ := OpenJournal(first)
	r1 := echoRunner(0)
	if _, err := Run(context.Background(), script, Options{Runner: r1, Journal: j1}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	_ = j1.Close()

	j2, _ := OpenJournal(filepath.Join(dir, "run-2"))
	_ = j2.LoadCache(first)
	r2 := echoRunner(0)
	res, err := Run(context.Background(), script, Options{Runner: r2, Journal: j2})
	if err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if r2.count() != 0 {
		t.Fatalf("both identical calls should replay, %d re-ran", r2.count())
	}
	if res.Cached != 2 {
		t.Fatalf("cached = %d, want 2", res.Cached)
	}
}

func TestJournalMissingPriorRunIsNotAnError(t *testing.T) {
	j, _ := OpenJournal(filepath.Join(t.TempDir(), "run"))
	if err := j.LoadCache(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatalf("a missing prior run must be tolerated: %v", err)
	}
}

func TestJournalWritesArtifacts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	j, _ := OpenJournal(dir)
	if err := j.WriteFile("script.js", "return 1"); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "script.js"))
	if err != nil || string(data) != "return 1" {
		t.Fatalf("artifact = %q, err = %v", data, err)
	}
	if err := j.Append(JournalEntry{Key: "k", Output: "o"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = j.Close()
	raw, err := os.ReadFile(filepath.Join(dir, "journal.jsonl"))
	if err != nil || !strings.Contains(string(raw), `"key":"k"`) {
		t.Fatalf("journal = %q, err = %v", raw, err)
	}
}

func TestNilJournalIsUsable(t *testing.T) {
	var j *Journal
	if _, ok := j.Take("k"); ok {
		t.Fatal("nil journal must never hit")
	}
	if err := j.Append(JournalEntry{}); err != nil {
		t.Fatalf("append on nil journal: %v", err)
	}
	if j.Hits() != 0 || j.Dir() != "" || j.Close() != nil {
		t.Fatal("nil journal accessors must be inert")
	}
}
