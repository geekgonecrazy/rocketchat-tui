# Atomic VCS Agent

You use **Atomic VCS** (not git). A draft view is created for each session automatically.

## Version control rules

- **Never use `git` for repository operations.** Do not run `git status`, `git diff`, `git log`, `git add`, `git commit`, `git checkout`, `git branch`, `git merge`, `git pull`, `git push`, or any other `git` command.
- Use the **Atomic CLI** for version-control context:
  - `atomic status` instead of `git status`
  - `atomic diff` instead of `git diff`
  - `atomic log` instead of `git log`
  - `atomic change <hash>` instead of `git show <hash>`
  - `atomic view list` instead of `git branch`
  - `atomic view switch <name>` instead of `git checkout <name>` when the user explicitly asks to switch views
  - `atomic pull` / `atomic push` instead of `git pull` / `git push`
- **Recording is automatic.** The OpenCode plugin records your changes automatically with full AI provenance (model, tokens, session, timing, decision graph) when the turn ends. **Do NOT run `atomic add` or `atomic record` yourself** — doing so pre-empts the plugin's recording and loses the provenance graph. The plugin fires on `session.idle` and handles everything.

## Every prompt is a turn. Every turn follows this sequence.

### 1. Create an intent

```bash
atomic intent new "<short title>"
```

This scaffolds a **directive-based** intent — a `:::why`, an
`:::acceptance-criterion`, a `:::task`, and `:::scope-in`/`:::scope-out`/
`:::constraint` — and prints its ID (e.g. `DEMO::you::4`) and file path.

Use `atomic intent new` — this is the only way to create an intent. (The old
`atomic vault intent create` wrote a legacy markdown template that did not lift
— no `:::why`, so it could never validate or attest — and has been removed.)

### 2. Define the problem — fill the directives

The user's prompt is usually a **solution** ("build me X"). Reframe it as a
**problem**. Ask clarifying questions if it's ambiguous — do not guess.

Edit the intent file and replace **every** stub:

