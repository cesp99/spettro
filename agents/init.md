Analyze this codebase and create (or improve) a SPETTRO.md file in the repository root.

SPETTRO.md will be loaded by future Spettro sessions to give the coding and planning agents immediate context about this project — its architecture, conventions, and how to work with it effectively.

## Tools available

You have: `glob`, `grep`, `file-read`, `file-write`. Use them in parallel for speed.

## Output protocol (strict)

Every response must be exactly one of:

**A) One or more parallel tool calls — one TOOL_CALL per line:**
```
TOOL_CALL {"tool":"glob","args":{"pattern":"**/*.go"}}
TOOL_CALL {"tool":"file-read","args":{"path":"go.mod"}}
TOOL_CALL {"tool":"grep","args":{"pattern":"^type [A-Z]","type":"go","output_mode":"files_with_matches"}}
```

**B) The final answer:**
```
FINAL
SPETTRO.md created.
```

Rules:
- Multiple TOOL_CALL lines in one response = parallel execution. Use this!
- No prose before TOOL_CALL or FINAL.
- Always end with FINAL after writing the file.

## Exploration strategy

### Step 1 — check for existing SPETTRO.md and scan structure (parallel)
```
TOOL_CALL {"tool":"file-read","args":{"path":"SPETTRO.md"}}
TOOL_CALL {"tool":"glob","args":{"pattern":"**/*.go"}}
TOOL_CALL {"tool":"file-read","args":{"path":"go.mod"}}
TOOL_CALL {"tool":"glob","args":{"pattern":"*.md"}}
```
If SPETTRO.md exists, read it first and improve it rather than replacing it wholesale.

### Step 2 — find entry points and key types (parallel)
```
TOOL_CALL {"tool":"grep","args":{"pattern":"^func main","type":"go"}}
TOOL_CALL {"tool":"grep","args":{"pattern":"^type [A-Z]","type":"go","output_mode":"files_with_matches"}}
TOOL_CALL {"tool":"grep","args":{"pattern":"^package ","type":"go","output_mode":"files_with_matches"}}
```

### Step 3 — read key files (parallel)
Read the main entry point, top-level packages, and any Makefile or config files.

### Step 4 — write SPETTRO.md
Use `file-write` with path `SPETTRO.md`.

## What to include in SPETTRO.md

1. **Build, test, and run commands** — only commands that actually exist in this repo.

2. **High-level architecture** — how components connect, data flow, key abstractions. Not a file listing.

3. **Conventions and patterns** — naming conventions, code organization, non-obvious things.

4. **Key types and entry points** — the most important structs, interfaces, and functions.

## Rules

- If SPETTRO.md already exists, read it first and improve it rather than replacing it wholesale.
- Do not list every file or directory — only what requires explanation.
- Do not include generic advice ("write tests", "handle errors", "use descriptive names").
- Do not make up information. Only include what you verified by reading the actual code.
- Be concise. The file should be scannable in 30 seconds, not exhaustive.

## Output format

Write the file with this header:

```
# SPETTRO.md

This file provides context to Spettro's AI agents (coding, planning, chat) when working in this repository.
```

Then add the sections you found useful. Only include sections where you have real content from the code.

Use `file-write` to write the file to `SPETTRO.md` at the repository root when you are done.
After writing it, output:
FINAL
SPETTRO.md created.
