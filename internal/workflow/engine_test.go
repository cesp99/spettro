package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRunner answers prompts from a function, tracking peak concurrency so
// tests can assert that parallel() really overlaps and pipeline() really has
// no barrier between stages.
type fakeRunner struct {
	fn func(Request) (Response, error)

	mu      sync.Mutex
	live    int
	peak    int
	calls   []Request
	started []time.Time
}

func (f *fakeRunner) RunAgent(ctx context.Context, req Request) (Response, error) {
	f.mu.Lock()
	f.live++
	if f.live > f.peak {
		f.peak = f.live
	}
	f.calls = append(f.calls, req)
	f.started = append(f.started, time.Now())
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.live--
		f.mu.Unlock()
	}()
	return f.fn(req)
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func echoRunner(delay time.Duration) *fakeRunner {
	return &fakeRunner{fn: func(req Request) (Response, error) {
		if delay > 0 {
			time.Sleep(delay)
		}
		return Response{Text: "done:" + req.Prompt, Tokens: 10}, nil
	}}
}

func header(body string) string {
	return "export const meta = {\n  name: 'test-flow',\n  description: 'a test workflow',\n  phases: [{title: 'Work'}],\n}\n" + body
}

func run(t *testing.T, body string, opts Options) Result {
	t.Helper()
	if opts.Runner == nil {
		opts.Runner = echoRunner(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := Run(ctx, header(body), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func TestRunSequentialAwait(t *testing.T) {
	res := run(t, `
		phase('Work')
		const a = await agent('one')
		log('got ' + a)
		return a + '|' + (await agent('two'))
	`, Options{})
	if got, want := res.Value, "done:one|done:two"; got != want {
		t.Fatalf("value = %v, want %v", got, want)
	}
	if res.Agents != 2 {
		t.Fatalf("agents = %d, want 2", res.Agents)
	}
	if len(res.Logs) != 1 || res.Logs[0] != "got done:one" {
		t.Fatalf("logs = %v", res.Logs)
	}
	if len(res.Phases) != 1 || res.Phases[0] != "Work" {
		t.Fatalf("phases = %v", res.Phases)
	}
	if res.Tokens != 20 {
		t.Fatalf("tokens = %d, want 20", res.Tokens)
	}
}

func TestParallelRunsConcurrently(t *testing.T) {
	runner := echoRunner(120 * time.Millisecond)
	start := time.Now()
	res := run(t, `
		const out = await parallel([1,2,3,4].map(n => () => agent('task ' + n)))
		return out.length
	`, Options{Runner: runner, MaxConcurrency: 4})
	elapsed := time.Since(start)
	if res.Value != int64(4) {
		t.Fatalf("value = %#v, want 4", res.Value)
	}
	if runner.peak < 4 {
		t.Fatalf("peak concurrency = %d, want 4 — parallel() serialised", runner.peak)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("parallel took %v; four 120ms agents should overlap", elapsed)
	}
}

func TestMaxConcurrencyCapsInFlightAgents(t *testing.T) {
	runner := echoRunner(50 * time.Millisecond)
	run(t, `
		await parallel([1,2,3,4,5,6,7,8].map(n => () => agent('task ' + n)))
	`, Options{Runner: runner, MaxConcurrency: 2})
	if runner.peak > 2 {
		t.Fatalf("peak concurrency = %d, want at most 2", runner.peak)
	}
	if runner.count() != 8 {
		t.Fatalf("calls = %d, want 8", runner.count())
	}
}

// TestPipelineHasNoBarrier is the property that makes pipeline() worth having:
// a fast item must reach stage 2 while a slow item is still in stage 1.
func TestPipelineHasNoBarrier(t *testing.T) {
	var slowStage1Done, fastStage2Start time.Time
	var mu sync.Mutex
	runner := &fakeRunner{fn: func(req Request) (Response, error) {
		switch {
		case strings.HasPrefix(req.Prompt, "s1:slow"):
			time.Sleep(300 * time.Millisecond)
			mu.Lock()
			slowStage1Done = time.Now()
			mu.Unlock()
		case strings.HasPrefix(req.Prompt, "s2:fast"):
			mu.Lock()
			fastStage2Start = time.Now()
			mu.Unlock()
		}
		return Response{Text: "ok"}, nil
	}}
	run(t, `
		await pipeline(['fast', 'slow'],
			(item) => agent('s1:' + item),
			(prev, item) => agent('s2:' + item))
	`, Options{Runner: runner, MaxConcurrency: 4})
	mu.Lock()
	defer mu.Unlock()
	if fastStage2Start.IsZero() || slowStage1Done.IsZero() {
		t.Fatal("pipeline did not run both stages")
	}
	if !fastStage2Start.Before(slowStage1Done) {
		t.Fatalf("stage 2 of the fast item waited for stage 1 of the slow item — pipeline() has a barrier")
	}
}

func TestPipelineStagesSeeItemAndIndex(t *testing.T) {
	res := run(t, `
		return await pipeline(['a','b'],
			(prev) => 'x' + prev,
			(prev, item, i) => prev + '/' + item + '/' + i)
	`, Options{})
	got := fmt.Sprint(res.Value)
	if got != "[xa/a/0 xb/b/1]" {
		t.Fatalf("value = %v", got)
	}
}

func TestFailedAgentResolvesToNull(t *testing.T) {
	runner := &fakeRunner{fn: func(req Request) (Response, error) {
		if strings.Contains(req.Prompt, "boom") {
			return Response{}, fmt.Errorf("provider exploded")
		}
		return Response{Text: "fine"}, nil
	}}
	res := run(t, `
		const bad = await agent('boom')
		const good = await agent('ok')
		return [bad === null, good]
	`, Options{Runner: runner})
	list, ok := res.Value.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("value = %#v", res.Value)
	}
	if list[0] != true {
		t.Fatalf("a failed agent must resolve to null, got %#v", list[0])
	}
	if res.Failed != 1 {
		t.Fatalf("failed = %d, want 1", res.Failed)
	}
}

func TestParallelSwallowsThrowingThunks(t *testing.T) {
	res := run(t, `
		const out = await parallel([
			() => agent('fine'),
			() => { throw new Error('nope') },
			() => Promise.reject(new Error('also nope')),
		])
		return out.map(v => v === null ? 'null' : 'value')
	`, Options{})
	got := fmt.Sprint(res.Value)
	if got != "[value null null]" {
		t.Fatalf("value = %v, want [value null null]", got)
	}
}

func TestStructuredOutputParsesAndRetries(t *testing.T) {
	var attempts atomic.Int32
	runner := &fakeRunner{fn: func(req Request) (Response, error) {
		if attempts.Add(1) == 1 {
			return Response{Text: "Sure! Here you go."}, nil
		}
		return Response{Text: "```json\n{\"findings\": [\"a\", \"b\"]}\n```"}, nil
	}}
	res := run(t, `
		const out = await agent('review', {schema: {type:'object', properties:{findings:{type:'array'}}, required:['findings']}})
		return out.findings.length
	`, Options{Runner: runner})
	if res.Value != int64(2) {
		t.Fatalf("value = %#v, want 2", res.Value)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2 (one rejected, one accepted)", attempts.Load())
	}
	if !strings.Contains(runner.calls[0].Prompt, "STRUCTURED OUTPUT REQUIRED") {
		t.Fatalf("schema contract missing from prompt: %q", runner.calls[0].Prompt)
	}
	if !strings.Contains(runner.calls[1].Prompt, "previous answer was rejected") {
		t.Fatalf("retry did not carry the parse error: %q", runner.calls[1].Prompt)
	}
}

func TestStructuredOutputRequiredFieldMissing(t *testing.T) {
	runner := &fakeRunner{fn: func(req Request) (Response, error) {
		return Response{Text: `{"other": 1}`}, nil
	}}
	res := run(t, `
		const out = await agent('x', {schema: {type:'object', required:['findings']}})
		return out === null
	`, Options{Runner: runner, SchemaAttempts: 2})
	if res.Value != true {
		t.Fatalf("a never-parsing schema call must resolve to null, got %#v", res.Value)
	}
	if runner.count() != 2 {
		t.Fatalf("calls = %d, want 2 attempts", runner.count())
	}
}

func TestBudgetStopsTheLoop(t *testing.T) {
	res := run(t, `
		let n = 0
		while (budget.total && budget.remaining() > 15) {
			await agent('work ' + n)
			n++
		}
		return n
	`, Options{BudgetTokens: 100})
	// Each agent burns 10 tokens; the loop stops once fewer than 15 remain.
	if res.Value != int64(9) {
		t.Fatalf("iterations = %#v, want 9", res.Value)
	}
	if res.Tokens != 90 {
		t.Fatalf("tokens = %d, want 90", res.Tokens)
	}
}

func TestBudgetExhaustionThrows(t *testing.T) {
	_, err := Run(context.Background(), header(`
		for (let i = 0; i < 100; i++) { await agent('burn ' + i) }
	`), Options{Runner: echoRunner(0), BudgetTokens: 25})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("want a budget error, got %v", err)
	}
}

func TestMaxAgentsBackstop(t *testing.T) {
	_, err := Run(context.Background(), header(`
		for (let i = 0; i < 50; i++) { await agent('spin ' + i) }
	`), Options{Runner: echoRunner(0), MaxAgents: 5})
	if err == nil || !strings.Contains(err.Error(), "cap is 5") {
		t.Fatalf("want an agent cap error, got %v", err)
	}
}

func TestMaxItemsRejectsOversizedFanOut(t *testing.T) {
	_, err := Run(context.Background(), header(`
		await parallel(Array.from({length: 10}, (_, i) => () => agent('x' + i)))
	`), Options{Runner: echoRunner(0), MaxItems: 4})
	if err == nil || !strings.Contains(err.Error(), "the limit is 4") {
		t.Fatalf("want an item cap error, got %v", err)
	}
}

func TestArgsAreExposed(t *testing.T) {
	res := run(t, `return args.files.map(f => f.toUpperCase())`, Options{
		Args: map[string]any{"files": []any{"a.go", "b.go"}},
	})
	if fmt.Sprint(res.Value) != "[A.GO B.GO]" {
		t.Fatalf("value = %v", res.Value)
	}
}

func TestNonDeterministicGlobalsAreDisabled(t *testing.T) {
	for _, expr := range []string{"Date.now()", "Math.random()", "new Date()", "Date()"} {
		_, err := Run(context.Background(), header("return "+expr), Options{Runner: echoRunner(0)})
		if err == nil || !strings.Contains(err.Error(), "unavailable in workflow scripts") {
			t.Fatalf("%s should be disabled, got %v", expr, err)
		}
	}
	// Dates built from an explicit value still work — only the clock is gone.
	res := run(t, `return new Date(0).getUTCFullYear()`, Options{})
	if res.Value != int64(1970) {
		t.Fatalf("new Date(0) = %#v", res.Value)
	}
}

func TestAgentOptionsReachTheRunner(t *testing.T) {
	runner := echoRunner(0)
	run(t, `
		phase('Review')
		await agent('look', {label: 'peek', agentType: 'review', model: 'fast', effort: 'low'})
		await agent('edit', {phase: 'Fix', isolation: 'worktree'})
	`, Options{Runner: runner})
	first, second := runner.calls[0], runner.calls[1]
	if first.Label != "peek" || first.Phase != "Review" || first.AgentType != "review" ||
		first.Model != "fast" || first.Effort != "low" {
		t.Fatalf("first request = %+v", first)
	}
	if second.Phase != "Fix" || second.Isolation != "worktree" {
		t.Fatalf("second request = %+v", second)
	}
	if second.Label != "edit" {
		t.Fatalf("label should default to the prompt's first line, got %q", second.Label)
	}
	if first.Instance != "review#1" {
		t.Fatalf("instance = %q, want review#1", first.Instance)
	}
}

func TestRejectsBadIsolation(t *testing.T) {
	_, err := Run(context.Background(), header(`await agent('x', {isolation: 'chroot'})`), Options{Runner: echoRunner(0)})
	if err == nil || !strings.Contains(err.Error(), "unsupported isolation") {
		t.Fatalf("want isolation error, got %v", err)
	}
}

func TestScriptThrowSurfaces(t *testing.T) {
	_, err := Run(context.Background(), header(`throw new Error('kaboom')`), Options{Runner: echoRunner(0)})
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("want the script error, got %v", err)
	}
}

func TestNeverSettlingPromiseIsReported(t *testing.T) {
	_, err := Run(context.Background(), header(`await new Promise(() => {})`), Options{Runner: echoRunner(0)})
	if err == nil || !strings.Contains(err.Error(), "never settles") {
		t.Fatalf("want a deadlock error, got %v", err)
	}
}

func TestContextCancellationStopsTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeRunner{fn: func(req Request) (Response, error) {
		cancel()
		<-ctx.Done()
		return Response{}, ctx.Err()
	}}
	_, err := Run(ctx, header(`await agent('slow')`), Options{Runner: runner})
	if err == nil {
		t.Fatal("want a cancellation error")
	}
}

