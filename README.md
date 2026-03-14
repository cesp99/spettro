# Spettro

[![Go 1.24+](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go)](https://go.dev/)
[![UI Bubble Tea](https://img.shields.io/badge/UI-Bubble%20Tea-ff69b4)](https://github.com/charmbracelet/bubbletea)
[![Providers](https://img.shields.io/badge/LLM-OpenAI%20Compatible%20%7C%20Anthropic-6f42c1)](#provider-setup)
[![Status](https://img.shields.io/badge/status-experimental-orange)](#)
[![License](https://img.shields.io/badge/license-unlicensed-lightgrey)](#)

Spettro is a terminal-first multi-agent coding assistant written in Go.

It uses a configurable agent manifest (`spettro.agents.toml` + `agents/*.md` prompts), parallel sub-agent spawning via the "agent" tool with `TOOL_CALL` syntax, and specialized agents (planning, coding, docs-writer, research, tester, reviewer, etc.). The entry point `cmd/spettro/main.go` boots config, storage, provider manager, model catalog, and TUI; manifest loaded via `config.LoadAgentManifestForProject`.

## Highlights

- Configurable multi-agent system via `spettro.agents.toml` and `agents/*.md`
- Parallel `TOOL_CALL` spawning of sub-agents
- Exploration-first workflow using glob/grep/file-read before edits
- Permission policies: `ask-first`, `restricted`, `yolo`
- Live tool traces during planning/coding runs
- Multi-provider model support via `models.dev` catalog + OpenAI-compatible endpoints
- Conversation persistence and resume per project
- Project trust prompt before first use in a folder

## Build and run

Requirements:

- Go `1.24.2+`

```bash
git clone https://github.com/cesp99/spettro
cd spettro
make build
./bin/spettro
```

Alternative:

```bash
go run ./cmd/spettro
```

## First-time setup

At first launch:

1. Confirm folder trust.
2. Run `/setup` (or `/connect` then `/models`) to configure provider/model and API key.
3. Start with `planning` (default_agent) and switch with `Shift+Tab`.

## Common commands

Spettro commands are entered in the input box with a leading `/`.

- `/help` show built-in help
- `/setup` interactive setup wizard
- `/connect` connect a provider/API key
- `/connect` also supports local endpoints (e.g. LM Studio `localhost:1234`)
- `/models` open model selector (supports `/models provider:model`)
- `/permission <ask-first|restricted|yolo>`
- `/approve` execute pending plan in coding mode (routes via manifest handoff)
- `/search [query]` search repository files/content
- `/compact [focus]` summarize conversation history (optionally focused)
- `/resume` load a previous saved conversation
- `/commit` generate and create a git commit message

For the full command and keybinding reference, see [`docs/commands.md`](docs/commands.md).

## Project docs

- [Agent Manifest](AGENTS.md)
- [Agent Prompts](agents/README.md)
- [Getting started and workflow](docs/getting-started.md)
- [Commands and keybindings](docs/commands.md)
- [Configuration and storage](docs/configuration.md)
- [Architecture overview](docs/architecture.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Documentation Index](docs/README.md)

## Development

```bash
make test
make build
make build-all
```