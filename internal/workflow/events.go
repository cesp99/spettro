// Package workflow runs deterministic multi-agent orchestration scripts.
//
// A workflow is a small JavaScript program that decides — in ordinary control
// flow, not by asking a model — which sub-agents run, in what order, and how
// their results combine. The script gets a handful of globals (agent, parallel,
// pipeline, phase, log, args, budget, workflow) and returns a value; everything
// else is plain JS.
//
// The engine is deliberately decoupled from Spettro's agent package: it talks
// to a Runner interface for sub-agent execution and pushes progress through an
// Observer, so it can be driven (and tested) without a provider.
package workflow

import "time"

// EventKind classifies a progress event emitted while a script runs.
type EventKind string

const (
	// EventStart fires once, before the first line of the script executes.
	EventStart EventKind = "start"
	// EventLog carries a log() call from the script.
	EventLog EventKind = "log"
	// EventPhase fires when phase() starts a new progress group.
	EventPhase EventKind = "phase"
	// EventAgentStart fires when an agent() call is dispatched.
	EventAgentStart EventKind = "agent_start"
	// EventAgentDone fires when an agent() call resolves.
	EventAgentDone EventKind = "agent_done"
	// EventAgentError fires when an agent() call fails or is rejected.
	EventAgentError EventKind = "agent_error"
	// EventFinish fires once the script settles, successfully or not.
	EventFinish EventKind = "finish"
)

// Event is one progress notification. Hosts render these (TUI workflow panel,
// ACP tool calls) and are free to ignore kinds they do not display.
type Event struct {
	Kind EventKind
	At   time.Time
	// Phase is the progress group the event belongs to ("" outside any phase).
	Phase string
	// Label is the display label of an agent call, defaulting to a trimmed
	// prompt when the script did not set one.
	Label string
	// Instance is the unique per-call agent identity ("review#3"), stable
	// across the start/done pair so hosts can update a row in place.
	Instance string
	// AgentType is the manifest agent the call ran as.
	AgentType string
	// Index is the 1-based global agent counter at dispatch time.
	Index int
	// Message carries log() text, the workflow name on start/finish, or the
	// error text on failure.
	Message string
	// Output is the agent's final text (truncated by the host, not here).
	Output string
	// Cached is true when a resumed run replayed this call from the journal
	// instead of executing it.
	Cached bool
	// Nested marks events produced by a workflow() sub-run.
	Nested bool
}

// Observer receives progress events. It is called from the engine's single
// script goroutine, so implementations must not block for long.
type Observer func(Event)
