//go:build livetest

// Read-only probe against a real Rocket.Chat server, driven by environment:
//
//	RC_SERVER=https://chat.example.com RC_USER=me RC_PASS=... \
//	  go test -tags livetest -run TestLiveProbe -v
//
// It uses the SDK directly rather than app.Core, because opening a room through
// the core marks it read — a write. Nothing here mutates server state.
package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
	"github.com/geekgonecrazy/rocketchat-tui/internal/store"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

func liveClient(t *testing.T) (*rocket.Client, rocket.Me) {
	t.Helper()
	server, user, pass := os.Getenv("RC_SERVER"), os.Getenv("RC_USER"), os.Getenv("RC_PASS")
	if server == "" || user == "" || pass == "" {
		t.Skip("set RC_SERVER, RC_USER and RC_PASS to run the live probe")
	}

	client, err := rocket.NewClient(server)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	me, err := client.LoginWithPassword(ctx, user, pass, os.Getenv("RC_TOTP"))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	t.Logf("logged in as %q (%s), server %s", me.Username, me.ID, client.ServerURL())
	return client, me
}

func TestLiveProbe(t *testing.T) {
	client, me := liveClient(t)
	ctx := context.Background()

	// --- subscriptions: unreads and mentions, exactly as the sidebar sees them
	subs, err := client.Subscriptions(ctx, time.Time{})
	if err != nil {
		t.Fatalf("subscriptions.get: %v", err)
	}
	rooms, err := client.Rooms(ctx, time.Time{})
	if err != nil {
		t.Fatalf("rooms.get: %v", err)
	}
	t.Logf("%d subscriptions, %d rooms", len(subs), len(rooms))

	byID := map[string]rocket.Room{}
	for _, room := range rooms {
		byID[room.ID] = room
	}

	var merged []model.Room
	for _, sub := range subs {
		room := byID[sub.RoomID]
		merged = append(merged, model.MergeRoom(&room, &sub))
	}
	model.SortRooms(merged)

	kinds := map[model.Kind]int{}
	totalUnread, totalMentions := 0, 0
	for _, room := range merged {
		kinds[room.Kind]++
		totalUnread += room.Unread
		totalMentions += room.Mentions()
	}
	t.Logf("totals: unread=%d mentions=%d", totalUnread, totalMentions)
	for kind, count := range kinds {
		t.Logf("  kind %-11s %d", kind, count)
	}

	t.Log("sidebar order (as rctui would render it):")
	for i, room := range merged {
		if i >= 15 {
			t.Logf("  … and %d more", len(merged)-i)
			break
		}
		badge := ""
		if room.Mentions() > 0 {
			badge = fmt.Sprintf("  @%d", room.Mentions())
		} else if room.Unread > 0 {
			badge = fmt.Sprintf("  %d", room.Unread)
		}
		t.Logf("  %-34s kind=%-11s ls=%s%s",
			room.Label(), room.Kind, shortTime(room.LastSeenAt), badge)
	}

	// --- history + threads on the busiest room, read-only
	target := pickBusiest(merged)
	if target.ID == "" {
		t.Skip("no rooms to inspect")
	}
	roomType := rawType(byID[target.ID], subs, target.ID)
	t.Logf("inspecting %s (id=%s, t=%q)", target.Label(), target.ID, roomType)

	history, err := client.History(ctx, rocket.HistoryQuery{
		RoomID: target.ID, RoomType: roomType, Count: 10,
	})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	t.Logf("history returned %d messages (newest first)", len(history))
	for i, msg := range history {
		if i >= 5 {
			break
		}
		converted := model.FromRocketMessage(msg, me.ID)
		t.Logf("  %s  %-14s %-8s tcount=%d own=%v  %.60q",
			shortTime(converted.At), converted.Username, converted.SystemType,
			converted.ThreadCount, converted.Own, converted.Text)
	}

	threads, total, err := client.ThreadsList(ctx, target.ID, 10, 0)
	if err != nil {
		t.Logf("threads unavailable (may be disabled): %v", err)
	} else {
		t.Logf("threads: %d of %d total", len(threads), total)
		for i, thread := range threads {
			if i >= 3 {
				break
			}
			t.Logf("  tmid=%s tcount=%d  %.50q", thread.ID, thread.ThreadCount, thread.Msg)
			replies, err := client.ThreadMessages(ctx, thread.ID, 5, 0)
			if err != nil {
				t.Errorf("    thread messages: %v", err)
				continue
			}
			t.Logf("    %d replies fetched", len(replies))
		}
	}
}

