package app_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
	"github.com/geekgonecrazy/rocketchat-tui/internal/store"
)

// harness wires a core to a fake server and records every event it publishes,
// which is the whole contract the UI depends on.
type harness struct {
	t      *testing.T
	server *fakerc.Server
	core   *app.Core
	cache  *store.Store

	mu     sync.Mutex
	events []app.Event
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	server := fakerc.New(t)

	cache, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	session, err := app.Resume(rocket.Credentials{
		ServerURL: server.URL,
		UserID:    fakerc.UserID,
		AuthToken: fakerc.AuthToken,
		Username:  fakerc.Username,
	})
	if err != nil {
		t.Fatalf("app.Resume: %v", err)
	}

	core := app.New(session.Client, cache, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go core.Run(ctx)

	h := &harness{t: t, server: server, core: core, cache: cache}
	go func() {
		for event := range core.Events() {
			h.mu.Lock()
			h.events = append(h.events, event)
			h.mu.Unlock()
		}
	}()
	return h
}

// start boots the session the way the UI does.
func (h *harness) start() {
	h.core.Start(fakerc.UserID, fakerc.Username)
}

// snapshot returns a copy of every event seen so far.
func (h *harness) snapshot() []app.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]app.Event(nil), h.events...)
}

// waitFor polls until check finds what it is looking for, returning the value.
func waitFor[T any](t *testing.T, what string, check func() (T, bool)) T {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if value, ok := check(); ok {
			return value
		}
		time.Sleep(10 * time.Millisecond)
	}
	var zero T
	t.Fatalf("timed out waiting for %s", what)
	return zero
}

// lastRooms returns the most recent sidebar snapshot.
func (h *harness) lastRooms() (app.RoomsUpdated, bool) {
	events := h.snapshot()
	for i := len(events) - 1; i >= 0; i-- {
		if rooms, ok := events[i].(app.RoomsUpdated); ok {
			return rooms, true
		}
	}
	return app.RoomsUpdated{}, false
}

// lastTimeline returns the most recent timeline for a room.
func (h *harness) lastTimeline(roomID string) (app.TimelineUpdated, bool) {
	events := h.snapshot()
	for i := len(events) - 1; i >= 0; i-- {
		if timeline, ok := events[i].(app.TimelineUpdated); ok && timeline.RoomID == roomID {
			return timeline, true
		}
	}
	return app.TimelineUpdated{}, false
}

// lastTyping returns the most recent typing set for a room.
func (h *harness) lastTyping(roomID string) (app.TypingUpdated, bool) {
	events := h.snapshot()
	for i := len(events) - 1; i >= 0; i-- {
		if typing, ok := events[i].(app.TypingUpdated); ok && typing.RoomID == roomID {
			return typing, true
		}
	}
	return app.TypingUpdated{}, false
}

func (h *harness) waitConnected() {
	waitFor(h.t, "realtime connection", func() (bool, bool) {
		for _, event := range h.snapshot() {
			if status, ok := event.(app.StatusChanged); ok && status.Connection == rocket.Connected {
				return true, true
			}
		}
		return false, false
	})
}

// waitForRoomInSidebar blocks until a room has actually synced into the sidebar.
// Tests that open a room must do this first: the core serves the cache before the
// first network sync, so the very first RoomsUpdated can legitimately be empty,
// and opening a room the cache has never heard of exercises a different path.
func (h *harness) waitForRoomInSidebar(roomID string) model.Room {
	return waitFor(h.t, "room "+roomID+" to sync into the sidebar", func() (model.Room, bool) {
		snapshot, ok := h.lastRooms()
		if !ok {
			return model.Room{}, false
		}
		for _, room := range snapshot.Rooms {
			if room.ID == roomID {
				return room, true
			}
		}
		return model.Room{}, false
	})
}

// seedRoom registers a channel with a subscription.
func (h *harness) seedRoom(id, name string, unread, mentions int, lastSeen time.Time) {
	h.server.AddRoom(id, "c", name, nil)
	h.server.AddSubscription(id, "c", name, unread, mentions, lastSeen, nil)
}

func TestStartSyncsRoomsAndUnreadCounts(t *testing.T) {
	h := newHarness(t)
	lastSeen := time.Now().Add(-time.Hour)
	h.seedRoom("room-1", "general", 3, 1, lastSeen)
	h.seedRoom("room-2", "random", 0, 0, lastSeen)

	h.start()

	rooms := waitFor(t, "rooms with unread counts", func() (app.RoomsUpdated, bool) {
		snapshot, ok := h.lastRooms()
		if !ok || len(snapshot.Rooms) < 2 {
			return snapshot, false
		}
		return snapshot, snapshot.Totals.Unread == 3
	})

	if rooms.Totals.Mentions != 1 {
		t.Errorf("mentions = %d, want 1", rooms.Totals.Mentions)
	}
	if rooms.Totals.UnreadRooms != 1 {
		t.Errorf("unread rooms = %d, want 1", rooms.Totals.UnreadRooms)
	}
	// Ordering is by activity, not by unread state: neither room has any messages
	// here, so they fall back to name order and the counters change nothing.
	if rooms.Rooms[0].ID != "room-1" {
		t.Errorf("first room = %s, want room-1 (general before random)", rooms.Rooms[0].ID)
	}
}

