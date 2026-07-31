# Compose in `$EDITOR`

**Status:** proposed · **Scope:** `internal/ui`, `internal/config`, docs

## TL;DR

`ctrl+g` in the composer writes the current draft to
`$XDG_DATA_HOME/rctui/compose.md`, hands the terminal to `$EDITOR` via
`tea.ExecProcess`, and replaces the composer with whatever was saved.

Because the file is pre-seeded and its path is stable, **quitting without
saving is already a no-op** — no code implements that. It falls out, and it
gives two cancel gestures for free (quit-without-save, and `:cq` via non-zero
exit), which is what makes it safe to treat an empty file literally as "clear
the composer".

- **Editor:** `config.editor` → `$VISUAL` → `$EDITOR` → refuse with a notice. No `vi` fallback.
- **Scope:** composer focus only, no guards — works mid-edit, in threads, in read-only rooms.
- **Draft file:** stable, 0600, never deleted; it is the recovery copy.
- **Paste-back:** CRLF→LF, trailing newlines stripped, four-line clamp, **all completers closed** — syncing them would let `m.mentions` swallow the user's `enter`.
- **New code:** `internal/ui/compose_editor.go`, an `Editor` config field + `ComposePath()`, a `composePath` model field (required for testability), one case in `handleComposerKey`.
- **Docs:** six surfaces — the cancel semantics and the recovery file are invisible from a key table.

## Problem

The composer is a `bubbles/textarea` capped at four visible lines, with `enter`
bound to send and newlines behind `alt+enter`. Writing anything long — a
handover note, a postmortem paragraph, a fenced code block — means composing
blind in a four-line window where the send key is one stray keystroke away.
People currently write elsewhere and paste, which loses the draft on a
disconnect and mangles indentation in terminals that bracket-paste badly.

## Proposal

`ctrl+g`, while the composer has focus, writes the current draft to a file,
hands the terminal to the user's editor, and replaces the composer with
whatever was saved.

### The property the design rests on

The draft file is **pre-seeded and stable** (same path every time). So quitting
the editor without saving is already a no-op — the file still holds the draft,
and the composer gets its own content back. Nothing special implements this.

That yields two cancel gestures for free:

- quit without saving → composer unchanged
- `:cq` / any non-zero exit → composer unchanged, *even after saving*

Which is what makes it safe to honour an empty file literally as "clear the
composer": anyone wanting to bail had two better exits.

### Behaviour

| Situation | Result |
| --- | --- |
| Editor saves content | Composer replaced; cursor at end |
| Editor saves an empty file | Composer cleared, height reset to 1 |
| Editor quits without saving | Composer unchanged |
| Editor exits non-zero (`:cq`, crash) | Composer unchanged; informational notice |
| Editor cannot be launched | Composer unchanged; error notice |
| No editor configured | Refuse with a notice; terminal never handed over |

Editor resolution: `config.editor` → `$VISUAL` → `$EDITOR` → refuse. No
hard-coded `vi` fallback — trapping a non-vi user in a modal editor from a chat
client is worse than an actionable message.

Draft file: `$XDG_DATA_HOME/rctui/compose.md`, mode 0600, rewritten on each
open, **never deleted**. It is the recovery copy if anything goes wrong between
the editor exiting and the composer being filled.

### Rejected

- **Global key** (like `ctrl+o`) — composer-only; opening an editor while
  scrolling the timeline reads as a bug.
- **Temp file** — deleting it discards the only copy of five minutes' writing.
- **Per-invocation files** — bounded growth beats a cleanup reaper we would own
  forever. Cost: two live instances share the path, last save wins.
- **Cursor position** (`+N`) — `code`, `helix`, `micro` disagree with vim; a
  wrong guess becomes a spurious filename argument.
- **`RCTUI_EDITOR`** — config already wins over the env vars.
- **Settings-pane entry** — that pane is a toggle list with no text-entry
  widget, same as `sound_command` and `download_dir`.

---

# Handoff plan

Facts already verified against the tree — do not re-derive:

- `ctrl+g` is **unbound** in `bubbles/textarea` v0.21.0's default keymap.
  `InsertNewline` is rebound to `alt+enter`/`ctrl+j` at `internal/ui/chat.go:161`.
