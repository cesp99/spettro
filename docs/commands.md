# Commands and Keybindings

## Slash commands

| Command | Description |
| --- | --- |
| `/help` | Show help text inside the app. |
| `/exit`, `/quit` | Quit Spettro. |
| `/mode`, `/next` | Cycle mode/active agent (`planning → coding → chat` and others per manifest). |
| `/setup` | Start setup wizard. |
| `/connect` | Open provider connection dialog. |
| `/models` | Open model selector dialog. |
| `/models <provider:model>` | Set model directly. |
| `/permission <ask-first\|restricted\|yolo>` | Set execution policy. |
| `/budget <n>` | Set request token budget (`min 1000`). |
| `/approve` | Execute pending plan (routes via manifest handoff to coding agent). |
| `/image <path>` | Queue image for next chat request. |
| `/commit` | Generate commit message and commit tracked changes (adds Co-Authored-By trailer). |
| `/search [query]` | Search repository files and contents. |
| `/compact [focus...]` | Summarize current conversation (optional focus instruction). |
| `/clear` | Save and clear current conversation. |
| `/resume` | Open saved conversation picker. |
| `/init` | Analyze codebase and write SPETTRO.md. |

## Agent system

- Type `@` in the input box to open repository file suggestions and insert mentions.
- Spawn sub-agents with parallel `TOOL_CALL` (example from `agents/planning.md`): `TOOL_CALL {"tool":"agent","args":{"id":"tester","task":"run focused tests"}}`
- Multiple `TOOL_CALL` lines in one response run in parallel.
- `/init` and `/clear` are available in command palette.
- `/approve` now routes through coding agent per manifest handoffs.

## Keyboard shortcuts

| Key | Action |
| --- | --- |
| `Shift+Tab` | Cycle mode/active agent. |
| `F2` | Next favorite model. |
| `Shift+F2` | Previous favorite model. |
| `Ctrl+O` | Toggle tool/thinking details visibility. |
| `Ctrl+C` twice | Quit (with safety prompt). |
| `Ctrl+Q` | Quit immediately. |
| `Up` / `Down` | Navigate command palette and dialogs. |
| `Tab` | Move selection in palettes/dialogs. |

## Notes

- `/approve` requires a pending plan and coding mode.
- In `ask-first`, coding prompts without approval return guidance instead of executing.
- When coding asks to run a non-default shell command outside `yolo`, approve with:
  `1) yes`, `2) yes and don't ask again`, `3) no`, `4) tell the agent what to do instead`.
- Approval choice `2` persists command approval in `.spettro/allowed_commands.json` for the current project.
- `/connect` includes a `Local endpoint (LM Studio/Ollama)` option: enter `localhost:1234` (or another host) and Spettro probes `/v1/models` automatically.
- `F2`/`Shift+F2` cycle only through favorited models (toggle favorite with `f` in `/models`).
- Pressing `Enter` on a highlighted suggestion inserts it into the input first; press `Enter` again to run/send.
- `/commit` adds this trailer automatically:
  `Co-Authored-By: Spettro <spettro@eyed.to>`.