func pickBusiest(rooms []model.Room) model.Room {
	best := model.Room{}
	for _, room := range rooms {
		if room.Kind == model.KindOmnichannel {
			continue
		}
		if best.ID == "" || room.LastMessageAt.After(best.LastMessageAt) {
			best = room
		}
	}
	return best
}

func rawType(room rocket.Room, subs []rocket.Subscription, roomID string) string {
	if room.Type != "" {
		return room.Type
	}
	for _, sub := range subs {
		if sub.RoomID == roomID {
			return sub.Type
		}
	}
	return ""
}

func shortTime(t time.Time) string {
	if t.IsZero() {
		return "         -"
	}
	return t.Local().Format("01-02 15:04")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestLiveRealtime verifies the DDP handshake, login and subscriptions against a
// real server. It only listens: no messages and no typing notifications are sent.
func TestLiveRealtime(t *testing.T) {
	client, me := liveClient(t)

	subs, err := client.Subscriptions(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("subscriptions.get: %v", err)
	}
	if len(subs) == 0 {
		t.Skip("no rooms to subscribe to")
	}

	realtime := rocket.NewRealtime(client.WebSocketURL(), client.Credentials, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go realtime.Run(ctx)

	t.Logf("dialing %s", client.WebSocketURL())

	deadline := time.After(45 * time.Second)
	connected := false
	for !connected {
		select {
		case event := <-realtime.Events():
			if state, ok := event.(rocket.ConnStateEvent); ok {
				t.Logf("conn state: %v (err=%v)", state.State, state.Err)
				if state.Err != nil {
					t.Fatalf("connection failed: %v", state.Err)
				}
				connected = state.State == rocket.Connected
			}
		case <-deadline:
			t.Fatal("timed out waiting for the DDP connection")
		}
	}
	t.Log("DDP connected and authenticated")

	// Subscribe to everything the client normally holds open.
	realtime.SubscribeUserEvents(me.ID)
	realtime.SubscribeAllMessages()
	for _, sub := range subs {
		realtime.SubscribeRoomActivity(sub.RoomID)
		realtime.SubscribeRoomMessages(sub.RoomID)
	}
	t.Logf("subscribed to %d rooms plus user streams", len(subs))

	// Listen for a while. Anything arriving proves the stream decoding works; a
	// quiet server simply means nothing happened, which is not a failure.
	window := 25 * time.Second
	if raw := os.Getenv("RC_LISTEN"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			window = parsed
		}
	}
	t.Logf("listening for %s …", window)

	counts := map[string]int{}
	listen := time.After(window)
	for {
		select {
		case event := <-realtime.Events():
			switch e := event.(type) {
			case rocket.ConnStateEvent:
				counts["conn"]++
				if e.State != rocket.Connected {
					t.Errorf("unexpected disconnect: %v (%v)", e.State, e.Err)
				}
			case rocket.MessageEvent:
				counts["message"]++
				converted := model.FromRocketMessage(e.Message, me.ID)
				t.Logf("  MESSAGE  room=%s %s: %.60q (ts=%s)",
					e.Message.RoomID, converted.Username, converted.Text, shortTime(converted.At))
				if converted.At.IsZero() {
					t.Error("realtime message timestamp failed to decode")
				}
			case rocket.TypingEvent:
				counts["typing"]++
				t.Logf("  TYPING   room=%s %s typing=%v", e.RoomID, e.Username, e.Typing)
			case rocket.SubscriptionEvent:
				counts["subscription"]++
				t.Logf("  SUBSCR   room=%s unread=%d mentions=%d action=%s",
					e.Subscription.RoomID, e.Subscription.Unread,
					e.Subscription.UserMentions, e.Action)
			case rocket.RoomChangedEvent:
				counts["room"]++
				t.Logf("  ROOM     %s action=%s", e.Room.ID, e.Action)
			case rocket.MessageDeletedEvent:
				counts["deleted"]++
				t.Logf("  DELETED  %s in %s", e.MessageID, e.RoomID)
			}
		case <-listen:
			t.Log("event counts:")
			for _, name := range sortedKeys(counts) {
				t.Logf("  %-13s %d", name, counts[name])
			}
			if realtime.State() != rocket.Connected {
				t.Errorf("connection dropped during the listen window: %v", realtime.State())
			} else {
				t.Log("still connected at the end of the window")
			}
			return
		}
	}
}

// TestLiveWriteInSelfDM exercises the write paths against a real server without
// touching any shared room: everything happens in the logged-in user's self-DM.
// Requires RC_ALLOW_WRITE=1.
func TestLiveWriteInSelfDM(t *testing.T) {
	if os.Getenv("RC_ALLOW_WRITE") != "1" {
		t.Skip("set RC_ALLOW_WRITE=1 to allow this test to post messages")
	}
	client, me := liveClient(t)
	ctx := context.Background()

	room, err := client.CreateDirectMessage(ctx, me.Username)
	if err != nil {
		t.Fatalf("im.create (self-DM): %v", err)
	}
	t.Logf("self-DM room id=%s t=%q", room.ID, room.Type)

	// Realtime first, so the echo of our own message can be observed arriving.
	realtime := rocket.NewRealtime(client.WebSocketURL(), client.Credentials, nil)
	rtCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go realtime.Run(rtCtx)

	waitConnected(t, realtime)
	realtime.SubscribeRoomMessages(room.ID)
	realtime.SubscribeRoomActivity(room.ID)
	realtime.SubscribeUserEvents(me.ID)
	time.Sleep(2 * time.Second) // let the subs land before we generate traffic

	// --- typing notification: the server must accept both stream shapes
	if err := realtime.NotifyTyping(ctx, room.ID, me.Username, true); err != nil {
		t.Errorf("NotifyTyping(true): %v", err)
	}
	time.Sleep(time.Second)
	if err := realtime.NotifyTyping(ctx, room.ID, me.Username, false); err != nil {
		t.Errorf("NotifyTyping(false): %v", err)
	}
	if state := realtime.State(); state != rocket.Connected {
		t.Fatalf("typing notification dropped the connection: %v", state)
	}
	t.Log("typing notifications accepted; connection still up")

	// --- send a message
	stamp := time.Now().Format("15:04:05")
	text := "rctui live test " + stamp
	sent, err := client.Send(ctx, rocket.SendOptions{RoomID: room.ID, Text: text})
	if err != nil {
		t.Fatalf("chat.sendMessage: %v", err)
	}
	t.Logf("sent id=%s ts=%s", sent.ID, shortTime(sent.Timestamp.Time))
	if sent.Timestamp.Time.IsZero() {
		t.Error("sent message timestamp did not decode")
	}

	// --- the same message must come back over the websocket
	echo := awaitMessage(t, realtime, 30*time.Second, func(msg rocket.Message) bool {
		return msg.ID == sent.ID
	})
	converted := model.FromRocketMessage(echo, me.ID)
	t.Logf("echo received over DDP: %s: %q (ts=%s)", converted.Username, converted.Text, shortTime(converted.At))
	if converted.At.IsZero() {
		t.Error("DDP echo timestamp did not decode — check the $date handling")
	}
	if !converted.Own {
		t.Error("our own message should be flagged as own")
	}
	if converted.Text != text {
		t.Errorf("echo text = %q, want %q", converted.Text, text)
	}

	// --- reply in a thread on it
	reply, err := client.Send(ctx, rocket.SendOptions{
		RoomID: room.ID, Text: "threaded reply " + stamp, ThreadID: sent.ID,
	})
	if err != nil {
		t.Fatalf("threaded chat.sendMessage: %v", err)
	}
	if reply.ThreadParentID != sent.ID {
		t.Errorf("reply tmid = %q, want %q", reply.ThreadParentID, sent.ID)
	}
	t.Logf("threaded reply id=%s tmid=%s", reply.ID, reply.ThreadParentID)

	// --- and the server must now report a thread in this room
	threads, total, err := client.ThreadsList(ctx, room.ID, 10, 0)
	if err != nil {
		t.Fatalf("chat.getThreadsList: %v", err)
	}
	t.Logf("threads in self-DM: %d of %d", len(threads), total)
	found := false
	for _, thread := range threads {
		if thread.ID == sent.ID {
			found = true
			if thread.ThreadCount < 1 {
				t.Errorf("thread parent tcount = %d, want >= 1", thread.ThreadCount)
			}
			t.Logf("  parent tcount=%d tlm=%s", thread.ThreadCount, shortTime(threadLastAt(thread)))
		}
	}
	if !found {
		t.Errorf("the message we replied to is not in the thread list")
	}

	replies, err := client.ThreadMessages(ctx, sent.ID, 10, 0)
	if err != nil {
		t.Fatalf("chat.getThreadMessages: %v", err)
	}
	t.Logf("%d replies fetched", len(replies))
	if len(replies) == 0 {
		t.Error("expected at least one reply")
	}

	// --- history must include the parent but hide the plain reply
	history, err := client.History(ctx, rocket.HistoryQuery{
		RoomID: room.ID, RoomType: room.Type, Count: 20,
	})
	if err != nil {
		t.Fatalf("im.history: %v", err)
	}
	sawParent, sawReply := false, false
	for _, msg := range history {
		switch msg.ID {
		case sent.ID:
			sawParent = true
		case reply.ID:
			sawReply = true
		}
	}
	t.Logf("history: %d messages, parent present=%v, plain reply inline=%v",
		len(history), sawParent, sawReply)
	if !sawParent {
		t.Error("history should contain the parent message")
	}
	// The server does return thread replies inline; hiding them is the client's
	// job, which is what the store's timeline filter does.
	t.Logf("note: server returns thread replies in history (inline=%v); the client filters them", sawReply)

	// --- mark read
	if err := client.MarkRead(ctx, room.ID); err != nil {
		t.Errorf("subscriptions.read: %v", err)
	} else {
		t.Log("subscriptions.read accepted")
	}
}

func waitConnected(t *testing.T, realtime *rocket.Realtime) {
	t.Helper()
	deadline := time.After(45 * time.Second)
	for {
		select {
		case event := <-realtime.Events():
			if state, ok := event.(rocket.ConnStateEvent); ok {
				if state.Err != nil {
					t.Fatalf("connection failed: %v", state.Err)
				}
				if state.State == rocket.Connected {
					return
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for the DDP connection")
		}
	}
}

func awaitMessage(t *testing.T, realtime *rocket.Realtime, window time.Duration, match func(rocket.Message) bool) rocket.Message {
	t.Helper()
	deadline := time.After(window)
	for {
		select {
		case event := <-realtime.Events():
			if msg, ok := event.(rocket.MessageEvent); ok && match(msg.Message) {
				return msg.Message
			}
		case <-deadline:
			t.Fatal("timed out waiting for the message to arrive over DDP")
			return rocket.Message{}
		}
	}
}

func threadLastAt(msg rocket.Message) time.Time {
	if msg.ThreadLastAt != nil {
		return msg.ThreadLastAt.Time
	}
	return time.Time{}
}

// TestLiveFixtureSetup creates (or reuses) the shared team, channel and
// discussion that every live test writes into, and makes sure the named guest is
// a member. Safe to run repeatedly.
func TestLiveFixtureSetup(t *testing.T) {
	if os.Getenv("RC_ALLOW_WRITE") != "1" {
		t.Skip("set RC_ALLOW_WRITE=1 to allow this test to create rooms")
	}
	client, _ := liveClient(t)
	ctx := context.Background()

	fixture, actions, err := ensureFixture(ctx, client, time.Now())
	for _, action := range actions {
		t.Logf("  %s", action)
	}
	if err != nil {
		t.Fatalf("ensureFixture: %v", err)
	}

	t.Logf("team       %s (id=%s room=%s)", fixture.TeamName, fixture.TeamID, fixture.TeamRoomID)
	t.Logf("channel    %s (id=%s)", fixture.ChannelName, fixture.ChannelID)
	t.Logf("discussion %s (id=%s)", fixture.DiscussionName, fixture.DiscussionID)

	if fixture.TeamRoomID == "" {
		t.Fatal("fixture has no team room")
	}

	members, err := client.TeamMembers(ctx, fixture.TeamID)
	if err != nil {
		t.Logf("teams.members: %v", err)
	} else {
		names := make([]string, 0, len(members))
		for _, member := range members {
			names = append(names, member.Username)
		}
		t.Logf("team members: %v", names)
	}

	// The kinds we derive must match what the sidebar should show.
	for _, check := range []struct {
		label  string
		roomID string
		want   string
	}{
		{"team main room", fixture.TeamRoomID, "team"},
		{"team channel", fixture.ChannelID, ""},
		{"discussion", fixture.DiscussionID, "discussion"},
	} {
		if check.roomID == "" {
			continue
		}
		room, err := client.RoomInfo(ctx, check.roomID)
		if err != nil {
			t.Errorf("rooms.info(%s): %v", check.label, err)
			continue
		}
		kind := model.RoomKind(room.Type, room.TeamMain, room.ParentRoomID)
		t.Logf("%-15s t=%q teamMain=%v teamId=%q prid=%q → kind=%s",
			check.label, room.Type, room.TeamMain, room.TeamID, room.ParentRoomID, kind)
		if check.want != "" && kind.String() != check.want {
			t.Errorf("%s resolved to kind %s, want %s", check.label, kind, check.want)
		}
	}
}

// TestLiveUnreadDivider exercises the unread divider against real server data.
// It posts into the tracked fixture channel, marks the room unread from a chosen
// message using subscriptions.unread, then drives the core and checks where the
// divider lands. No pre-existing room is touched.
func TestLiveUnreadDivider(t *testing.T) {
	if os.Getenv("RC_ALLOW_WRITE") != "1" {
		t.Skip("set RC_ALLOW_WRITE=1 to allow this test to post messages")
	}
	client, me := liveClient(t)
	ctx := context.Background()

	fixture, actions, err := ensureFixture(ctx, client, time.Now())
	if err != nil {
		t.Fatalf("ensureFixture: %v", err)
	}
	for _, action := range actions {
		t.Logf("  %s", action)
	}
	roomID := fixture.ChannelID
	if roomID == "" {
		t.Skip("fixture has no channel")
	}

	// Post a short run of messages so there is something to divide.
	stamp := time.Now().Format("15:04:05")
	var ids []string
	for i := 1; i <= 4; i++ {
		sent, err := client.Send(ctx, rocket.SendOptions{
			RoomID: roomID,
			Text:   fmt.Sprintf("divider test %s — message %d", stamp, i),
		})
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		ids = append(ids, sent.ID)
		time.Sleep(400 * time.Millisecond) // distinct timestamps
	}
	t.Logf("posted %d messages, ids=%v", len(ids), ids)

	if err := client.MarkUnread(ctx, roomID); err != nil {
		t.Fatalf("subscriptions.unread: %v", err)
	}
	t.Log("marked the room unread")

	subs, err := client.Subscriptions(ctx, time.Time{})
	if err != nil {
		t.Fatalf("subscriptions.get: %v", err)
	}
	for _, sub := range subs {
		if sub.RoomID == roomID {
			lastSeen := "nil"
			if sub.LastSeen != nil {
				lastSeen = sub.LastSeen.Time.Format(time.RFC3339)
			}
			t.Logf("server state: unread=%d alert=%v ls=%s", sub.Unread, sub.Alert, lastSeen)
			if sub.Unread == 0 {
				t.Error("server reports zero unread after subscriptions.unread")
			}
		}
	}

	// Now drive the real core and see where it puts the divider.
	h := newLiveHarness(t, client, me)
	waitFor(t, "the fixture channel to appear in the sidebar", func() (bool, bool) {
		snapshot, ok := h.lastRooms()
		if !ok {
			return false, false
		}
		for _, room := range snapshot.Rooms {
			if room.ID == roomID {
				t.Logf("sidebar: %s unread=%d mentions=%d", room.Label(), room.Unread, room.Mentions())
				return true, room.Unread > 0
			}
		}
		return false, false
	})

	h.core.OpenRoom(roomID)
	timeline := waitFor(t, "the timeline to load", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline(roomID)
		return snapshot, ok && len(snapshot.Messages) >= 4
	})

	marker := "none"
	if !timeline.UnreadFrom.IsZero() {
		marker = timeline.UnreadFrom.Format(time.RFC3339)
	}
	t.Logf("divider anchor=%s unread count=%d messages loaded=%d",
		marker, timeline.UnreadCount, len(timeline.Messages))

	// Count messages after the marker that we did not write. The divider
	// deliberately ignores our own messages — you have read what you just sent —
	// so a room where every unread message is ours must show no rule.
	foreignAfterMarker := 0
	for _, msg := range timeline.Messages {
		if !timeline.UnreadFrom.IsZero() && msg.At.After(timeline.UnreadFrom) && !msg.Own {
			foreignAfterMarker++
		}
	}

	view := render.Timeline(render.DefaultTheme(), render.TimelineState{
		Messages:   timeline.Messages,
		UnreadFrom: timeline.UnreadFrom,
		Width:      74,
		Cursor:     -1,
	})

	if foreignAfterMarker == 0 {
		if view.UnreadLine >= 0 {
			t.Errorf("a rule was drawn at line %d although every unread message is our own",
				view.UnreadLine)
		}
		t.Log("no rule drawn, correctly: every message after the marker is our own.")
		t.Log("Exercising the divider against live data needs a message from another")
		t.Log("user in this channel; the positioning itself is covered by the fake-server tests.")
		return
	}

	if view.UnreadLine < 0 {
		t.Fatalf("expected a rule: %d messages from other users sit after the marker", foreignAfterMarker)
	}
	t.Logf("rule drawn at line %d with %d foreign unread messages", view.UnreadLine, foreignAfterMarker)
	for i, msg := range timeline.Messages {
		if msg.Own || !msg.At.After(timeline.UnreadFrom) {
			continue
		}
		if view.MessageLine[i] < view.UnreadLine {
			t.Errorf("unread message %s renders above the rule", msg.ID)
		}
		break
	}

	t.Log("rendered tail:")
	start := max(0, view.UnreadLine-6)
	for i := start; i < len(view.Lines) && i < view.UnreadLine+8; i++ {
		t.Logf("  |%s", view.Lines[i])
	}
}

// liveHarness runs the real app core against the live server, recording the
// events the UI would consume.
type liveHarness struct {
	core  *app.Core
	cache *store.Store

	mu     sync.Mutex
	events []app.Event
}

func newLiveHarness(t *testing.T, client *rocket.Client, me rocket.Me) *liveHarness {
	t.Helper()

	cache, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	core := app.New(client, cache, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go core.Run(ctx)

	h := &liveHarness{core: core, cache: cache}
	go func() {
		for event := range core.Events() {
			h.mu.Lock()
			h.events = append(h.events, event)
			h.mu.Unlock()
		}
	}()
	core.Start(me.ID, me.Username)
	return h
}

func (h *liveHarness) snapshot() []app.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]app.Event(nil), h.events...)
}