func TestOpenRoomLoadsHistoryAndFreezesUnreadDivider(t *testing.T) {
	h := newHarness(t)
	lastSeen := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	h.seedRoom("room-1", "general", 2, 0, lastSeen)

	h.server.AddMessage("old-1", "room-1", "alice", "before you left",
		lastSeen.Add(-10*time.Minute), nil)
	h.server.AddMessage("new-1", "room-1", "bob", "while you were away",
		lastSeen.Add(10*time.Minute), nil)
	h.server.AddMessage("new-2", "room-1", "carol", "and another",
		lastSeen.Add(20*time.Minute), nil)

	h.start()
	h.waitForRoomInSidebar("room-1")

	h.core.OpenRoom("room-1")

	timeline := waitFor(t, "room history", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && len(snapshot.Messages) == 3
	})

	// The divider is anchored to last-seen as it was on open.
	if !timeline.UnreadFrom.Equal(lastSeen) {
		t.Errorf("unread marker = %v, want %v", timeline.UnreadFrom, lastSeen)
	}
	if timeline.UnreadCount != 2 {
		t.Errorf("unread count = %d, want 2", timeline.UnreadCount)
	}

	// Oldest-first ordering is what the renderer expects.
	if timeline.Messages[0].ID != "old-1" || timeline.Messages[2].ID != "new-2" {
		t.Errorf("unexpected message order: %v", messageIDs(timeline.Messages))
	}

	// Opening a room marks it read server-side, but the divider must not move.
	waitFor(t, "room marked read", func() (bool, bool) {
		for _, roomID := range h.server.ReadRooms() {
			if roomID == "room-1" {
				return true, true
			}
		}
		return false, false
	})
	after, _ := h.lastTimeline("room-1")
	if !after.UnreadFrom.Equal(lastSeen) {
		t.Errorf("divider moved after mark-read: %v", after.UnreadFrom)
	}
}

func TestOpenRoomWithNothingUnreadHasNoDivider(t *testing.T) {
	h := newHarness(t)
	lastSeen := time.Now().Add(-time.Hour)
	h.seedRoom("room-1", "general", 0, 0, lastSeen)
	h.server.AddMessage("m1", "room-1", "alice", "hello", lastSeen.Add(-time.Minute), nil)

	h.start()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")

	timeline := waitFor(t, "room history", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && len(snapshot.Messages) == 1
	})
	if !timeline.UnreadFrom.IsZero() {
		t.Errorf("expected no divider, got marker %v", timeline.UnreadFrom)
	}
	if timeline.UnreadCount != 0 {
		t.Errorf("unread count = %d, want 0", timeline.UnreadCount)
	}
}

func TestRealtimeMessageAppearsInOpenRoom(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.server.AddMessage("m1", "room-1", "alice", "first", time.Now().Add(-time.Minute), nil)

	h.start()
	h.waitConnected()
	h.core.OpenRoom("room-1")
	waitFor(t, "initial history", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && len(snapshot.Messages) == 1
	})

	h.server.PushMessage("m2", "room-1", "bob", "pushed live", time.Now(), nil)

	timeline := waitFor(t, "pushed message", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		if !ok {
			return snapshot, false
		}
		for _, msg := range snapshot.Messages {
			if msg.ID == "m2" {
				return snapshot, true
			}
		}
		return snapshot, false
	})
	if last := timeline.Messages[len(timeline.Messages)-1]; last.Text != "pushed live" {
		t.Errorf("last message = %q, want the pushed one", last.Text)
	}
}

func TestRealtimeMessageForOtherRoomUpdatesSidebarOnly(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.seedRoom("room-2", "random", 0, 0, time.Now().Add(-time.Hour))

	h.start()
	h.waitConnected()
	h.core.OpenRoom("room-1")
	waitFor(t, "room-1 open", func() (app.TimelineUpdated, bool) { return h.lastTimeline("room-1") })

	h.server.PushMessage("elsewhere", "room-2", "bob", "over here", time.Now(), nil)
	h.server.PushSubscription("room-2", 1, 1)

	rooms := waitFor(t, "sidebar unread for room-2", func() (app.RoomsUpdated, bool) {
		snapshot, ok := h.lastRooms()
		if !ok {
			return snapshot, false
		}
		for _, room := range snapshot.Rooms {
			if room.ID == "room-2" && room.Unread == 1 && room.Mentions() == 1 {
				return snapshot, true
			}
		}
		return snapshot, false
	})
	if rooms.Totals.Mentions != 1 {
		t.Errorf("totals mentions = %d, want 1", rooms.Totals.Mentions)
	}

	// The open room's timeline must not have absorbed another room's message.
	timeline, _ := h.lastTimeline("room-1")
	for _, msg := range timeline.Messages {
		if msg.ID == "elsewhere" {
			t.Error("a message from another room leaked into the open timeline")
		}
	}
}