- `tea.Exec` precedent: `internal/ui/viewer.go:41` (custom `ExecCommand`),
  dispatched at `internal/ui/attachments.go:122`, completion handled at
  `internal/ui/attachments.go:129`. Use plain `tea.ExecProcess` here — the
  custom type exists only because the viewer *draws*.
- `tea.WithAltScreen()` + `tea.WithMouseCellMotion()` at `cmd/rctui/main.go:76`.
  Bubble Tea's exec path releases and restores both; no manual handling needed.
- `sh -c` precedent: `internal/notify/notify.go:208`.
- `getenv`-as-parameter precedent: `internal/termimg/detect.go:44`.
- `config → model` plumbing precedent: `downloadDir: cfg.Downloads()` at
  `internal/ui/chat.go:180`.

## 1. `internal/config/config.go`

```go
// Editor is the command used to compose long messages; "" falls back to
// $VISUAL, then $EDITOR.
Editor string `json:"editor,omitempty"`
```

- `func (c *Config) EditorCommand(getenv func(string) string) string` — trimmed
  `c.Editor`, else `getenv("VISUAL")`, else `getenv("EDITOR")`, else `""`.
  Takes `getenv` so precedence is testable without `t.Setenv`.
- `func ComposePath() (string, error)` — `$XDG_DATA_HOME/rctui/compose.md`,
  resolved exactly as `Paths()` does it. **Additive**: leave `Paths()`'s
  signature alone so `cmd/rctui/main.go` is untouched.

## 2. `internal/ui/chat.go`

- Field beside `downloadDir` (~line 141):

  ```go
  // composePath is the file handed to $EDITOR by ctrl+g. Deliberately stable
  // and never deleted: between the editor exiting and the composer being
  // filled, it is the only copy of the message.
  composePath string
  ```

- `newChatModel` sets it from `config.ComposePath()`; on error leave it `""`
  and let the key refuse with a notice. Tests overwrite the field directly
  (same package) — **this field is what makes the feature testable at all.**
- Message type beside `clearNoticeMsg` (~line 39):

  ```go
  // editorFinishedMsg reports that $EDITOR has handed the terminal back.
  type editorFinishedMsg struct{ err error }
  ```

- `Update` case beside `viewerFinishedMsg` (~line 263): `return m.editorClosed(msg)`.

## 3. `internal/ui/compose_editor.go` (new)

New file rather than growing `chat_keys.go`, matching how `chat_edit.go` and
`attachments.go` are split out.

**`openEditor() (chatModel, tea.Cmd)`**

1. `composePath == ""` → error notice, return.
2. `m.cfg.EditorCommand(os.Getenv)`; empty → `m.notify("set $EDITOR to compose in an external editor", false)`.
3. `os.MkdirAll(filepath.Dir(path), 0o700)`; `os.WriteFile(path, []byte(m.composer.Value()), 0o600)`.
   A failed write returns a notice and **must not** hand over the terminal.
4. `tea.ExecProcess(editorCmd(editor, path), func(err error) tea.Msg { return editorFinishedMsg{err} })`

**`editorCmd(editor, path string) *exec.Cmd`**

```go
exec.Command("sh", "-c", editor+` "$@"`, "sh", path)
```

The path arrives as `$1` and is **never parsed by the shell**, so spaces or
metacharacters in `$XDG_DATA_HOME` are safe while `EDITOR="code --wait"` and
`EDITOR="emacsclient -c -a ''"` still work. Windows (`runtime.GOOS`) falls back
to `strings.Fields`; a quoted Windows path containing spaces is knowingly
unsupported.

**`editorClosed(msg editorFinishedMsg) (chatModel, tea.Cmd)`**

- `msg.err != nil` → composer untouched.
  `*exec.ExitError` → `notify("editor exited without saving; draft unchanged", false)` (**not** an error — `:cq` is legitimate).
  Otherwise → `notify("could not launch editor: "+err.Error(), true)`.
- Else read the file (read failure → error notice, composer untouched), then:
  - `normalizeDraft`, `m.composer.SetValue(...)` (leaves cursor at end)
  - `SetHeight(clamp(lines, 1, 4))` — same clamp as `handleComposerKey`
  - **close `m.cmdPicker`, `m.mentions`, `m.picker` — do not `sync` them**
  - `m.rebuildBody()`
  - typing indicator: `UserTyping` if non-empty, `StopTyping` if empty, skipped
    while `m.editing()` — mirroring the typed path