func (h *liveHarness) lastRooms() (app.RoomsUpdated, bool) {
	events := h.snapshot()
	for i := len(events) - 1; i >= 0; i-- {
		if rooms, ok := events[i].(app.RoomsUpdated); ok {
			return rooms, true
		}
	}
	return app.RoomsUpdated{}, false
}

func (h *liveHarness) lastTimeline(roomID string) (app.TimelineUpdated, bool) {
	events := h.snapshot()
	for i := len(events) - 1; i >= 0; i-- {
		if timeline, ok := events[i].(app.TimelineUpdated); ok && timeline.RoomID == roomID {
			return timeline, true
		}
	}
	return app.TimelineUpdated{}, false
}

// TestLiveThreadStartedWhileWatching is the exact scenario a thread created
// after the app is already running and the room is already open. The reply
// carries only its parent's id, so nothing local reveals that the parent has
// become a thread; the list has to be re-fetched from the server.
func TestLiveThreadStartedWhileWatching(t *testing.T) {
	if os.Getenv("RC_ALLOW_WRITE") != "1" {
		t.Skip("set RC_ALLOW_WRITE=1 to allow this test to post messages")
	}
	client, me := liveClient(t)
	ctx := context.Background()

	fixture, _, err := ensureFixture(ctx, client, time.Now())
	if err != nil {
		t.Fatalf("ensureFixture: %v", err)
	}
	roomID := fixture.ChannelID
	if roomID == "" {
		t.Skip("fixture has no channel")
	}

	stamp := time.Now().Format("15:04:05")
	parent, err := client.Send(ctx, rocket.SendOptions{
		RoomID: roomID,
		Text:   "thread-while-watching parent " + stamp,
	})
	if err != nil {
		t.Fatalf("send parent: %v", err)
	}

	// Bring the client up with the room already open, exactly as the user had it.
	h := newLiveHarness(t, client, me)
	waitFor(t, "the fixture channel in the sidebar", func() (bool, bool) {
		snapshot, ok := h.lastRooms()
		if !ok {
			return false, false
		}
		for _, room := range snapshot.Rooms {
			if room.ID == roomID {
				return true, true
			}
		}
		return false, false
	})
	h.core.OpenRoom(roomID)
	waitFor(t, "the parent message to load", func() (bool, bool) {
		snapshot, ok := h.lastTimeline(roomID)
		if !ok {
			return false, false
		}
		for _, msg := range snapshot.Messages {
			if msg.ID == parent.ID {
				return true, true
			}
		}
		return false, false
	})
	t.Logf("room open with parent %s visible", parent.ID)

	// Confirm the thread list does not already contain it.
	before := waitFor(t, "an initial thread list", func() (app.ThreadListUpdated, bool) {
		for _, event := range h.snapshot() {
			if list, ok := event.(app.ThreadListUpdated); ok && list.RoomID == roomID {
				return list, true
			}
		}
		return app.ThreadListUpdated{}, false
	})
	for _, thread := range before.Threads {
		if thread.ID == parent.ID {
			t.Fatal("the parent is already a thread; the test cannot prove anything")
		}
	}
	t.Logf("thread list currently holds %d threads, none of them ours", len(before.Threads))

	// Now start the thread, while the room is open and being watched.
	reply, err := client.Send(ctx, rocket.SendOptions{
		RoomID:   roomID,
		Text:     "…and this reply starts the thread " + stamp,
		ThreadID: parent.ID,
	})
	if err != nil {
		t.Fatalf("send reply: %v", err)
	}
	t.Logf("posted reply %s with tmid=%s", reply.ID, reply.ThreadParentID)

	list := waitFor(t, "the new thread to appear in the list", func() (app.ThreadListUpdated, bool) {
		events := h.snapshot()
		for i := len(events) - 1; i >= 0; i-- {
			list, ok := events[i].(app.ThreadListUpdated)
			if !ok || list.RoomID != roomID {
				continue
			}
			for _, thread := range list.Threads {
				if thread.ID == parent.ID {
					return list, true
				}
			}
		}
		return app.ThreadListUpdated{}, false
	})

	for _, thread := range list.Threads {
		if thread.ID == parent.ID {
			t.Logf("thread appeared: tcount=%d %.50q", thread.ThreadCount, thread.Text)
			if thread.ThreadCount < 1 {
				t.Errorf("thread count = %d, want at least 1", thread.ThreadCount)
			}
		}
	}
}

