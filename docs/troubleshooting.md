# Troubleshooting

## “Setup required” or provider errors

- Run `/setup` again.
- Verify API key correctness.
- Confirm selected model exists for that provider (`/models`).

## “No providers connected”

- Use `/connect` and provide an API key.
- For LM Studio/Ollama, choose `Local endpoint (LM Studio/Ollama)` in `/connect`, then enter the endpoint (for example `localhost:1234`).
- Then open `/models` and choose a model.

## Token budget exceeded

- Increase budget with `/budget <n>`.
- Use `/compact` to reduce conversation context size (`/compact <focus...>` if you need a focused summary).
- Split large prompts into smaller tasks (per-agent context in manifest).

## `/approve` does nothing useful

- Ensure you generated a plan first in planning mode.
- Switch to coding mode (or let manifest handoff), then run `/approve`.
- Check permission mode with `/permission`.

## Unknown agent / TOOL_CALL

- Verify `id` exists in `spettro.agents.toml` or built-ins.
- Check `handoffs` and `allowed_tools` in manifest.
- Re-run `/models` or restart to reload manifest.

## Manifest validation error

- Fix unknown TOML fields, duplicate IDs, missing `default_agent`, or invalid permissions.
- See `AGENTS.md` for rules; remove `spettro.agents.toml` to use built-ins.

## docs-writer recursion

- `agents/docs-writer.md` contains self-recursion guard that blocks edits to own file or planning docs; avoid updating without explicit request.

## Search feels slow

`/search` scans repository files and content directly; very large repos can be slower. Prefer narrower queries.

## Commit command fails

- Ensure there are local changes (`git status` not empty).
- Resolve git issues (conflicts, hooks, auth) and retry `/commit`.

## Reset local state

If needed, remove local Spettro state:

```bash
rm -rf .spettro
rm -rf ~/.spettro/config.json ~/.spettro/keys.enc ~/.spettro/trusted.json
```

Only do this if you are comfortable losing cached models, encrypted keys, and saved conversations.