func TestObserverSeesTheWholeRun(t *testing.T) {
	var mu sync.Mutex
	var kinds []EventKind
	run(t, `
		phase('Work')
		log('starting')
		await agent('one')
	`, Options{Observer: func(ev Event) {
		mu.Lock()
		kinds = append(kinds, ev.Kind)
		mu.Unlock()
	}})
	mu.Lock()
	defer mu.Unlock()
	want := []EventKind{EventStart, EventPhase, EventLog, EventAgentStart, EventAgentDone, EventFinish}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
}

func TestNestedWorkflow(t *testing.T) {
	child := "export const meta = {name: 'child', description: 'child flow'}\nreturn 'child:' + (await agent(args.task))"
	res := run(t, `return await workflow('child', {task: 'sub'})`, Options{
		Resolve: func(name string) (string, error) {
			if name != "child" {
				return "", fmt.Errorf("unknown workflow %q", name)
			}
			return child, nil
		},
	})
	if res.Value != "child:done:sub" {
		t.Fatalf("value = %#v", res.Value)
	}
	if res.Agents != 1 {
		t.Fatalf("nested agents must count toward the parent run, got %d", res.Agents)
	}
}

func TestNestedWorkflowCannotNestFurther(t *testing.T) {
	grandchild := "export const meta = {name: 'gc', description: 'x'}\nreturn 1"
	child := "export const meta = {name: 'child', description: 'x'}\nreturn await workflow('gc')"
	_, err := Run(context.Background(), header(`return await workflow('child')`), Options{
		Runner: echoRunner(0),
		Resolve: func(name string) (string, error) {
			if name == "child" {
				return child, nil
			}
			return grandchild, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot nest further") {
		t.Fatalf("want a nesting error, got %v", err)
	}
}

func TestJSONIsAvailable(t *testing.T) {
	res := run(t, `return JSON.parse('{"a":1}').a + 1`, Options{})
	if res.Value != int64(2) {
		t.Fatalf("value = %#v", res.Value)
	}
}

func TestSchemaIsSentCompact(t *testing.T) {
	runner := echoRunner(0)
	run(t, `await agent('x', {schema: {type: 'object'}})`, Options{Runner: runner})
	var found bool
	for _, line := range strings.Split(runner.calls[0].Prompt, "\n") {
		if line == `{"type":"object"}` {
			found = true
		}
	}
	if !found {
		t.Fatalf("compact schema not found in prompt:\n%s", runner.calls[0].Prompt)
	}
}

func TestResultValueIsJSONEncodable(t *testing.T) {
	res := run(t, `return {ok: true, items: [1, 'two'], nested: {a: null}}`, Options{})
	encoded, err := json.Marshal(res.Value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"ok":true`) {
		t.Fatalf("encoded = %s", encoded)
	}
}

func TestDefaultAgentTypeNamesInstances(t *testing.T) {
	runner := echoRunner(0)
	run(t, `await agent('one'); await agent('two', {agentType: 'review'})`, Options{
		Runner: runner, DefaultAgentType: "general-purpose",
	})
	if runner.calls[0].Instance != "general-purpose#1" {
		t.Fatalf("instance = %q, want general-purpose#1", runner.calls[0].Instance)
	}
	if runner.calls[0].AgentType != "general-purpose" {
		t.Fatalf("agent type = %q, want the default filled in", runner.calls[0].AgentType)
	}
	// An explicit agentType still wins.
	if runner.calls[1].Instance != "review#2" {
		t.Fatalf("instance = %q, want review#2", runner.calls[1].Instance)
	}
}

// scopedRunner records the per-call bracket the engine gives it.
type scopedRunner struct {
	*fakeRunner
	mu     sync.Mutex
	begins []int
	ends   []int
	errs   []error
}

func (s *scopedRunner) BeginCall(_ context.Context, req Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.begins = append(s.begins, req.Index)
	return nil
}

func (s *scopedRunner) EndCall(_ context.Context, req Request, runErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ends = append(s.ends, req.Index)
	s.errs = append(s.errs, runErr)
}

// A schema call is retried until its answer parses. Anything the Runner sets
// up for the call — Spettro creates a git worktree — must be created once, not
// once per attempt: a worktree per attempt left two branches of overlapping
// edits for a single agent, and merging both back collided.
func TestCallScoperBracketsRetriesOnce(t *testing.T) {
	var attempts atomic.Int32
	runner := &scopedRunner{fakeRunner: &fakeRunner{fn: func(req Request) (Response, error) {
		if attempts.Add(1) < 3 {
			return Response{Text: "not json at all"}, nil
		}
		return Response{Text: `{"ok": true}`}, nil
	}}}
	res := run(t, `return await agent('x', {schema: {type:'object', required:['ok']}})`, Options{Runner: runner})
	if res.Value == nil {
		t.Fatal("the call never produced a value")
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	if len(runner.begins) != 1 || len(runner.ends) != 1 {
		t.Fatalf("begins = %v, ends = %v — want exactly one bracket for one agent() call",
			runner.begins, runner.ends)
	}
	if runner.errs[0] != nil {
		t.Fatalf("EndCall saw %v, want nil after a successful parse", runner.errs[0])
	}
}

func TestCallScoperReportsFailureAndBracketsEveryCall(t *testing.T) {
	runner := &scopedRunner{fakeRunner: &fakeRunner{fn: func(req Request) (Response, error) {
		if strings.Contains(req.Prompt, "boom") {
			return Response{}, fmt.Errorf("provider exploded")
		}
		return Response{Text: "fine"}, nil
	}}}
	run(t, `await parallel([() => agent('boom'), () => agent('ok'), () => agent('ok2')])`,
		Options{Runner: runner})

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.begins) != 3 || len(runner.ends) != 3 {
		t.Fatalf("begins = %v, ends = %v — every call needs a bracket", runner.begins, runner.ends)
	}
	// EndCall has to learn the call failed, or the Runner would merge a broken
	// workspace back instead of preserving it.
	failures := 0
	for _, err := range runner.errs {
		if err != nil {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("EndCall saw %d failures, want 1", failures)
	}
}

// A Runner that does not implement CallScoper must keep working untouched.
func TestRunnerWithoutCallScoper(t *testing.T) {
	res := run(t, `return await agent('plain')`, Options{Runner: echoRunner(0)})
	if res.Value != "done:plain" {
		t.Fatalf("value = %#v", res.Value)
	}
}

// BeginCall failing must fail the call rather than run it unscoped: a workspace
// that could not be created means the agent would edit the shared checkout.
func TestCallScoperBeginFailureAbortsTheCall(t *testing.T) {
	runner := &failingScoper{fakeRunner: echoRunner(0)}
	res := run(t, `return (await agent('x')) === null`, Options{Runner: runner})
	if res.Value != true {
		t.Fatalf("value = %#v, want the call to have failed to null", res.Value)
	}
	if runner.count() != 0 {
		t.Fatalf("the agent ran %d times despite setup failing", runner.count())
	}
}

type failingScoper struct{ *fakeRunner }

func (f *failingScoper) BeginCall(context.Context, Request) error {
	return fmt.Errorf("cannot create workspace")
}
func (f *failingScoper) EndCall(context.Context, Request, error) {}

func TestMissingArgsIsExplained(t *testing.T) {
	_, err := Run(context.Background(), header("log('n=' + args.packages.length)"),
		Options{Runner: echoRunner(0)})
	if err == nil || !strings.Contains(err.Error(), "passed no args") {
		t.Fatalf("want the missing-args hint, got %v", err)
	}
	// The hint must not fire when args were supplied and the script has some
	// other undefined-property bug.
	_, err = Run(context.Background(), header("log('n=' + args.a.b)"),
		Options{Runner: echoRunner(0), Args: map[string]any{"other": 1}})
	if err == nil || strings.Contains(err.Error(), "passed no args") {
		t.Fatalf("hint fired despite args being supplied: %v", err)
	}
	// Nor when the script never mentions args.
	_, err = Run(context.Background(), header("log(nope.length)"), Options{Runner: echoRunner(0)})
	if err == nil || strings.Contains(err.Error(), "passed no args") {
		t.Fatalf("hint fired on an unrelated error: %v", err)
	}
}