func TestTypingIndicatorArrivesAndExpires(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))

	h.start()
	h.waitConnected()
	h.core.OpenRoom("room-1")
	waitFor(t, "room open", func() (app.TimelineUpdated, bool) { return h.lastTimeline("room-1") })

	h.server.PushTyping("room-1", "alice", true)
	typing := waitFor(t, "typing indicator", func() (app.TypingUpdated, bool) {
		snapshot, ok := h.lastTyping("room-1")
		return snapshot, ok && len(snapshot.Users) == 1
	})
	if typing.Users[0] != "alice" {
		t.Errorf("typing user = %q, want alice", typing.Users[0])
	}
	if text := typing.Users.Text(); text != "alice is typing…" {
		t.Errorf("typing text = %q", text)
	}

	h.server.PushTyping("room-1", "bob", true)
	waitFor(t, "two typists", func() (bool, bool) {
		snapshot, ok := h.lastTyping("room-1")
		return true, ok && len(snapshot.Users) == 2
	})

	h.server.PushTyping("room-1", "alice", false)
	waitFor(t, "alice stopped typing", func() (bool, bool) {
		snapshot, ok := h.lastTyping("room-1")
		return true, ok && len(snapshot.Users) == 1 && snapshot.Users[0] == "bob"
	})
}

func TestOwnTypingEchoIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))

	h.start()
	h.waitConnected()
	h.core.OpenRoom("room-1")
	waitFor(t, "room open", func() (app.TimelineUpdated, bool) { return h.lastTimeline("room-1") })

	// The server echoes our own typing back; showing ourselves would be wrong.
	h.server.PushTyping("room-1", fakerc.Username, true)
	h.server.PushTyping("room-1", "alice", true)

	typing := waitFor(t, "typing indicator", func() (app.TypingUpdated, bool) {
		snapshot, ok := h.lastTyping("room-1")
		return snapshot, ok && len(snapshot.Users) > 0
	})
	for _, user := range typing.Users {
		if user == fakerc.Username {
			t.Error("own username appeared in the typing indicator")
		}
	}
}

func TestUserTypingNotifiesServerAndStopsWhenIdle(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))

	h.start()
	h.waitConnected()
	h.core.OpenRoom("room-1")
	waitFor(t, "room open", func() (app.TimelineUpdated, bool) { return h.lastTimeline("room-1") })

	h.core.UserTyping("room-1")
	waitFor(t, "typing announced", func() (bool, bool) {
		for _, notification := range h.server.Notifications() {
			if notification.Typing && notification.Username == fakerc.Username {
				return true, true
			}
		}
		return false, false
	})

	// Repeated keystrokes inside the keepalive window must not spam the server.
	before := len(h.server.Notifications())
	for i := 0; i < 20; i++ {
		h.core.UserTyping("room-1")
	}
	time.Sleep(200 * time.Millisecond)
	if after := len(h.server.Notifications()); after != before {
		t.Errorf("keystroke burst sent %d extra notifications, want 0", after-before)
	}

	h.core.StopTyping("room-1")
	waitFor(t, "typing stop announced", func() (bool, bool) {
		for _, notification := range h.server.Notifications() {
			if !notification.Typing {
				return true, true
			}
		}
		return false, false
	})
}

func TestSendPostsMessageAndShowsIt(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))

	h.start()
	h.waitConnected()
	h.core.OpenRoom("room-1")
	waitFor(t, "room open", func() (app.TimelineUpdated, bool) { return h.lastTimeline("room-1") })

	h.core.Send("room-1", "", "hello from the terminal")

	timeline := waitFor(t, "sent message in timeline", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		if !ok {
			return snapshot, false
		}
		for _, msg := range snapshot.Messages {
			if msg.Text == "hello from the terminal" {
				return snapshot, true
			}
		}
		return snapshot, false
	})

	var found model.Message
	for _, msg := range timeline.Messages {
		if msg.Text == "hello from the terminal" {
			found = msg
		}
	}
	if !found.Own {
		t.Error("our own sent message should be flagged as own")
	}
	if posted := h.server.SentMessages(); len(posted) != 1 || posted[0].RoomID != "room-1" {
		t.Errorf("server recorded %+v", posted)
	}
}

