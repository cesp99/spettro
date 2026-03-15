# Spettro Agent Prompt Pack

This folder contains operational prompt files referenced by `spettro.agents.toml`.

Included roles:

- `planning.md`
- `coding.md`
- `chat.md`
- `explore.md`
- `git.md`
- `reviewer.md`
- `tester.md`
- `docs-writer.md`

Normalized agent IDs used in these prompts:

- `plan` -> `planning.md`
- `coding` -> `coding.md`
- `ask` -> `chat.md`
- `explore` -> `explore.md`
- `git` -> `git.md`
- `test` -> `tester.md`
- `review` -> `reviewer.md`
- `docs` -> `docs-writer.md`

Usage:

- Set `prompt_file = "agents/<name>.md"` in an `[[agents]]` block.
- Keep `system_prompt` empty when using `prompt_file` for a single source of truth.
- Each prompt follows an operational contract:
  - mission and scope boundaries
  - tool contract (exact tool families and intended use)
  - execution protocol
  - output contract
  - hard safety rules and escalation guidance
