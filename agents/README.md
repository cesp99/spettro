# Spettro Agent Prompt Pack

This folder contains operational prompt files referenced by `spettro.agents.toml`.

Included roles:

- `planning.md`
- `coding.md`
- `chat.md`
- `research.md`
- `git-expert.md`
- `reviewer.md`
- `debugger.md`
- `tester.md`
- `docs-writer.md`

Usage:

- Set `prompt_file = "agents/<name>.md"` in an `[[agents]]` block.
- Keep `system_prompt` empty when using `prompt_file` for a single source of truth.
- Each prompt follows an operational contract:
  - mission
  - workflow
  - output contract
  - safety and escalation rules
