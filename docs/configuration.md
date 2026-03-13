# Configuration and Storage

Spettro uses both project-local and user-global storage.

## Global (`~/.spettro/`)

| Path | Purpose |
| --- | --- |
| `config.json` | Active provider/model, permission, token budget, favorites. |
| `keys.enc` | Encrypted API keys map by provider ID. |
| `trusted.json` | Permanently trusted project paths. |
| `models.json` | Cached `models.dev` catalog. |
| `conversations/<project-hash>/` | Saved conversations for each project. |

## Project-local (`<repo>/.spettro/`)

| Path | Purpose |
| --- | --- |
| `PLAN.md` | Last generated implementation plan. |
| `AGENT.md` | Coding outputs appended over time. |
| `index.json` | Optional project snapshot (when indexer path is used). |

## Security model

- API keys are not stored in plaintext in `config.json`.
- Keys are encrypted with AES-GCM and a derived machine/user secret.
- You can override key derivation input via `SPETTRO_MASTER_KEY`.
- First run in a folder prompts for trust before enabling normal workflow.

## Permission levels

| Level | Behavior |
| --- | --- |
| `ask-first` | Requires explicit approval flow for coding execution. |
| `restricted` | Allows coding execution with policy restrictions. |
| `yolo` | Least restrictive execution policy. |

## Agent manifest

Spettro loads `spettro.agents.toml` from the project root if present; otherwise it falls back to built-in defaults.

See [`AGENTS.md`](../AGENTS.md) for schema and validation rules.

`config.json` also stores local model endpoints configured via `/connect` (for example `http://localhost:1234`), while API keys remain encrypted in `keys.enc`.
