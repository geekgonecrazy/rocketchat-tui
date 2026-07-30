# Rocket.Chat API deviations

Places where the live API differs from the published documentation, or where the
documentation is silent on something a client must get right. Each entry records
what we do about it and whether it has been confirmed against a real server.

Confirmation status:

- **confirmed** — observed directly against a live server (version noted)
- **defensive** — handled in code, not yet observed live; based on upstream
  source, changelogs, or older deployments

Servers used for confirmation:

| Target | Version | Notes |
| --- | --- | --- |
| a production Rocket.Chat instance | 8.4 | primary live target |
| fake (`internal/fakerc`) | n/a | reproduces the behaviours below |

The live target is deliberately unnamed: pairing a reachable hostname with an
exact server version in a public repository is free reconnaissance.

### Live test fixture

All live testing writes only into a dedicated, reusable fixture — never into a
pre-existing room. It is created on demand and recorded in
`test/integration/testdata/live-fixture.json`, so repeated runs reuse it instead
of accumulating rooms:

| Item | Name | Kind resolved |
| --- | --- | --- |
| team | `rctui-testing` (private) | team |
| team channel | `rctui-test-channel` | private (team membership kept in `TeamID`) |
| discussion | `rctui-test-discussion` | discussion |

`aaron` is a member of the team. Everything is prefixed `rctui-` and safe to
delete; the recorded file is rebuilt automatically if the rooms are removed.

---

## 1. `chat.getThreadsList` rejects `type=all`

**Status:** confirmed (8.4) · **Severity:** breaks the feature outright

The documentation presents `type` as an optional filter and lists `all` among the
accepted values. On 8.4 sending it fails the request:

```
GET /api/v1/chat.getThreadsList?rid=<rid>&type=all&count=50&offset=0
→ 400 {"success":false,"errorType":"error-invalid-params",
        "error":"must be equal to one of the allowed values"}
```

The parameter is validated against a fixed enum that no longer contains `all`.
Omitting `type` returns every thread, which is what a thread list wants anyway.

**What we do:** never send `type`. See `rocket.ThreadsList`.

**Cost of getting it wrong:** the whole thread list silently 400s. Our core
downgrades thread-list errors to a debug log (threads can legitimately be
disabled server-side), so this failed *quietly* — it only surfaced when run
against a real server. Worth remembering that a lenient error path can hide a
hard failure.

## 2. Two different date encodings on the same fields

**Status:** confirmed (8.4) · **Severity:** silently wrong timestamps

The same logical field arrives in two shapes depending on transport:

| Transport | `ts` / `_updatedAt` / `ls` encoding |
| --- | --- |
| REST | RFC3339 string — `"2026-07-30T12:34:56.789Z"` |
| DDP (websocket) | EJSON — `{"$date": 1785587696789}` |

The REST reference documents the string form; the realtime docs do not spell out
that the same fields switch to EJSON. A client that models these as `time.Time`
works over REST and then silently gets zero values over the websocket — which
means every pushed message sorts to the beginning of the timeline.

**What we do:** `rocket.Timestamp` accepts the string form, the `{"$date": ms}`
form, and a bare integer of milliseconds.

## 3. Typing notifications use two different streams by version

**Status:** confirmed reachable on 8.4 (both subscribed, no errors); which one
this server emits is still unobserved — see *Open questions*
· **Severity:** feature appears broken against half the fleet

There is no single documented typing API. Two shapes exist in the wild:

| Era | Event name | `args` |
| --- | --- | --- |
| legacy | `<rid>/typing` | `[username, isTyping]` |
| current | `<rid>/user-activity` | `[username, ["user-typing"], {}]` |

Neither is described in the realtime API reference. Subscribing to only one
means typing indicators work on some servers and not others.

**What we do:** subscribe to *both* per room and parse both shapes; emit on both
when the local user types. A server that only understands one ignores the other.

