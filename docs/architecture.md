# Architecture Overview

Spettro is a Go application with a Bubble Tea TUI front-end and internal service packages.

## Entry point and runtime

- `cmd/spettro/main.go` boots config, storage, provider manager, model catalog, and TUI model.
- `internal/tui/model.go` is the active application flow and command dispatcher.

## Core packages

- `internal/tui`: interactive terminal UI, dialogs, rendering, and command handling.
- `internal/agent`: planning/coding loops, tool protocol, commit and search agents.
- `internal/provider`: provider adapters and model catalog mapping.
- `internal/models`: fetch/cache of `models.dev` catalog.
- `internal/config`: config persistence, encrypted key management, trust list, agent manifest parsing.
- `internal/storage`: `.spettro` directory handling.
- `internal/conversation`: save/list/load project conversations.
- `internal/indexer`: file snapshot builder (skip `.git` and `.spettro`).
- `internal/budget`: token estimation and guardrails.

## Execution flow

1. User prompt enters current mode (`planning`, `coding`, or `chat`).
2. Planning/coding uses a tool loop protocol:
   - LLM emits `TOOL_CALL` JSON.
   - Runtime executes allowed tools (`repo-search`, `file-read`, `file-write`, `shell-exec`).
   - LLM finalizes via `FINAL`.
3. Chat mode sends plain prompt/images to provider adapter.
4. Outputs and metadata are appended to the message timeline and persisted as needed.

## Provider abstraction

- Anthropic uses native API adapter.
- Other providers use OpenAI-compatible adapter and known base URL mapping.
- Model list is catalog-driven, with fallback models if catalog is unavailable.