func TestThreadsLoadAndReplyTargetsThread(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-time.Hour)
	h.seedRoom("room-1", "general", 0, 0, base)
	h.server.AddMessage("parent-1", "room-1", "alice", "let's discuss this", base,
		map[string]any{"tcount": 1, "tlm": base.Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)})
	h.server.AddMessage("reply-1", "room-1", "bob", "good idea", base.Add(5*time.Minute),
		map[string]any{"tmid": "parent-1"})

	h.start()
	h.waitConnected()
	h.core.OpenRoom("room-1")

	threads := waitFor(t, "thread list", func() (app.ThreadListUpdated, bool) {
		for _, event := range h.snapshot() {
			if list, ok := event.(app.ThreadListUpdated); ok && len(list.Threads) == 1 {
				return list, true
			}
		}
		return app.ThreadListUpdated{}, false
	})
	if threads.Threads[0].ID != "parent-1" || threads.Threads[0].ThreadCount != 1 {
		t.Errorf("unexpected thread: %+v", threads.Threads[0])
	}

	// The plain reply must stay out of the main timeline.
	timeline, _ := h.lastTimeline("room-1")
	for _, msg := range timeline.Messages {
		if msg.ID == "reply-1" {
			t.Error("thread reply leaked into the main timeline")
		}
	}

	h.core.OpenThread("room-1", "parent-1")
	thread := waitFor(t, "thread replies", func() (app.ThreadUpdated, bool) {
		for _, event := range h.snapshot() {
			if update, ok := event.(app.ThreadUpdated); ok && len(update.Replies) == 1 {
				return update, true
			}
		}
		return app.ThreadUpdated{}, false
	})
	if thread.Parent.ID != "parent-1" || thread.Parent.Text != "let's discuss this" {
		t.Errorf("unexpected thread parent: %+v", thread.Parent)
	}
	if thread.Replies[0].ID != "reply-1" {
		t.Errorf("unexpected reply: %+v", thread.Replies[0])
	}

	h.core.Send("room-1", "parent-1", "my reply")
	waitFor(t, "threaded reply posted", func() (bool, bool) {
		for _, posted := range h.server.SentMessages() {
			if posted.ThreadID == "parent-1" && posted.Text == "my reply" {
				return true, true
			}
		}
		return false, false
	})
}

func TestOpenThreadFetchesParentOutsideCachedHistory(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-72 * time.Hour)
	h.seedRoom("room-1", "general", 0, 0, time.Now())
	// The parent is far older than anything the timeline holds.
	h.server.AddMessage("ancient-parent", "room-1", "alice", "old topic", base, nil)

	h.start()
	h.waitConnected()
	h.core.OpenThread("room-1", "ancient-parent")

	thread := waitFor(t, "thread parent fetched", func() (app.ThreadUpdated, bool) {
		for _, event := range h.snapshot() {
			if update, ok := event.(app.ThreadUpdated); ok && update.Parent.Text == "old topic" {
				return update, true
			}
		}
		return app.ThreadUpdated{}, false
	})
	if thread.Parent.ID != "ancient-parent" {
		t.Errorf("parent = %+v", thread.Parent)
	}
}

func TestLoadOlderPagesHistoryBackwards(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-24 * time.Hour)
	h.seedRoom("room-1", "general", 0, 0, time.Now())

	// More messages than a single page, so paging is actually exercised.
	for i := 0; i < 90; i++ {
		h.server.AddMessage("m"+pad(i), "room-1", "alice", "message "+pad(i),
			base.Add(time.Duration(i)*time.Minute), nil)
	}

	h.start()
	h.waitConnected()
	h.core.OpenRoom("room-1")

	first := waitFor(t, "first page", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && len(snapshot.Messages) >= 60
	})
	if !first.HasMore {
		t.Error("expected more history to be available")
	}
	firstOldest := first.Messages[0].At

	h.core.LoadOlder("room-1")
	waitFor(t, "older page", func() (bool, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		if !ok || len(snapshot.Messages) == 0 {
			return false, false
		}
		return true, snapshot.Messages[0].At.Before(firstOldest)
	})

	oldest, err := h.cache.OldestTimestamp("room-1")
	if err != nil {
		t.Fatalf("OldestTimestamp: %v", err)
	}
	if !oldest.Before(firstOldest) {
		t.Errorf("cache oldest = %v, want older than %v", oldest, firstOldest)
	}
}