## 4. `subscriptions.get` and `rooms.get` always use the delta envelope

**Status:** confirmed (8.4) · **Severity:** empty room list

Documentation shows a flat `{"subscriptions": [...]}` payload, and describes
`update`/`remove` as the shape returned when `updatedSince` is supplied. On 8.4
the delta envelope is returned *either way*:

```
GET /api/v1/subscriptions.get        → {"update":[…],"remove":[],"success":true}
```

A client reading only `subscriptions` gets nothing and shows an empty sidebar.

**What we do:** read `subscriptions` if present, otherwise `update`.

## 5. A personal access token works as a Meteor resume token

**Status:** confirmed (8.4) · **Severity:** none — this is a useful undocumented
behaviour

REST auth is documented as `X-Auth-Token` + `X-User-Id`, which means a client
asking for a PAT must also ask the user to paste their user id from the admin
UI. Undocumented, `POST /api/v1/login` accepts a PAT in the `resume` field and
responds with the full login payload including `userId`:

```
POST /api/v1/login  {"resume": "<personal access token>"}
→ {"status":"success","data":{"authToken":"…","userId":"…","me":{…}}}
```

**What we do:** PAT login goes through `resume`, so the user pastes one value.

## 6. 2FA challenge and response are inconsistently specified

**Status:** defensive (this server has 2FA disabled for the test account)
· **Severity:** login impossible on 2FA-enforced servers

The TOTP challenge arrives as a 401 whose body nests the error, rather than
using the top-level `errorType` that other endpoints use:

```json
{"success": false, "error": {"error": "totp-required", "reason": "TOTP Required"}}
```

Which field carries the code on the retry has changed across versions: a `code`
member of the login body, an `x-2fa-code` header paired with `x-2fa-method`, or
`X-Auth-Method`. The docs describe the header form only.

**What we do:** parse both the nested and top-level error shapes; on retry send
the code in the body *and* both header spellings.

## 7. Message history has no generic endpoint

**Status:** confirmed (8.4) · **Severity:** design constraint, not a bug

Paginated history is only available per room type — `channels.history`,
`groups.history`, `im.history` — so a client must know a room's `t` before it can
fetch anything. `chat.syncMessages` is the one type-agnostic call, but it needs a
`lastUpdate` cursor and so cannot serve a cold room or page backwards.

**What we do:** dispatch on the room's `t`, falling back to `rooms.info` when we
see an unknown room id. Teams and discussions need no special case: they are
ordinary `c`/`p` rooms.

## 8. Parameter naming is inconsistent across endpoints

**Status:** confirmed (8.4) · **Severity:** wasted debugging time

The room identifier is `roomId` on `channels.history`, `chat.syncMessages` and
`rooms.info`; `rid` on `chat.getThreadsList` and `subscriptions.read`; and
`tmid` identifies a thread on `chat.getThreadMessages` while the same value is
`_id` on the parent message. Sending the wrong spelling yields
`error-invalid-params` rather than a message naming the missing parameter.

## 9. Room kind is not a field

**Status:** confirmed (8.4) · **Severity:** wrong labels and wrong endpoints

Teams and discussions are not distinct room types. Both appear as `c` or `p`, and
their real nature has to be inferred:

| Kind | How to detect |
| --- | --- |
| discussion | `prid` is set (the parent room id) |
| team main room | `teamMain` is true |
| team channel | `teamId` set, `teamMain` false |
| direct message | `t == "d"` |

Order matters: a discussion inside a team has both `prid` and `teamId`.

**What we do:** `model.RoomKind` resolves this once, checking `prid` first.

## 10. Thread replies are hidden from the main timeline by a client convention

**Status:** confirmed (8.4) · **Severity:** duplicated or missing messages

Thread replies are ordinary messages carrying `tmid`. Whether history returns
them inline is not documented, and the answer turned out to be *no* on 8.4:
posting a reply with `tmid` and then fetching `im.history` returned only the
parent. The web client's rule — hide replies from the main timeline unless the
author set `tshow` ("also send to channel") — is therefore already applied
server-side, at least on this endpoint and version.