- **`:::why`** — why this work matters. **Mandatory**: the gate rejects an
  intent with no `why` (its content isn't graded, but it must be present).
- **`:::acceptance-criterion{#…}`** — a single concrete, checkable outcome that
  means "done." Add more as the work needs.
- **`:::task{#… criteria=…}`** — an ordered work item toward a criterion; name
  the files it touches with `::file-ref{path=…}`.
- **`:::scope-in` / `:::scope-out` / `:::constraint`** — the boundaries and
  rules to respect.

Then `atomic vault sync` to persist your edits. `validate`/`attest`/`show` read
from the database, so sync **before** them or they see the stale scaffold.

### 3. Execute the tasks

Work the tasks in order. After each one: **verify** it (run the checks), then
mark it in the intent file with your **file-editing tool** — flip the
acceptance-criterion `status=unmet` → `status=met` when its outcome holds — and
`atomic vault sync`. Never edit the file with bash/Python/sed; that bypasses the
vault.

**A met criterion needs three attributes, not one.** Add `verifiedBy` and
`evidence` in the same edit:

```markdown
:::acceptance-criterion{#<uid>-ac-1 status=met verifiedBy="<who/what checked it>" evidence="<how it was checked>"}
```

Setting only `status=met` fails with `a met acceptance criterion must carry
verifiedBy and evidence`.

### 4. Validate, attest, and complete

An intent is not done until it **conforms and is signed** — this is the gate
that forces a clean intent:

```bash
atomic vault sync                          # persist your edits first
atomic intent update <ID> --status done    # mark it done
atomic vault sync
atomic intent validate <ID>                # MUST conform
atomic intent attest <ID>                  # sign the completed intent
```

`validate` is a hard gate. Before `attest` the only violations it may report
are the fillable `attributedTo` + `proof` (which `attest` fills). **If it flags
`why` or a criterion, your directives are incomplete — fix them, `atomic vault
sync`, and validate again before attesting.** Confirm with `atomic intent list`:
the intent must show `fresh` / `✓`.

**Do NOT run `atomic add` or `atomic record`.** The OpenCode plugin records your
changes automatically with full AI provenance when the turn ends. (`atomic vault
sync` only moves your `.vault/` edits into the vault database; it is not `atomic
record`.)

### 5. Record durable memories

Before finishing, review the turn's ledger and reasoning and capture each
**durable insight** as an Atomic memory of the **right kind** — don't force
everything into `decision`. Classify each insight into one of the allowed kinds
(`atomic memory kinds`): `decision`, `lesson`, `constraint`, `preference`,
`context`. A turn may yield several (e.g. a decision *and* a lesson) — record
one memory per insight — or nothing. See the `/decision-record` skill for the
rubric and source-linking table.

For each insight: create, **attest and validate**, and
link it to the **most specific** source it came from — the acceptance criterion,
task, or todo — not just the intent:

```bash
ID=$(atomic memory new --kind <chosen-kind> \
  --text "<the insight, self-contained>" \
  --derived-from urn:atomic:ac:<UID>-ac-1,urn:atomic:intent:<UID> \
  --json | jq -r .id)
atomic memory attest "$ID"      # signs it — fills attributedTo + proof
atomic memory validate "$ID"    # confirm it conforms once signed
```

**Attest first, then validate.** Validating a fresh memory exits 2 because
`attributedTo` and `proof` are only added by `attest`.

`--derived-from` takes canonical urns (comma-separated), each becoming a
`wasDerivedFrom` edge in the graph: `urn:atomic:ac:<UID>-ac-N` (acceptance
criterion), `urn:atomic:task:<UID>-N` (task), and
`urn:atomic:intent:<UID>` (fallback). For a todo, use
`atomic query search "<todo text>"`, copy its exact KG id, and prefix it with
`urn:atomic:`; todo nodes may be session-scoped. Read intent and criterion/task
ids straight from the intent file.

Record only genuine insights (chose X over Y and why, a corrective lesson, a
constraint discovered, a durable preference/context) — **not** routine steps or
a restatement of the intent. `atomic memory new` writes to the vault (no
`atomic record`, no `atomic vault sync` needed); the plugin records it at turn
end.

## Rules

- **One intent per turn.** Every prompt gets its own intent.
- **Every intent must end conforming and attested.** Create it with `atomic intent new` (the only way to create an intent), fill the mandatory `:::why` + at least one `:::acceptance-criterion` and `:::task`, and finish with `atomic intent validate` → `atomic intent attest`. The intent is not done until `atomic intent list` shows it `fresh` / `✓`. A missing `why` is a hard gate failure — fix it, don't skip it.
- **Record durable memories at turn end.** Classify each durable insight into the right kind from `atomic memory kinds` (`decision`/`lesson`/`constraint`/`preference`/`context`) and `atomic memory new --kind <kind>` it (see `/decision-record`) — keep them high-signal, one memory per insight, attested, and linked to the most specific source with `--derived-from`.
- **Problem first.** Reframe solution-requests as problems. Ask questions if unclear.
- **Write the intent file before coding.** The plan goes in the file, not just in chat.
- **Do NOT run `atomic add` or `atomic record`.** The plugin handles recording with provenance automatically. Running these commands yourself pre-empts the plugin and loses the provenance graph.
- **Simplification guard.** When you pick an approach simpler than or divergent from a reference (the standard library, an existing implementation, a spec, a prior version), the simpler choice almost always drops a behavior the reference guaranteed. Name what it drops — interrupted/partial operations, error or panic states, round-trip fidelity, ordering, resource cleanup, concurrency, overflow/empty/boundary inputs — and for each, either pin it as an acceptance criterion, record it explicitly as out-of-scope with the consequence stated, or ask the user. Never leave it unstated. A decision about API *shape* is not a decision about *behavior*: the same signature can be implemented correctly or incorrectly, so resolve behavioral gaps as separate items.
- **Do run `atomic vault sync` after editing any `.vault/` file**, and before `atomic intent show`/`update`. It deflates your on-disk edits into the vault database; it is not `atomic record` and hooks do not do it for you mid-turn.
- **Do not create or switch views.** The session view is created automatically.
- **Do not run `atomic agent enable`.** The integration is already configured globally.

## Skills

Use these for detailed reference when needed:

- `/atomic-vault` — intent and goal lifecycle, memory operations
- `/decision-record` — capture durable decisions as searchable, attestable memory records at turn end
- `/atomic-vcs` — inspect repository state and history: `status`, `log`, `change` (`-p` provenance, `-a` AI attestation), `diff`
- `/code-intelligence` — knowledge graph queries for code exploration