func TestCacheServesRoomsBeforeAnyNetworkCall(t *testing.T) {
	dir := t.TempDir()
	server := fakerc.New(t)
	lastSeen := time.Now().Add(-time.Hour)
	server.AddRoom("room-1", "c", "general", nil)
	server.AddSubscription("room-1", "c", "general", 2, 0, lastSeen, nil)

	// First run: populate the cache.
	warm := func() {
		cache, err := store.Open(filepath.Join(dir, "cache.db"))
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		defer cache.Close()

		session, err := app.Resume(rocket.Credentials{
			ServerURL: server.URL, UserID: fakerc.UserID,
			AuthToken: fakerc.AuthToken, Username: fakerc.Username,
		})
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		core := app.New(session.Client, cache, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go core.Run(ctx)

		var mu sync.Mutex
		var seen []app.Event
		go func() {
			for event := range core.Events() {
				mu.Lock()
				seen = append(seen, event)
				mu.Unlock()
			}
		}()
		core.Start(fakerc.UserID, fakerc.Username)

		waitFor(t, "rooms synced to cache", func() (bool, bool) {
			mu.Lock()
			defer mu.Unlock()
			for _, event := range seen {
				if rooms, ok := event.(app.RoomsUpdated); ok && len(rooms.Rooms) == 1 {
					return true, true
				}
			}
			return false, false
		})
	}
	warm()

	// Second run against a dead server: the cache alone must render the sidebar.
	server.Close()

	cache, err := store.Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer cache.Close()

	rooms, err := cache.Rooms()
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if len(rooms) != 1 || rooms[0].DisplayName != "general" {
		t.Fatalf("cache did not survive the restart: %+v", rooms)
	}
	if rooms[0].Unread != 2 {
		t.Errorf("cached unread = %d, want 2", rooms[0].Unread)
	}
}

func TestExpiredSessionIsReported(t *testing.T) {
	server := fakerc.New(t)
	cache, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer cache.Close()

	// A token the fake server rejects stands in for an expired session.
	session, err := app.Resume(rocket.Credentials{
		ServerURL: server.URL, UserID: fakerc.UserID,
		AuthToken: "expired-token", Username: fakerc.Username,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	core := app.New(session.Client, cache, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go core.Run(ctx)

	var mu sync.Mutex
	var seen []app.Event
	go func() {
		for event := range core.Events() {
			mu.Lock()
			seen = append(seen, event)
			mu.Unlock()
		}
	}()
	core.Start(fakerc.UserID, fakerc.Username)

	waitFor(t, "session invalid event", func() (bool, bool) {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range seen {
			if _, ok := event.(app.SessionInvalid); ok {
				return true, true
			}
		}
		return false, false
	})
}

func TestRoomKindsSurviveTheRoundTrip(t *testing.T) {
	h := newHarness(t)
	lastSeen := time.Now().Add(-time.Hour)

	h.server.AddRoom("team-1", "c", "engineering", map[string]any{"teamMain": true, "teamId": "team-1"})
	h.server.AddSubscription("team-1", "c", "engineering", 0, 0, lastSeen,
		map[string]any{"teamMain": true, "teamId": "team-1"})

	h.server.AddRoom("disc-1", "p", "spike", map[string]any{"prid": "team-1"})
	h.server.AddSubscription("disc-1", "p", "spike", 0, 0, lastSeen, map[string]any{"prid": "team-1"})

	h.server.AddRoom("dm-1", "d", "alice", nil)
	h.server.AddSubscription("dm-1", "d", "alice", 0, 0, lastSeen, nil)

	h.server.AddRoom("priv-1", "p", "secret", nil)
	h.server.AddSubscription("priv-1", "p", "secret", 0, 0, lastSeen, nil)

	h.start()

	rooms := waitFor(t, "all four room kinds", func() (app.RoomsUpdated, bool) {
		snapshot, ok := h.lastRooms()
		return snapshot, ok && len(snapshot.Rooms) == 4
	})

	want := map[string]model.Kind{
		"team-1": model.KindTeam,
		"disc-1": model.KindDiscussion,
		"dm-1":   model.KindDirect,
		"priv-1": model.KindPrivate,
	}
	for _, room := range rooms.Rooms {
		if room.Kind != want[room.ID] {
			t.Errorf("%s kind = %v, want %v", room.ID, room.Kind, want[room.ID])
		}
	}
}

func messageIDs(messages []model.Message) []string {
	out := make([]string, len(messages))
	for i, msg := range messages {
		out[i] = msg.ID
	}
	return out
}

// pad renders i as a fixed-width string so ids sort lexicographically.
func pad(i int) string {
	digits := []byte{byte('0' + i/100%10), byte('0' + i/10%10), byte('0' + i%10)}
	return string(digits)
}

func TestMarkReadAnchorsToServerTimeNotTheLocalClock(t *testing.T) {
	h := newHarness(t)
	// A room whose newest message is well in the past, standing in for a client
	// clock that runs ahead of the server.
	serverNewest := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Millisecond)
	h.seedRoom("room-1", "general", 2, 0, serverNewest.Add(-time.Hour))
	h.server.AddMessage("m1", "room-1", "alice", "newest server message", serverNewest, nil)

	h.start()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")

	waitFor(t, "room marked read", func() (bool, bool) {
		for _, roomID := range h.server.ReadRooms() {
			if roomID == "room-1" {
				return true, true
			}
		}
		return false, false
	})

	// Give the mark-read write time to land, then assert the marker was never
	// pushed past the newest thing the server has told us about: messages
	// arriving inside the skew window must not look already-read.
	time.Sleep(300 * time.Millisecond)
	stored, err := h.cache.LastSeen("room-1")
	if err != nil {
		t.Fatalf("LastSeen: %v", err)
	}
	if stored.After(serverNewest) {
		t.Errorf("last-seen = %v, want no later than the newest server message %v",
			stored, serverNewest)
	}

	// The unread counters must still have been cleared, so the sidebar responds
	// immediately even though the marker did not move.
	rooms := waitFor(t, "unread counters to clear", func() (app.RoomsUpdated, bool) {
		snapshot, ok := h.lastRooms()
		if !ok {
			return snapshot, false
		}
		for _, room := range snapshot.Rooms {
			if room.ID == "room-1" && room.Unread == 0 && !room.Alert {
				return snapshot, true
			}
		}
		return snapshot, false
	})
	_ = rooms
}

// A room can be flagged unread with no count at all: Rocket.Chat sets
// alert=true with unread=0 whenever unread counters are off. Observed on a real
// server, and never produced by the fixtures until this test.
func TestAlertWithoutCountStillDrawsDividerButClaimsNoNumber(t *testing.T) {
	h := newHarness(t)
	lastSeen := time.Now().Add(-90 * 24 * time.Hour).UTC().Truncate(time.Millisecond)

	h.server.AddRoom("room-1", "c", "general", nil)
	h.server.AddSubscription("room-1", "c", "general", 0, 0, lastSeen,
		map[string]any{"alert": true})
	h.server.AddMessage("old", "room-1", "alice", "long ago",
		lastSeen.Add(-time.Hour), nil)
	h.server.AddMessage("new", "room-1", "bob", "since you last looked",
		lastSeen.Add(time.Hour), nil)

	h.start()
	room := h.waitForRoomInSidebar("room-1")
	if !room.HasUnread() {
		t.Error("a room with alert=true must count as unread even with no counter")
	}

	h.core.OpenRoom("room-1")
	timeline := waitFor(t, "the timeline with a settled divider", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && len(snapshot.Messages) == 2 && !snapshot.UnreadFrom.IsZero()
	})

	// The divider is still positioned, because last-seen is known.
	if !timeline.UnreadFrom.Equal(lastSeen) {
		t.Errorf("divider anchor = %v, want %v", timeline.UnreadFrom, lastSeen)
	}
	// But no count is claimed: the server did not provide one, and deriving it
	// from the loaded page would just report the page size.
	if timeline.UnreadCount != 0 {
		t.Errorf("unread count = %d, want 0 when the server reports no counter",
			timeline.UnreadCount)
	}
}

// A subscription may have no last-seen marker at all (never opened in any
// client). There is nothing to anchor a divider to, so none is drawn.
func TestAlertWithoutLastSeenDrawsNoDivider(t *testing.T) {
	h := newHarness(t)
	h.server.AddRoom("room-1", "c", "general", nil)
	h.server.AddSubscription("room-1", "c", "general", 0, 0, time.Time{},
		map[string]any{"alert": true})
	h.server.AddMessage("m1", "room-1", "alice", "hello",
		time.Now().Add(-time.Hour), nil)

	h.start()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")

	timeline := waitFor(t, "the timeline", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && len(snapshot.Messages) == 1
	})
	if !timeline.UnreadFrom.IsZero() {
		t.Errorf("expected no divider anchor, got %v", timeline.UnreadFrom)
	}
}

// A thread started while the room is already open must appear in the thread
// list. The parent message is already cached with tcount=0, so nothing about the
// reply arriving updates it locally — the list has to be refreshed from the
// server.
func TestThreadStartedAfterRoomIsOpenAppearsInTheList(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-time.Hour)
	h.seedRoom("room-1", "general", 0, 0, base)
	h.server.AddMessage("parent-1", "room-1", "alice", "a normal message", base, nil)

	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")
	waitFor(t, "the timeline", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && len(snapshot.Messages) == 1
	})

	// No threads yet.
	if threads, err := h.cache.ThreadParents("room-1", 10); err != nil {
		t.Fatalf("ThreadParents: %v", err)
	} else if len(threads) != 0 {
		t.Fatalf("expected no threads yet, got %d", len(threads))
	}

	// Someone replies in a thread on that message while we are watching.
	h.server.AddMessage("reply-1", "room-1", "bob", "starting a thread",
		time.Now(), map[string]any{"tmid": "parent-1"})
	h.server.PromoteToThreadParent("parent-1", 1, time.Now())
	h.server.PushMessage("reply-1", "room-1", "bob", "starting a thread", time.Now(),
		map[string]any{"tmid": "parent-1"})

	waitFor(t, "the new thread to appear in the list", func() (bool, bool) {
		events := h.snapshot()
		for i := len(events) - 1; i >= 0; i-- {
			list, ok := events[i].(app.ThreadListUpdated)
			if !ok || list.RoomID != "room-1" {
				continue
			}
			for _, thread := range list.Threads {
				if thread.ID == "parent-1" {
					return true, true
				}
			}
		}
		return false, false
	})
}

