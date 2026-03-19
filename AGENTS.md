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

- `version` (int, required): schema version, currently `2`.
- `default_agent` (string, required): agent ID to start from.
- `[metadata]` (table, optional): human-facing metadata.
- `[runtime]` (table, required): global execution defaults.
- `[[tools]]` (array of tables, required): tool registry.
- `[[agents]]` (array of tables, required): callable agents.

### `[runtime]`

- `default_permission`: one of `ask-first`, `restricted`, `yolo`.
- `default_timeout_sec`: positive integer.
- `sandbox_mode`: `workspace-write`, `read-only`, or `full-access`.
- `log_tool_calls`: boolean.
- `permission_rules`: optional layered policy rules (`permission`, `pattern`, `action`).
- `[runtime.delegation]`: defaults for `max_parallel_workers` and `max_depth`.

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
- `aliases`: optional alternate tool IDs
- `input_schema`: optional JSON-like schema metadata
- `risk_level`: optional `low|medium|high`
- `primary_only`: optional boolean (only primary/orchestrator agents can use)
- `permission_rules`: optional tool-scoped policy rules

### `[[agents]]`

- `id` (required, unique)
- `name` (required)
- `description`
- `skill` (short capability keyword)
- `mode` (e.g. `planning`, `coding`, `chat`, `custom`)
- `role`: `primary`, `subagent`, `orchestrator`, or `worker`
- `model_provider` / `model` (optional override; fallback is active UI model)
- `system_prompt` or `prompt_file`
- `allowed_tools`: non-empty tool ID list
- `permitted_actions`: action list for high-level policy
- `permission`: `ask-first`, `restricted`, or `yolo`
- `temperature`, `max_tokens`, `max_steps`
- `permission_rules`: optional agent-scoped policy rules
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

## Prompt file folder

This repository ships a ready-to-edit prompt folder:

- `agents/`

The default pack includes specialized roles for day-to-day CLI/TUI work:

- planning
- coding
- chat
- explore
- git
- reviewer
- tester
- docs-writer
