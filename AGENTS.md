---
description: Intent-driven development with Atomic VCS. Converts each prompt into a problem statement, plans tasks, executes, and records with provenance.
mode: primary
permission:
  edit: allow
  bash: allow
  skill:
    "*": allow
---

You use **Atomic VCS** (not git). A draft view is created for each session automatically.

## Every prompt is a turn. Every turn follows this sequence.

### 1. Create an intent

```bash
atomic vault intent create --title "<short title>"
```

This gives you an intent ID (e.g., HELL-4) and a file path.

### 2. Define the problem

The user's prompt is usually a **solution** ("build me X"). Reframe it as a **problem statement**.

Ask clarifying questions if the problem is ambiguous. Do not guess — ask.

Once the problem is clear, define:

- **Problem statement** — what problem are we solving and why
- **Success criteria** — concrete, testable conditions that mean "done"
- **Tasks** — ordered list of work items

Write all of this into the intent file. Replace every REPLACE placeholder.

Then run `atomic vault sync` to persist the file into the vault database. The intent file lives on disk, but `atomic vault intent show`/`update` read from the database — without `sync` they see the original placeholder template, and `update` will overwrite your file edits with it.

### 3. Execute the tasks

Work through the tasks. Check off tasks as you complete them.

### 4. Update the intent

```bash
atomic vault sync                          # persist file edits to the database first
atomic vault intent update <ID> --status done
```

Always `atomic vault sync` before `atomic vault intent show`/`update` — the CLI reads from the database, not the file, so an unsynced `show` renders the stale placeholder template and `update` re-materializes the database copy over the file, clobbering your edits.

**Do NOT run `atomic add` or `atomic record`.** The OpenCode plugin records your changes automatically with full AI provenance (model, tokens, session, timing) when the turn ends. (`atomic vault sync` is not `atomic record` — it only moves your `.vault/` edits into the vault database, and you must run it even though the plugin handles recording.)

## Rules

- **One intent per turn.** Every prompt gets its own intent.
- **Problem first.** Reframe solution-requests as problems. Ask questions if unclear.
- **Write the intent file before coding.** The plan goes in the file, not just in chat.
- **Simplification guard.** When you choose an approach simpler than or divergent from a reference (the standard library, an existing implementation, a spec, a prior version), name the behavior the simpler choice drops — interrupted/partial operations, error or panic states, round-trip fidelity, ordering, resource cleanup, boundary/empty/overflow inputs — and for each either pin it as an acceptance criterion, record it explicitly as out-of-scope with the consequence stated, or ask the user. Never leave it unstated. A decision about API *shape* is not a decision about *behavior*: the same signature can be implemented correctly or incorrectly, so resolve behavioral gaps as separate items.
- **Do run `atomic vault sync` after editing any `.vault/` file**, and before `atomic vault intent show`/`update`. It deflates your on-disk edits into the vault database; it is not `atomic record` and the plugin does not do it for you mid-turn.
- **Do not run `atomic add` or `atomic record`.** The plugin handles this with provenance.
- **Do not create or switch views.** The session view is created automatically.
- **Do not run `atomic agent enable`.** The integration is already configured globally.

## Skills

Load these for detailed reference when needed:

- `@atomic-vault` — intent and goal lifecycle, memory operations
- `@atomic-vcs` — inspect repository state and history: `status`, `log`, `change` (`-p` provenance, `-a` AI attestation), `diff`
- `@code-intelligence` — knowledge graph queries for code exploration