func TestOpenRoomPublishesMentionCandidates(t *testing.T) {
	h := newHarness(t)
	lastSeen := time.Now().Add(-time.Hour)
	h.seedRoom("room-1", "general", 0, 0, lastSeen)
	// dana is in the room but has never spoken; erin has spoken and, on a real
	// server, might have since left — both should be offerable.
	h.server.AddMembers("room-1", "dana", "tester")
	h.server.AddMessage("m1", "room-1", "erin", "morning", lastSeen.Add(time.Minute), nil)

	h.start()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")

	members := waitFor(t, "mention candidates", func() (app.MembersUpdated, bool) {
		events := h.snapshot()
		for i := len(events) - 1; i >= 0; i-- {
			update, ok := events[i].(app.MembersUpdated)
			if !ok || update.RoomID != "room-1" {
				continue
			}
			if len(update.Members) >= 2 {
				return update, true
			}
		}
		return app.MembersUpdated{}, false
	})

	var names []string
	for _, member := range members.Members {
		names = append(names, member.Username)
	}
	for _, want := range []string{"dana", "erin"} {
		if !containsString(names, want) {
			t.Errorf("candidates %v missing %q", names, want)
		}
	}
	// Mentioning yourself is never what you meant.
	if containsString(names, fakerc.Username) {
		t.Errorf("candidates %v include the logged-in user", names)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// roomInSidebar returns a room from the latest sidebar snapshot.
func (h *harness) roomInSidebar(roomID string) (model.Room, bool) {
	snapshot, ok := h.lastRooms()
	if !ok {
		return model.Room{}, false
	}
	for _, room := range snapshot.Rooms {
		if room.ID == roomID {
			return room, true
		}
	}
	return model.Room{}, false
}

// sidebarShowedUnread reports whether any sidebar snapshot ever had the room
// unread, which is how a state that was later undone can still be asserted on.
func (h *harness) sidebarShowedUnread(roomID string) bool {
	for _, event := range h.snapshot() {
		snapshot, ok := event.(app.RoomsUpdated)
		if !ok {
			continue
		}
		for _, room := range snapshot.Rooms {
			if room.ID == roomID && room.HasUnread() {
				return true
			}
		}
	}
	return false
}

// lastNotice returns the most recent notice the core published.
func (h *harness) lastNotice() (app.Notice, bool) {
	events := h.snapshot()
	for i := len(events) - 1; i >= 0; i-- {
		if notice, ok := events[i].(app.Notice); ok {
			return notice, true
		}
	}
	return app.Notice{}, false
}

// openReadRoom seeds a three-message room from other people, opens it, and waits
// until it has been read — the state every mark-unread test starts from.
func (h *harness) openReadRoom(base time.Time) {
	h.t.Helper()
	h.seedRoom("room-1", "general", 0, 0, base.Add(-time.Hour))
	h.server.AddMessage("m1", "room-1", "alice", "first", base, nil)
	h.server.AddMessage("m2", "room-1", "bob", "second", base.Add(time.Minute), nil)
	h.server.AddMessage("m3", "room-1", "carol", "third", base.Add(2*time.Minute), nil)

	h.start()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")
	waitFor(h.t, "room history", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && len(snapshot.Messages) == 3
	})
}

func TestMarkUnreadFromMessageMovesTheDividerAboveIt(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	h.openReadRoom(base)

	h.core.MarkUnreadFrom("room-1", "m2")

	// The divider anchors to m1, so m2 and m3 are the new messages.
	timeline := waitFor(t, "divider above m2", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && snapshot.UnreadFrom.Equal(base)
	})
	if timeline.UnreadCount != 2 {
		t.Errorf("unread count = %d, want 2 (m2 and m3)", timeline.UnreadCount)
	}

	// The sidebar has to say so too, otherwise the room is only unread for as
	// long as it stays open.
	room := waitFor(t, "sidebar to show the room unread", func() (model.Room, bool) {
		room, ok := h.roomInSidebar("room-1")
		return room, ok && room.HasUnread()
	})
	if room.Unread != 2 {
		t.Errorf("sidebar unread = %d, want 2", room.Unread)
	}

	mark := waitFor(t, "the server to be told", func() (fakerc.UnreadMark, bool) {
		marks := h.server.UnreadMarks()
		if len(marks) == 0 {
			return fakerc.UnreadMark{}, false
		}
		return marks[0], true
	})
	if mark.MessageID != "m2" {
		t.Errorf("server mark = %+v, want the per-message form for m2", mark)
	}
}

