# Architecture Overview

Spettro is a Go application with a Bubble Tea TUI front-end and internal service packages.

## Entry point and runtime

- `cmd/spettro/main.go` boots config, storage, provider manager, model catalog, and TUI.
- `internal/tui` is the active application flow and command dispatcher (via `tui.New`).
- `internal/config` provides `LoadAgentManifestForProject`.

## Core packages

- `internal/tui`: interactive terminal UI, dialogs, rendering, and command handling.
- `internal/config`: config persistence, encrypted key management, trust list, agent manifest parsing.
- `internal/agent`: tool loop protocol and "agent" tool support for parallel spawning.
- `internal/provider`: provider adapters and model catalog mapping.
- `internal/models`: fetch/cache of `models.dev` catalog.
- `internal/storage`: `.spettro` directory handling.
- `internal/conversation`: save/list/load project conversations.
- `internal/budget`: token estimation and guardrails.

## Agent Manifest

Spettro loads `spettro.agents.toml` from the project root if present; otherwise falls back to built-ins. See [AGENTS.md](../AGENTS.md) for the full schema (version, default_agent, [runtime], [[tools]], [[agents]], permitted_actions, validation rules).

## Execution flow

1. User prompt enters current mode/active agent from manifest (default: planning).
2. Agents follow tool loop: emit `TOOL_CALL` (parallel capable via multiple lines), runtime executes allowed tools from manifest (glob/grep/file-read/file-write/shell-exec/agent/etc.), finalize via `FINAL`.
3. Planning agent requires minimum exploration (glob + read every referenced file).
4. Chat/coding/other agents follow their `agents/*.md` prompts.
5. Outputs and metadata appended to timeline.

## Provider abstraction

- Anthropic uses native API adapter.
- Other providers use OpenAI-compatible adapter and known base URL mapping.
- Model list is catalog-driven, with fallback models if catalog is unavailable.

## Risks
If dispatch or manifest loader changes later, revisit this file.