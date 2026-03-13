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
- Split large prompts into smaller tasks.

## `/approve` does nothing useful

- Ensure you generated a plan first in `planning` mode.
- Switch to `coding` mode, then run `/approve`.
- Check permission mode with `/permission`.

## Search feels slow

`/search` scans repository files and content directly; very large repos can be slower. Prefer narrower queries.

## Commit command fails

- Ensure there are local changes (`git status` not empty).
- Resolve git issues (conflicts, hooks, auth) and retry `/commit`.

## Reset local state

If needed, remove local Spettro state:

```bash
rm -rf .spettro
rm -rf ~/.spettro
```

Only do this if you are comfortable losing cached models, encrypted keys, and saved conversations.
