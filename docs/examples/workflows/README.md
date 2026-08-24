# Example workflows

Ready-to-run [workflow](../../workflows.md) scripts. Copy one into a folder
Spettro discovers and it becomes available by name:

```bash
mkdir -p .spettro/workflows                       # this project only
cp docs/examples/workflows/review-branch.js .spettro/workflows/

mkdir -p ~/.spettro/workflows                     # every project
cp docs/examples/workflows/explain-subsystem.js ~/.spettro/workflows/
```

Then:

```text
/workflows                                        list what is available
/workflows show review-branch                     read it before running it
/workflows run review-branch {"base": "develop"}  run it with args
```

| Script | What it does |
| --- | --- |
| [`review-branch.js`](review-branch.js) | Reviews the branch across independent dimensions in parallel, then spawns a skeptic per finding whose job is to *refute* it. Only findings that survive are reported. Demonstrates `pipeline` (no barrier between review and verification), structured output, and adversarial verification. |
| [`explain-subsystem.js`](explain-subsystem.js) | Explains a subsystem by reading it from five deliberately different angles at once, synthesising one account, then critiquing that account against the code. Demonstrates `parallel` as a genuine barrier, phase progression, and a completeness critic. |

Both are read-only: they run searches, reads and `git diff`, and change
nothing. Treat them as starting points — a workflow is just a script, and
the useful ones are usually shaped around your repository.