func TestMarkUnreadRoomLevelFlagsTheLastMessage(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	h.openReadRoom(base)

	h.core.MarkUnread("room-1")

	// The room-level form makes the last message new, which is what the server
	// does with it (unread: 1, alert: true).
	room := waitFor(t, "sidebar to show the room unread", func() (model.Room, bool) {
		room, ok := h.roomInSidebar("room-1")
		return room, ok && room.HasUnread()
	})
	if room.Unread != 1 {
		t.Errorf("sidebar unread = %d, want 1", room.Unread)
	}
	timeline, _ := h.lastTimeline("room-1")
	if !timeline.UnreadFrom.Equal(base.Add(time.Minute)) {
		t.Errorf("divider = %v, want it above m3 at %v", timeline.UnreadFrom, base.Add(time.Minute))
	}

	mark := waitFor(t, "the server to be told", func() (fakerc.UnreadMark, bool) {
		marks := h.server.UnreadMarks()
		if len(marks) == 0 {
			return fakerc.UnreadMark{}, false
		}
		return marks[0], true
	})
	if mark.RoomID != "room-1" || mark.MessageID != "" {
		t.Errorf("server mark = %+v, want the room-level form", mark)
	}
}

func TestMarkUnreadRefusesYourOwnMessage(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	h.seedRoom("room-1", "general", 0, 0, base.Add(-time.Hour))
	h.server.AddMessage("m1", "room-1", "alice", "first", base, nil)
	h.server.AddMessage("mine", "room-1", fakerc.Username, "my own words", base.Add(time.Minute),
		map[string]any{"u": map[string]any{"_id": fakerc.UserID, "username": fakerc.Username}})

	h.start()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")
	waitFor(t, "room history", func() (app.TimelineUpdated, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		return snapshot, ok && len(snapshot.Messages) == 2
	})

	h.core.MarkUnreadFrom("room-1", "mine")

	// The server refuses this outright, so it is refused here with a reason
	// instead of being sent and reported as a bare 400.
	notice := waitFor(t, "the refusal", func() (app.Notice, bool) {
		notice, ok := h.lastNotice()
		return notice, ok && strings.Contains(notice.Text, "own message")
	})
	if notice.IsErr {
		t.Errorf("notice %q is flagged as an error; it is a refusal, not a failure", notice.Text)
	}
	if marks := h.server.UnreadMarks(); len(marks) != 0 {
		t.Errorf("server was called anyway: %+v", marks)
	}
	if room, ok := h.roomInSidebar("room-1"); ok && room.HasUnread() {
		t.Errorf("room went unread despite the refusal: %+v", room)
	}
}

