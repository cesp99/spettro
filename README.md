# Spettro

[![Go 1.24+](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go)](https://go.dev/)
[![UI Bubble Tea](https://img.shields.io/badge/UI-Bubble%20Tea-ff69b4)](https://github.com/charmbracelet/bubbletea)
[![Providers](https://img.shields.io/badge/LLM-OpenAI%20Compatible%20%7C%20Anthropic-6f42c1)](#provider-setup)
[![Status](https://img.shields.io/badge/status-experimental-orange)](#)
[![License](https://img.shields.io/badge/license-unlicensed-lightgrey)](#)

Spettro is a terminal-first multi-agent coding assistant written in Go.

It provides a Bubble Tea TUI with three modes (`planning`, `coding`, `chat`), provider/model switching, permission-gated execution, repository search, conversation resume, and encrypted API key storage.

## Highlights

- Multi-mode workflow: plan first, execute in coding mode, then discuss in chat mode.
- Live tool traces during planning/coding runs.
- Permission policies: `ask-first`, `restricted`, `yolo`.
- Multi-provider model support via `models.dev` catalog + OpenAI-compatible endpoints.
- Conversation persistence and resume per project.
- Project trust prompt before first use in a folder.

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
3. Start with `planning` mode and switch modes with `Shift+Tab`.

## Common commands

Spettro commands are entered in the input box with a leading `/`.

- `/help` show built-in help
- `/setup` interactive setup wizard
- `/connect` connect a provider/API key
- `/connect` also supports local endpoints (e.g. LM Studio `localhost:1234`)
- `/models` open model selector (supports `/models provider:model`)
- `/permission <ask-first|restricted|yolo>`
- `/approve` execute pending plan in coding mode
- `/search [query]` search repository files/content
- `/compact [focus]` summarize conversation history (optionally focused)
- `/resume` load a previous saved conversation
- `/commit` generate and create a git commit message

For the full command and keybinding reference, see [`docs/commands.md`](docs/commands.md).

## Project docs

- [Getting started and workflow](docs/getting-started.md)
- [Commands and keybindings](docs/commands.md)
- [Configuration and storage](docs/configuration.md)
- [Architecture overview](docs/architecture.md)
- [Troubleshooting](docs/troubleshooting.md)

## Development

```bash
make test
make build
make build-all
```
