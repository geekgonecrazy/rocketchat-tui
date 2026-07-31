# rctui

A terminal client for Rocket.Chat, written in Go with
[Bubbletea](https://github.com/charmbracelet/bubbletea) and
[Lipgloss](https://github.com/charmbracelet/lipgloss).

It ships its own minimal Rocket.Chat client rather than depending on the
deprecated Go SDK: REST for anything fetched or paginated, DDP over WebSocket
purely for realtime push. State is cached in SQLite, so launching shows your
rooms and history immediately instead of an empty screen.

## Features

- Login with username/password (with 2FA) or a personal access token; the
  session is cached so you only sign in once
- Sidebar ordered by message activity and nothing else, so positions stay put;
  unread and mentions show as weight and badges rather than by reordering
- Channels, private groups, DMs, teams, and discussions, each with its own sigil
- Message history with scroll-back paging
- A **new messages** divider frozen at where you left off, and the view scrolls
  to it when you open a room
- Live typing indicators, sent and received the way the web client does it
- Thread view: browse a room's threads, read a thread, and reply into it
- Fix a typo: `↑` in an empty composer brings your last message back to edit,
  again for the one before, staying inside whatever you are looking at
- Emoji: shortcodes render as glyphs, `:jo` pops up an autocomplete while you
  type, and any message can be reacted to
- Mentions: `@` autocompletes the people in the room, plus `@all` and `@here`;
  `#` autocompletes the rooms you are in
- Slash commands: `/` lists everything this server offers — its own commands and
  any an app has added — plus the ones rctui carries itself, from `/leave` and
  `/invite` down to `/exit`
- Send files: `ctrl+o` gives you a path prompt with tab-completion, attach as
  many as you like, and nothing uploads until you send the message
- Works offline against the local cache; reconnects and resyncs automatically

## Install

```sh
go install github.com/geekgonecrazy/rocketchat-tui/cmd/rctui@dev
```

That puts an `rctui` binary in `$(go env GOPATH)/bin` — add it to your `PATH` if
it is not there already. Then just:

```sh
rctui
```

The first run asks for your server and credentials; after that it goes straight
to your rooms.

Building from a clone instead:

```sh
git clone https://github.com/geekgonecrazy/rocketchat-tui
cd rocketchat-tui
go build -o rctui ./cmd/rctui
./rctui
```

Requires Go 1.24 or newer. The SQLite driver is pure Go
(`modernc.org/sqlite`), so cross-compiling a static binary needs no cgo or C
toolchain:

```sh
GOOS=darwin GOARCH=arm64 go build -o rctui-darwin-arm64 ./cmd/rctui
```

Flags:

| Flag | Purpose |
| --- | --- |
| `-server <url>` | Server address, overriding the saved one |
| `-db <path>` | Cache database location |
| `-log <path>` | Write debug logs to a file (never to the terminal) |
| `-logout` | Forget saved credentials and exit |

Config lives at `$XDG_CONFIG_HOME/rctui/config.json` (mode 0600, it holds an
auth token); the cache lives at `$XDG_DATA_HOME/rctui/cache.db`.

## Keys

| Key | Action |
| --- | --- |
| `tab` / `shift+tab` | Move focus: rooms → messages → composer |
| `↑ ↓` or `k j` | Move within the focused pane |
| `enter` | Rooms: open · Messages: open or start a thread · Composer: send |
| `↑` (empty composer) | Edit your last message; again for the one before, `↓` forward |
| `ctrl+t` | Thread list for this room — works while typing |
| `r` | React to the selected message |
| `@` / `#` | Autocomplete a person or a channel while typing |
| `/` (empty composer) | List the slash commands this server offers |
| `alt+enter` | Newline in the composer |
| `/` | Filter the room list |
| `g` | Load older messages |
| `u` | Jump to the unread line |
| `U` | Mark unread: the room under the sidebar cursor, or from the selected message |
| `t` | Thread list (messages pane only) |
| `v` | View the selected message's image full-screen |
| `s` | Save the selected message's attachment to your downloads |
| `o` | Open the selected message's attachment in a desktop app |
| `ctrl+o` | Attach a file to send with your message |
| `ctrl+x` | Remove the last file you attached |
| `esc` | Close thread / clear filter / leave the composer |
| `ctrl+r` | Resync now |
| `ctrl+l` | Mark the current room read |
| `?` | Toggle help |
| `ctrl+c` | Quit |

The mouse works too: click a room to open it, click a message to select it,
click a `↳ N replies` line to open that thread, click a reaction to toggle it,
and scroll with the wheel.

### Mentions

Typing `@` in the composer opens a list of the people in the room; keep typing
to narrow it:

```
› thanks @sa
  @sanjay  Sanjay Patel
  @sam.oconnell  Sam O'Connell
  @sandra  Sandra Diaz
```

`tab` or `enter` inserts the username, `↑`/`↓` move, `esc` dismisses. `@all`
and `@here` are offered too, after the people, so a real name is never
displaced while you are typing one.

Candidates are the room's member list merged with everyone who has spoken in
the room, most recently spoken first — so the person you are replying to is
usually the first suggestion, and the list still works from the cache when you
are offline or when the server will not list a channel's members. An `@` in the
middle of a word never opens the list, so email addresses are left alone.

`#` works the same way for rooms:

```
› see also #eng
  #eng-platform  Engineering Platform
  #engineering  Engineering
```

This is worth having because a channel's mentionable name is its slug, not the
display name in the sidebar — `#eng-platform` is what the server links, even
though the room reads as "Engineering Platform". The list inserts the slug and
shows the display name beside it.

Candidates are the rooms you are subscribed to, most recently active first.
Direct messages are not offered: they are addressed by person, not pointed at.
Public channels you have not joined are not offered either — they are
mentionable on the server, but finding them would mean a live search, and this
list is the one already on your machine. A `#` in the middle of a word never
opens it, so `issue#42` is left alone.

### Emoji and reactions

Shortcodes render as glyphs wherever they appear — `:joy:` shows as 😂 in
messages, reactions and thread previews. Text that merely contains colons is
left alone, so `10:30:45` and `http://host/:path:` survive intact.

Typing a colon followed by a letter or two opens an autocomplete:

```
› nice work :ta
  🎉  :tada:
  🍹  :tropical_drink:
  🎯  :dart:
```

`tab` or `enter` inserts the glyph, `↑`/`↓` move, `esc` dismisses.

To react, select a message and press `r`. The picker opens on a set of common
reactions; type to search the full set. Reactions you have already made are
highlighted, and choosing one again removes it — as does clicking it.

### Attachments and images

An attachment renders as a one-liner in the timeline, sigilled `🖼` when it is
an image rctui can show you and `📎` when it is not. Select the message and
press `v`, and the image is drawn full-screen, at whatever resolution your
terminal can manage. Any key returns you to the chat exactly where you left it.

Which resolution that is depends on the terminal:

| Terminal | Protocol | Result |
| --- | --- | --- |
| kitty, Ghostty, WezTerm, Konsole | kitty graphics | Real pixels |
| iTerm2, VS Code, mintty | iTerm2 inline images | Real pixels |
| Anything else, and inside tmux | Half-block cells | Coarse but legible |

Detection is automatic. If it guesses wrong — terminals impersonate one another
constantly — override it:

```sh
RCTUI_IMAGE_PROTOCOL=kitty rctui    # or iterm2, or blocks
```

Inside tmux rctui deliberately drops to half-blocks: a multiplexer does not pass
graphics escapes through without configuration, and repaints over the pixels
even when it does. Half-blocks are made of ordinary cells, so they survive it.

Inside the viewer, `s` saves the image, `o` hands it to your desktop, and `n`/`p`
cycle when one message carries several images. Those keys work from the timeline
too, and `s` and `o` work on any attachment, not just images — that is how you
reach a PDF or a zip. Saved files land in `~/Downloads`, or wherever
`download_dir` in the config points:

```json
{ "download_dir": "~/screenshots" }
```

WebP and AVIF have no decoder in the Go standard library and rctui takes no
dependency to add one, so those open externally rather than drawing.

### Sending files

`ctrl+o` turns the composer into a file prompt. Type a path — `tab` completes
it, `~` means your home directory — and `enter` attaches the file:

```
  screenshot-one.png  screenshot-two.png  scratch/
📎 ~/shots/screen
```

`esc` cancels and gives you back whatever you were writing. `/upload <path>`
typed into the composer does the same thing without the prompt, which is handy
when you already have the path on the clipboard; the path is taken verbatim, so
a name with spaces in it needs no quoting.

Attached files sit above the composer and go nowhere until you send:

```
  📎 diagram.png (84 KB) · perf-notes.txt (1.2 KB)
› numbers attached, have a look
```

Attach as many as you like. `ctrl+x` removes the last one, and leaving the room
drops the queue along with the draft it belonged to — they were meant for that
conversation.

Rocket.Chat has no notion of a message with files bolted onto it: each file is
its own message. So sending three files posts three messages, in the order you
attached them, and your text rides on the first. If a file is refused — the
server has both a media-type whitelist and a size limit — the rest still go, and
the status bar names the one that did not. Should every file be refused, the
text is posted on its own rather than discarded: you watched it leave the
composer, so it has to land somewhere.

Each file is streamed off disk rather than read into memory, and it is sent with
its real media type, worked out from the extension and confirmed against the
first few hundred bytes when there is no extension to go on. That last part
matters more than it sounds: an image uploaded as `application/octet-stream` is
an image no client will ever draw inline.

### Slash commands

Type `/` as the first character in the composer and the list appears:

```
  /invite @username…  add people to this room
  /join #channel      join a public channel
  /leave              leave this room
  /open <room>        jump to a room in the sidebar
```

`tab` completes what is highlighted. `enter` completes it too, except when you
have already typed the command out in full — then `enter` runs it, so `/exit`
does not need a second keystroke to mean what it says. The list closes at the
first space, which hands the rest of the line back to `@` and `#`: `/invite @ja`
completes the username the same way a message does.

Which commands exist is a property of the server, not of this client, so they are
discovered: `commands.list` is fetched at login and cached, and the result is
merged with what rctui implements. Three kinds end up in one list:

| Kind | Examples | Who runs it |
| --- | --- | --- |
| the client's own | `/exit`, `/quit`, `/upload`, `/open`, `/help` | rctui — no server has an opinion about quitting your terminal client |
| the server's | whatever `commands.list` reports, apps included | the server, through `commands.run` |
| rctui's fallbacks | `/leave`, `/part`, `/hide`, `/join`, `/invite`, `/kick`, `/topic`, `/archive`, `/unarchive`, `/create`, `/msg`, `/shrug`, `/tableflip`, `/unflip`, `/lennyface` | rctui, over REST, but only where the server offers no version of its own |

Precedence runs one way. The client's own commands are never displaced — a
server registration of `/open` is flagged `clientOnly` for exactly that reason.
Everywhere else the server wins: it is the authority on what `/leave` means on
that deployment, and its description replaces ours in the list. A command the
server flags `clientOnly` that rctui does not implement cannot run anywhere, so
it is kept out of the list and invoking it says so.

A command nobody recognises is never posted as a message. `/inivte @jane` says
"no such command" and leaves the line in the composer to be corrected, because
sending a typo to the room is both useless and public.

Commands work in read-only rooms — leaving one is exactly what a read-only room
is for — and `/leave` and `/hide` take the room out of the sidebar immediately
rather than at the next resync.

### Starting a thread

Any message can anchor one, whether or not it already has replies:

1. Select the message — `tab` to the messages pane and use `k`/`j`, or just
   click it.
2. Press `enter`. The pane switches to that thread and the composer follows.
3. Type and press `enter`; the reply is posted with the message as its parent.

`ctrl+t` lists every thread in the room, from any focus. `esc` goes back to the
timeline.

### Editing what you sent

With the composer empty, `↑` loads your most recent message into it and marks it
in the timeline. `↑` again steps further back through your own messages, `↓`
comes forward, and past the newest it drops out of edit mode and gives you back
whatever you had typed. `enter` saves, `esc` leaves the message alone.

The gesture only ever offers messages from the context in front of you: in a
thread it walks that thread's replies and its parent, never the channel's other
messages, because the thread's composer posts into the thread. It also only
offers your own messages, and only ones currently loaded — not something ten
pages back that you cannot see. A draft in the composer is left alone: `↑` moves
the cursor then, as it does inside a multi-line message being edited.

Whether an edit is allowed at all is the server's call — editing can be disabled
or time-limited — so nothing changes on screen until the server accepts it, and
a refusal is shown in the status bar. Clearing the box and pressing `enter` is
refused rather than treated as a delete.

## Architecture

```
cmd/rctui/              the binary: flags, config, logging, program startup
internal/rocket/        the mini SDK: REST + DDP, no other internal deps
internal/emoji/         shortcode ↔ glyph table, generated from gemoji
internal/model/         view-facing domain types (Room, Message, Kind)
internal/store/         SQLite cache: rooms, subscriptions, messages, members, paging state
internal/app/           headless core: owns SDK + cache + all state, emits events
internal/ui/            Bubbletea models: input handling and widget state
internal/ui/render/     pure functions: view state → strings, no Bubbletea
internal/fakerc/        fake Rocket.Chat server (REST + DDP) used by the tests
deploy/                 docker compose for a local Rocket.Chat to develop against
docs/api-deviations.md  where the live API differs from its documentation
test/integration/       live-server tests (separate module, build-tagged)
```

Three boundaries do the heavy lifting:

**The core is headless.** `app.Core` runs on a single goroutine and is the only
writer of application state, so no field needs a mutex. Every mutation arrives as
an action closure on a channel; network calls run off-loop and report back the
same way. It publishes `app.Event` values and never renders anything.

**The UI is a pure consumer.** It never calls the SDK, never touches SQLite, and
shares no mutable state with the core. Events arrive as Bubbletea messages via a
re-arming command that reads one event per `Update`, so a burst of server
activity cannot block a redraw and a slow redraw cannot block the network.

**Rendering is separate from the refresh loop.** `internal/ui/render` is pure
functions over explicit view-state structs — no Bubbletea import, no I/O, no
clock beyond formatting. Layout is recomputed when state changes, not on every
frame: the model holds pre-rendered lines plus the anchors (`MessageLine`,
`UnreadLine`) that scrolling needs, and `View` just slices a window out of them.

See `docs/api-deviations.md` for the full catalogue of API-versus-documentation
discrepancies. The most important ones:

### Rocket.Chat specifics worth knowing

- **Two date encodings.** REST sends RFC3339 strings, DDP sends
  `{"$date": millis}`. `rocket.Timestamp` decodes both.
- **Typing has two stream shapes.** Older servers use `<rid>/typing` with
  `[username, bool]`; current ones use `<rid>/user-activity` with
  `[username, ["user-typing"], {}]`. The client subscribes to both and emits on
  both, so indicators work regardless of server version.
- **History endpoints are type-specific.** `channels.history`, `groups.history`,
  and `im.history` are selected from the room's `t`. Teams and discussions are
  ordinary `c`/`p` rooms, so they need no special case.
- **Thread replies are hidden from the timeline** unless the author ticked "also
  send to channel" (`tshow`), matching the web client.
- **Room kind is derived, not given.** A discussion has `prid`; a team main room
  has `teamMain`; both are also `c` or `p`. `model.RoomKind` resolves this once.
- **Sidebar order must not use the subscription's `_updatedAt`.** The server
  bumps it on any subscription change, marking a room read included, so ordering
  by it makes a room jump to the top of the list simply because you opened it.
  Activity means the room's `lm` and the newest message actually cached.
- **The unread divider uses the subscription's `ls`**, captured when the room is
  opened and held there, so it does not slide down as you read or when the room
  is marked read.
- **Unread state has three shapes.** `alert: true` with `unread: 0` is normal
  (counters disabled) and means "something is new, no count"; `ls` may be absent
  entirely, or years stale. So a room can be unread with no number and no anchor,
  and any count derived by tallying messages after `ls` is really just a report of
  how much history you loaded.
- **Slash commands are a server property.** `commands.list` differs per
  deployment (built-ins plus installed apps), pages, and can be permission-gated,
  so it is discovered rather than assumed and a refusal leaves the client's own
  commands working. `clientOnly` on an entry means `commands.run` will not
  execute it — that is a job for whichever client implements it.
- **Client and server clocks disagree** — 94 seconds of skew measured in
  development. Unread state compares a client-held marker against server
  timestamps, so the marker is anchored to server data, never to `time.Now()`.

## Running a server

`deploy/docker-compose.yml` is a minimal MongoDB + Rocket.Chat stack for local
use, with the admin account seeded and the setup wizard skipped:

```sh
cd deploy && docker compose up -d
rctui -server http://localhost:3000
```

See `deploy/README.md` for details, the official upstream compose file, and the
testcontainers-based integration suite in `test/integration`.

## Tests

```sh
go test ./...
go test ./... -race
```

`internal/fakerc` is a working fake server — REST endpoints plus a DDP websocket
that speaks real frames — so the suite covers the whole stack without a live
Rocket.Chat instance. The end-to-end tests in `internal/ui` drive an actual
Bubbletea program through login, room switching, live pushes, typing indicators,
threads, and sending.

To see the rendered layout:

```sh
go test ./internal/ui/ -run TestFullScreenSnapshot -v
```

### Against a real server

Two build-tagged suites run against a live Rocket.Chat instance. Both skip
unless the environment is supplied, so neither affects `go test ./...`.

```sh
# API-level: login, sync, DDP, history, threads, typing, slash command discovery
cd test/integration
RC_SERVER=https://chat.example.com RC_USER=me RC_PASS=…   go test -tags livetest -v

# add RC_ALLOW_WRITE=1 to also exercise sending, and to create/reuse the
# dedicated test fixture (a team, a channel in it, and a discussion)

# UI-level: drives the real TUI and prints each rendered frame
RC_SERVER=… RC_USER=… RC_PASS=…   go test -tags livetest -run TestLiveScreens -v ./internal/ui/
```

Live tests only ever write into their own fixture rooms, never into
pre-existing ones. The fixture is recorded in
`test/integration/testdata/live-fixture.json` and reused across runs.

`docs/api-deviations.md` tracks every place the live API differs from the
published documentation, with the confirmation status of each. Several bugs in
this client were found only by running against a real server — that document is
where those lessons live.

## Regenerating the emoji table

`internal/emoji/table.go` is generated from
[gemoji](https://github.com/github/gemoji), the same database GitHub, Slack and
Rocket.Chat shortcodes come from. To refresh it:

```sh
cd internal/emoji && go generate
```

## Not implemented

Presence (deliberately out of scope), message deletion, search, admin
functions, and E2E-encrypted rooms (their messages arrive as ciphertext).

Slash commands run, but two things around them do not. A command that provides
a preview (`/giphy`) is run outright rather than offering its gallery first, and
a command that answers with a UIKit modal gets a trigger id and nothing to draw
the modal it sends back. There is also no local fallback for `/me`: its wire
form is a message *type* and `chat.sendMessage` exposes no way to set one, so the
server's own `/me` is dispatched normally and there is simply nothing to fall
back to. Same for `/mute` and `/unmute`, which are DDP methods with no REST
route.

Uploads have no progress bar — a large file shows as `syncing` like any other
request — no per-file description separate from the message text, and no retry:
a refused file has to be attached again.

Images are viewed full-screen rather than inline in the timeline: Bubbletea
repaints by diffing lines and has no concept of an image cell, so an inline
placement would have to be tracked and torn down on every scroll, resize, and
incoming message. Sixel is not spoken either — the two native protocols already
cover every terminal that has one, and animation is not played, only the first
frame of a GIF.