**What we do:** filter client-side anyway (`tmid = '' OR show_in_parent = 1`).
That is redundant against 8.4 but harmless, and it keeps the timeline correct if
a version or endpoint does return replies inline.

## 11. `teams.createRoom` does not exist; `teams.listRooms` is GET-only

**Status:** confirmed (8.4) · **Severity:** documented feature is unusable

`POST /api/v1/teams.createRoom` is documented as the way to create a channel
inside a team. On 8.4 it answers a **plain-text `404 Not Found`** — an
unregistered route, not a JSON API error.

The distinction matters when probing: this server returns plain-text 404 for
*method/path* combinations it does not serve, and a JSON `{"success": false}`
body for routes that exist but were called wrongly. `POST teams.listRooms` also
404s, while `GET teams.listRooms?teamId=…` returns 200 — the route exists, only
the method differs.

| Call | Result on 8.4 |
| --- | --- |
| `POST teams.createRoom` | plain-text 404 — route absent |
| `POST teams.addRooms` | 400 JSON, route present |
| `POST groups.create` / `channels.create` | 400 JSON, route present |
| `GET teams.listRooms?teamId=` | 200 |
| `GET teams.rooms?teamId=` | plain-text 404 |

**What we do:** create the room with `groups.create`/`channels.create`, then
attach it with `teams.addRooms`. See `rocket.CreateTeamChannel`.

## 12. `subscriptions.unread` rejects the documented per-message form

**Status:** confirmed (8.4) · **Severity:** minor; affects tooling, not the client

The endpoint documents two bodies. Only one works here:

| Body | Result |
| --- | --- |
| `{"roomId": "<rid>"}` | 200, sets `unread: 1`, `alert: true` |
| `{"firstUnreadMessage": {"_id": "<mid>"}}` | 400 `error-action-not-allowed` |

The per-message form was rejected when the target message was the caller's own,
which may be the whole explanation — a user cannot mark unread from their own
message. Untested with a message from someone else.

**What we do:** `rocket.MarkUnread` exposes only the room-level form.

## 13. Unread state has three shapes, and two of them carry no number

**Status:** confirmed (8.4) · **Severity:** invented counts, misplaced dividers

This is the single biggest gap between the documented model and reality. A
subscription's unread state is not "a count"; observed simultaneously on one
account:

| Room | `unread` | `alert` | `ls` |
| --- | --- | --- | --- |
| a | 0 | **true** | **absent entirely** |
| b | 0 | **true** | **2018**-04-20 (7 years stale) |
| c | 1 | true | current |

Three consequences a client has to handle:

1. **`alert: true` with `unread: 0` is normal**, not a contradiction. It is what
   the server sends when unread counters are disabled, and it means "something is
   new here, no count available". Treating `unread == 0` as "nothing new" hides
   the room; treating `alert` as implying a count invents one.
2. **`ls` may be missing.** There is then nothing to anchor an unread divider to.
3. **`ls` may be years old** while the room is perfectly ordinary. Anything
   derived by counting messages after `ls` is then bounded only by how much
   history you happen to have loaded.

Consequence 3 produced a real bug: the status bar read **"60 new messages"** in a
room the server considered fully read, because 60 was simply the page size. The
count was recomputed from the loaded page every time, so paging further back made
the number grow.

**What we do:**

- `Room.HasUnread()` is `unread > 0 || alert`, so alert-only rooms show as unread.
- The divider is anchored to `ls`, frozen at open, and suppressed entirely when
  `ls` is absent.
- The count shown comes from the server's `unread`, captured at open. When the
  server gives no number the UI says "new messages" with no figure rather than
  deriving one. See `Core.unreadAtOpen` and `chatModel.unreadHint`.

## 14. Client and server clocks cannot be assumed to agree