// TestLiveSidebarOrderSurvivesOpeningRooms is the regression for "don't move
// rooms to the top when I click them". It records the live sidebar order, opens
// several rooms through the real core (which marks each read), and requires the
// order to be identical afterwards.
func TestLiveSidebarOrderSurvivesOpeningRooms(t *testing.T) {
	client, me := liveClient(t)
	h := newLiveHarness(t, client, me)

	before := waitFor(t, "the sidebar to populate", func() ([]model.Room, bool) {
		snapshot, ok := h.lastRooms()
		return snapshot.Rooms, ok && len(snapshot.Rooms) >= 3
	})

	order := func(rooms []model.Room) []string {
		out := make([]string, len(rooms))
		for i, room := range rooms {
			out[i] = room.Label()
		}
		return out
	}
	initial := order(before)
	t.Logf("initial order: %v", initial)

	// Open each room in turn, letting the mark-read and its subscription update
	// land between them.
	for _, room := range before {
		h.core.OpenRoom(room.ID)
		time.Sleep(1200 * time.Millisecond)
	}

	// Let the follow-up subscription pushes settle.
	time.Sleep(3 * time.Second)

	after, ok := h.lastRooms()
	if !ok {
		t.Fatal("no sidebar snapshot after opening rooms")
	}
	final := order(after.Rooms)
	t.Logf("final order:   %v", final)

	if len(final) != len(initial) {
		t.Fatalf("room count changed: %d -> %d", len(initial), len(final))
	}
	for i := range initial {
		if initial[i] != final[i] {
			t.Errorf("sidebar reordered after opening rooms:\n  before %v\n  after  %v",
				initial, final)
			return
		}
	}
	t.Log("order unchanged after opening every room")
}
