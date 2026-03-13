# Spettro agent manifest

Spettro supports a project-level agent manifest at:

- `spettro.agents.toml`

If the file is missing, Spettro uses an internal default manifest.

## Goals

This file lets you define, in one place:

- which agents exist
- what each agent is good at
- which tools each agent can use
- what actions each tool and agent is allowed to perform
- handoff relationships between agents
- runtime safety defaults

## Schema

### Root fields

- `version` (int, required): schema version, currently `1`.
- `default_agent` (string, required): agent ID to start from.
- `[metadata]` (table, optional): human-facing metadata.
- `[runtime]` (table, required): global execution defaults.
- `[[tools]]` (array of tables, required): tool registry.
- `[[agents]]` (array of tables, required): callable agents.

### `[runtime]`

- `default_permission`: one of `ask-first`, `restricted`, `yolo`.
- `default_timeout_sec`: positive integer.
- `allow_network_tools`: boolean global network toggle.
- `log_tool_calls`: boolean.

### `[[tools]]`

- `id` (required, unique)
- `name` (required)
- `description`
- `kind`: `builtin`, `mcp`, `script`, `http`
- `enabled`: boolean
- `entry_point`: required when `kind` is `mcp`, `script`, or `http`
- `timeout_sec`: positive integer
- `requires_approval`: boolean
- `permitted_actions`: non-empty string list, e.g. `read`, `write`, `search`, `execute`, `git`, `chat`, `network`

### `[[agents]]`

- `id` (required, unique)
- `name` (required)
- `description`
- `skill` (short capability keyword)
- `mode` (e.g. `planning`, `coding`, `chat`, `custom`)
- `model_provider` / `model` (optional override; fallback is active UI model)
- `system_prompt` or `prompt_file`
- `allowed_tools`: non-empty tool ID list
- `permitted_actions`: action list for high-level policy
- `permission`: `ask-first`, `restricted`, or `yolo`
- `temperature`, `max_tokens`, `max_steps`
- `handoffs`: list of target agent IDs
- `enabled`: boolean

## Validation rules

Spettro validates at startup:

- unknown TOML fields are rejected
- tool IDs and agent IDs must be unique
- `default_agent` must exist
- all `allowed_tools` and `handoffs` must reference existing IDs
- all permissions and timeouts must be valid

## Writing tips

- Start from the included `spettro.agents.toml` template.
- Keep IDs stable; rename labels, not IDs, to avoid breaking references.
- Use narrow `allowed_tools` and `permitted_actions` by default.
- Keep one responsibility per agent (`planning`, `coding`, `chat`, etc.).