**Status:** confirmed — 94 seconds of skew measured · **Severity:** silently
suppressed unread dividers

Not an API deviation, but an integration hazard the docs never mention. The
development sandbox ran **94 seconds ahead** of the server:

```
local  08:12:49Z    server (Date header)  08:11:15Z
```

Unread state is a comparison between a client-held marker and server-issued
timestamps, so any client that writes `time.Now()` into that marker is comparing
two different clocks. Setting a last-seen marker into the server's future
classifies genuinely new messages as already read, and the failure is invisible:
the divider simply does not appear.

**What we do:** `Core.markRead` anchors the local marker to the newest *server*
timestamp already cached, never to the local clock. With nothing cached it leaves
the marker untouched and lets the authoritative `ls` arrive with the next sync.
The store keeps the later of the two values, so a stale anchor cannot regress it.

## 15. A thread reply does not tell you a thread now exists

**Status:** confirmed (8.4) · **Severity:** threads created while you watch never
appear

Thread membership is recorded on the **parent** message as `tcount`/`tlm`, not on
the reply. A reply pushed over `stream-room-messages` carries only `tmid`, so
receiving it gives a client no way to learn that the parent — already cached with
`tcount: 0` — has become a thread parent. No update to the parent arrived on the
message stream during testing.

The result is that threads started *before* a room is opened show up (the initial
`chat.getThreadsList` sees them), while threads started *while the room is open*
never do. That is exactly how it was reported: "the thread was started after the
application was started and the room was open".

**What we do:** treat a reply as a signal to re-fetch `chat.getThreadsList` for
that room rather than trying to patch the cached parent, since only the server
knows the real count. Refreshes are coalesced (`threadListDebounce`) so a busy
thread cannot cause a request per reply. See `Core.markThreadListDirty`.

**Note:** incrementing the cached parent's `tcount` locally instead is tempting
and wrong — the same message is delivered on both the per-room stream and
`__my_messages__`, so a counter bumped on delivery double-counts.

## 16. A subscription's `_updatedAt` is not activity

**Status:** confirmed (8.4) · **Severity:** the room list rearranges itself under
the user

`_updatedAt` on a subscription is a row-modification timestamp, not a signal of
conversation activity, and the docs never distinguish the two. The server bumps
it for anything that changes the subscription — unread counters, favourite flags,
and **marking a room read**.

Ordering a sidebar by it therefore produces a specific, confusing bug: opening a
room marks it read, the server pushes the subscription back with a fresh
`_updatedAt`, and the room jumps to the top of the list. The user's own click
moves the thing they clicked.

**What we do:** order by activity only — the room's `lm`, and the newest cached
message so that websocket arrivals count before `rooms.get` catches up.
`_updatedAt` is stored but never used for ordering.

**Related, and not an API issue:** sorting unread rooms to the top has the same
effect in reverse. Reading a room clears its unread, so it drops out of the
unread group and moves away from where the user just found it. Unread is now
carried entirely by weight and badges.

---

## Open questions

Things not yet settled against a live server:

- **Which typing stream 8.4 emits.** Both are subscribed and both are accepted
  when sending (the connection survives, no error frame), but no *inbound* typing
  event has been observed: that needs another user typing in a shared room.
- **Whether `chat.getThreadsList` accepts any `type` value on 8.4**, or whether
  the enum is effectively empty. We omit the parameter, so this is academic
  unless a "following only" filter is ever wanted.
- **Divider placement against live data.** Verified live that the divider is
  correctly *suppressed* when every unread message is the reader's own, and that
  unread badges reflect real server state. Confirming placement needs a message
  from another user in the fixture channel.
- **Whether `subscriptions.read` accepts `roomId` as well as `rid`.** We send
  `rid`, which works; the alternative is untested.
- **Whether the per-message `subscriptions.unread` form works for another user's
  message.** Only the own-message case was tried, and it was refused.
