# Getting Started

This guide focuses on daily usage flow. For command details, see [`commands.md`](commands.md).

## 1) Launch Spettro

```bash
./bin/spettro
```

On first launch in a folder, Spettro asks whether to trust the directory for this session or permanently.

## 2) Configure model access

Use either:

- `/setup` for guided setup (provider → model → API key → permission), or
- `/connect` then `/models` for manual selection.

Spettro supports:

- `anthropic` via native Anthropic API.
- OpenAI-compatible providers through provider-specific base URLs.
- Local OpenAI-compatible endpoints (for example LM Studio at `localhost:1234`) via `/connect`.

Model metadata is loaded from `https://models.dev/api.json` and cached locally.

## 3) Work with the agent system

Spettro starts with `default_agent` from manifest (planning).

- Use `glob`/`grep`/`file-read` for exploration (minimum required before edits).
- Spawn sub-agents in parallel via `TOOL_CALL {"tool":"agent","args":{"id":"...","task":"..."}}`.
- Switch mode/agent with `Shift+Tab` (or `/mode`).

## 4) Approval flow (important)

If permission is `ask-first`:

1. Prompt in planning.
2. Manifest handoff to coding agent.
3. Run `/approve` to execute the pending plan.

## 5) Persist and resume conversations

- `/compact` summarizes long threads to save context (or `/compact <focus...>` for focused summaries).
- `/clear` saves current conversation and clears active messages.
- `/resume` loads a prior conversation from the project’s conversation store.

## 6) Customize

Edit `spettro.agents.toml`, `agents/*.md` (e.g. `agents/docs-writer.md`) and restart.