> The close-don't-sync rule prevents a real bug, not just noise: `m.mentions`
> intercepts `enter` to accept a completion, so a message ending in `@dou`
> would swallow the user's send. Text arriving wholesale from an editor is
> finished, not mid-typing.

**`normalizeDraft(s string) string`**

`strings.ReplaceAll(s, "\r\n", "\n")` then `strings.TrimRight(s, "\n")`.
Nothing else — leading and interior whitespace is content in a chat client
(indented code, fenced blocks, deliberate blank lines). `TrimRight` on `"\n"`
only, not `" \t\n"`, so a trailing space inside a code fence survives.

## 4. `internal/ui/chat_keys.go`

One case in `handleComposerKey`, before the fall-through to `m.composer.Update`:

```go
case "ctrl+g":
    return m.openEditor()
```

No guards. It stays live while editing a sent message (`m.editing()` — arguably
the best use of the feature; must not clear `m.editID`), in threads, in
read-only rooms, and with no room open. The composer is *typeable* in all of
those; "I can type here but ctrl+g refuses" is harder to explain than letting
it work, and `send()` already enforces the real rule. The attach prompt is
excluded for free — `m.attach.open` intercepts before `handleComposerKey`.

## 5. Tests

| Test | Location | Covers |
| --- | --- | --- |
| `TestEditorCommandPrecedence` | `internal/config/config_test.go` | config / `$VISUAL` / `$EDITOR` / both / neither |
| `TestNormalizeDraft` | `internal/ui/compose_editor_test.go` | CRLF; trailing newlines; preserved leading + interior whitespace; empty |
| `TestEditorCmdArgs` | same | `nvim`; `code --wait`; editor path with a space; **compose path with a space** (pins the `"$@"` quoting) |
| `TestEditorClosed` | `internal/ui/` (`chat_test.go` style) | success → value set, completers closed, height clamped, `editID` preserved mid-edit; empty file → cleared; `*exec.ExitError` → unchanged + informational notice; launch failure → unchanged + error notice |

Not tested: the `tea.Exec` handoff itself — it needs a pty harness,
disproportionate for three lines of glue.

## 6. Docs — six surfaces, all required

1. `internal/ui/render/chrome.go:328` key list — `{"ctrl+g", "write your message in $EDITOR"}`, beside `ctrl+o`.
2. `internal/ui/render/chrome.go:370+` — a 4–5 line prose block at the density of the `Attachments` block.
3. README key table (~line 128) — matching row.
4. README `### Composing in an editor` — precedence, both cancel gestures, the recovery file.
5. README config docs — the `editor` field, where `sound_command` / `download_dir` are described.
6. README Features bullets (~line 34) — one line.

The prose blocks earn their place: quit-to-cancel, empty-clears, `:cq`-aborts
and the `compose.md` recovery file are all invisible from a key table, and an
undocumented recovery file insures nobody.

## Acceptance criteria

- [ ] `ctrl+g` in the composer opens `$VISUAL`/`$EDITOR`/`config.editor`, seeded with the draft.
- [ ] Saving replaces the composer; the four-line clamp and `rebuildBody()` are applied.
- [ ] Quitting without saving leaves the composer unchanged.
- [ ] Non-zero exit leaves the composer unchanged, with an *informational* notice.
- [ ] Saving an empty file clears the composer and resets its height to 1.
- [ ] No editor configured → notice, terminal never handed over.
- [ ] CRLF normalised; trailing newlines stripped; leading/interior whitespace intact.
- [ ] All three completers are closed after paste-back — `enter` sends rather than completing.
- [ ] Works mid-edit of a sent message without clearing `editID`.
- [ ] A compose path containing a space launches correctly (test-pinned).
- [ ] All six doc surfaces updated.
- [ ] `go test ./...` passes.

## Known limitations (accepted, not oversights)

- Two live instances share `compose.md`; last save wins.
- A message cannot end with a blank line via the editor (Rocket.Chat trims anyway).
- Cursor position is not carried into the editor.
- Windows uses `strings.Fields`, so quoted paths with spaces in `$EDITOR` fail there.
- The composer does not grow past four lines for editor-sourced content.
