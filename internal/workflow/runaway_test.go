package workflow

import (
	"context"
	"strings"
	"testing"
	"time"
)

func runWithDeadline(t *testing.T, body string, d time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, header(body), Options{Runner: echoRunner(0)})
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(d + 5*time.Second):
		t.Fatalf("the engine hung: cancellation never took effect for %q", body)
		return nil
	}
}

// A workflow script is written by a model and runs inside the user's session.
// It must never be able to wedge that session: goja executes JS on the calling
// goroutine and checks nothing, so a loop that never yields ignores Esc,
// ignores the tool timeout, and burns a core until the process dies. Interrupt
// is the only thing that reaches a running script from outside it.
func TestRunawayScriptsAreStopped(t *testing.T) {
	cases := map[string]string{
		"bare loop":            "while (true) { }",
		"loop after an await":  "await agent('x'); while (true) { }",
		"caught loop":          "try { while (true) { } } catch (e) { log('swallowed') } ; while (true) { }",
		"loop building memory": "let s = 'x'; const a = []; while (true) { a.push(s) }",
		"loop in a thunk":      "await parallel([() => { while (true) {} }])",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			err := runWithDeadline(t, body, 1200*time.Millisecond)
			if err == nil {
				t.Fatal("a runaway script must not report success")
			}
			if !strings.Contains(err.Error(), "stopped") && !strings.Contains(err.Error(), "context") {
				t.Fatalf("unexpected error: %v", err)
			}
			t.Logf("%s -> %v", name, err)
		})
	}
}

func TestRunJobContainsPanics(t *testing.T) {
	if err := runJob(func() {}); err != nil {
		t.Fatalf("a clean job must not error: %v", err)
	}
	err := runJob(func() { panic("binding blew up") })
	if err == nil || !strings.Contains(err.Error(), "binding blew up") {
		t.Fatalf("a panicking job must become an error, got %v", err)
	}
	// The process must still be alive to run this line.
	if err := runJob(func() {}); err != nil {
		t.Fatalf("the engine did not survive: %v", err)
	}
}