func TestMarkUnreadIsRolledBackWhenTheServerRefuses(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	h.server.RejectUnread = true
	h.openReadRoom(base)

	h.core.MarkUnreadFrom("room-1", "m2")

	// The rollback is queued before the failure is reported, so the sidebar has
	// settled by the time the notice appears — no sleep needed, and no race with
	// the optimistic write this is meant to be undoing.
	notice := waitFor(t, "the failure to be reported", func() (app.Notice, bool) {
		notice, ok := h.lastNotice()
		return notice, ok && notice.IsErr
	})
	if !strings.Contains(notice.Text, "not-allowed") {
		t.Errorf("notice = %q, want the server's refusal", notice.Text)
	}

	// The optimistic write must actually have happened, or this proves nothing.
	if !h.sidebarShowedUnread("room-1") {
		t.Fatal("the room never went unread, so there was nothing to roll back")
	}

	// An unread badge the server does not have would otherwise survive until the
	// next full sync, claiming there is something to read when there is not.
	if room, ok := h.roomInSidebar("room-1"); !ok || room.HasUnread() {
		t.Errorf("unread survived the refusal: %+v", room)
	}
	if timeline, ok := h.lastTimeline("room-1"); ok && !timeline.UnreadFrom.IsZero() {
		t.Errorf("divider survived the refusal: %v", timeline.UnreadFrom)
	}
}

func TestMarkedUnreadRoomStaysUnreadWhenAMessageArrives(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	h.openReadRoom(base)
	h.waitConnected()

	h.core.MarkUnreadFrom("room-1", "m2")
	waitFor(t, "the room to go unread", func() (bool, bool) {
		room, ok := h.roomInSidebar("room-1")
		return true, ok && room.HasUnread()
	})
	readsBefore := len(h.server.ReadRooms())

	// Traffic in the open room normally marks it read again. A room the user
	// deliberately marked unread has to survive that, or the feature lasts only
	// until the next reply.
	h.server.PushMessage("m4", "room-1", "bob", "and another", time.Now(), nil)
	waitFor(t, "the pushed message", func() (bool, bool) {
		snapshot, ok := h.lastTimeline("room-1")
		if !ok {
			return false, false
		}
		for _, msg := range snapshot.Messages {
			if msg.ID == "m4" {
				return true, true
			}
		}
		return false, false
	})

	time.Sleep(200 * time.Millisecond)
	if reads := h.server.ReadRooms(); len(reads) != readsBefore {
		t.Errorf("room was marked read again: %v", reads)
	}
	if room, ok := h.roomInSidebar("room-1"); !ok || !room.HasUnread() {
		t.Errorf("room stopped being unread: %+v", room)
	}
}

func TestMarkUnreadWorksOnARoomWithNoCachedHistory(t *testing.T) {
	h := newHarness(t)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	// Never opened, so the cache holds the subscription and nothing else — the
	// normal state of most of the sidebar on a cold start.
	h.seedRoom("room-1", "general", 0, 0, base)
	h.server.AddMessage("m1", "room-1", "alice", "first", base.Add(time.Minute), nil)

	h.start()
	h.waitForRoomInSidebar("room-1")

	h.core.MarkUnread("room-1")

	// With nothing to anchor to the room still alerts; the server knows its own
	// last message, and the count arrives with the next sync.
	room := waitFor(t, "sidebar to show the room unread", func() (model.Room, bool) {
		room, ok := h.roomInSidebar("room-1")
		return room, ok && room.HasUnread()
	})
	if !room.Alert {
		t.Errorf("room = %+v, want it alerting", room)
	}
	// The marker must not have been invented: an ls in the wrong place puts the
	// divider in the wrong place for the whole of the next visit.
	if !room.LastSeenAt.Equal(base) {
		t.Errorf("last seen = %v, want it untouched at %v", room.LastSeenAt, base)
	}
	waitFor(t, "the server to be told", func() (bool, bool) {
		for _, mark := range h.server.UnreadMarks() {
			if mark.RoomID == "room-1" {
				return true, true
			}
		}
		return false, false
	})
}
