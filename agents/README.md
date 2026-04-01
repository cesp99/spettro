# Spettro Agent Prompt Pack

This folder contains prompt files referenced by `spettro.agents.toml`.

## Included prompt files

- `planning.md`
- `coding.md`
- `chat.md`
- `explore.md`
- `git.md`
- `reviewer.md`
- `tester.md`
- `docs-writer.md`

## Default manifest agent IDs

- `plan` -> `planning.md`
- `coding` -> `coding.md`
- `ask` -> `chat.md`
- `explore` -> `explore.md`
- `code` -> `coding.md`
- `git` -> `git.md`
- `test` -> `tester.md`
- `review` -> `reviewer.md`
- `docs` -> `docs-writer.md`

## Usage

- Set `prompt_file = "agents/<name>.md"` in each `[[agents]]` block.
- Keep `system_prompt` empty when using `prompt_file` as source of truth.
- Prompts define mission/scope, tool contracts, execution protocol, output contract, and escalation/safety rules.
