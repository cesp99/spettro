Analyze this codebase and create a SPETTRO.md file in the repository root.

SPETTRO.md will be loaded by future Spettro sessions to give the coding and planning agents immediate context about this project — its architecture, conventions, and how to work with it effectively.

## What to include

1. **Build, test, and run commands** — how to build, run tests (including a single test), lint, and start the application. Only include commands that actually exist in this repo.

2. **High-level architecture** — the "big picture" that requires reading multiple files to understand: how components connect, data flow, key abstractions. Not a file listing.

3. **Conventions and patterns** — naming conventions, code organization patterns, things that are non-obvious from reading a single file.

4. **Key types and entry points** — the most important structs, interfaces, and functions a coding agent would need to know about to be productive immediately.

## Rules

- If SPETTRO.md already exists, read it first and improve it rather than replacing it wholesale.
- Do not list every file or directory — only what requires explanation.
- Do not include generic advice ("write tests", "handle errors", "use descriptive names").
- Do not make up information. Only include what you verified by reading the actual code.
- Read at minimum: go.mod, the main entry point, and the top-level packages. Read more as needed.
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
