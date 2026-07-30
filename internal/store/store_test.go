package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
	"github.com/geekgonecrazy/rocketchat-tui/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.SetIdentity("self", "me")
	return s
}

func ts(at time.Time) rocket.Timestamp { return rocket.NewTimestamp(at) }

func tsPtr(at time.Time) *rocket.Timestamp {
	stamp := rocket.NewTimestamp(at)
	return &stamp
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cache.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.SetMeta("k", "v"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening must find the existing schema and data, not re-migrate over it.
	second, err := store.Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()

	value, err := second.Meta("k")
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if value != "v" {
		t.Errorf("meta = %q, want v", value)
	}
}

func TestMetaTimeRoundTrip(t *testing.T) {
	s := openStore(t)
	want := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.SetMetaTime("cursor", want); err != nil {
		t.Fatalf("SetMetaTime: %v", err)
	}
	got, err := s.MetaTime("cursor")
	if err != nil {
		t.Fatalf("MetaTime: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// A missing key is "never synced", not an error.
	missing, err := s.MetaTime("absent")
	if err != nil || !missing.IsZero() {
		t.Errorf("absent cursor = %v, %v", missing, err)
	}
}

func TestRoomsMergeSubscriptionAndRoomMetadata(t *testing.T) {
	s := openStore(t)
	lastSeen := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	lastMessage := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)

	if err := s.SaveRooms([]rocket.Room{{
		ID: "r1", Type: "c", Name: "general", DisplayName: "general",
		Topic: "chatter", UserCount: 12, LastMessage: tsPtr(lastMessage),
		UpdatedAt: ts(lastMessage),
	}}); err != nil {
		t.Fatalf("SaveRooms: %v", err)
	}
	if err := s.SaveSubscriptions([]rocket.Subscription{{
		ID: "s1", RoomID: "r1", Type: "c", Name: "general", Open: true,
		Alert: true, Unread: 4, UserMentions: 2, LastSeen: tsPtr(lastSeen),
		UpdatedAt: ts(lastMessage),
	}}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}

	rooms, err := s.Rooms()
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("got %d rooms, want 1", len(rooms))
	}

	room := rooms[0]
	if room.Topic != "chatter" || room.UserCount != 12 {
		t.Errorf("room metadata missing: %+v", room)
	}
	if room.Unread != 4 || room.UserMentions != 2 || !room.Alert {
		t.Errorf("subscription state missing: %+v", room)
	}
	if !room.LastSeenAt.Equal(lastSeen) {
		t.Errorf("last seen = %v, want %v", room.LastSeenAt, lastSeen)
	}
	if room.Kind != model.KindChannel {
		t.Errorf("kind = %v, want channel", room.Kind)
	}
	if room.Label() != "# general" {
		t.Errorf("label = %q", room.Label())
	}
}

func TestRoomKindResolution(t *testing.T) {
	s := openStore(t)

	rooms := []rocket.Room{
		{ID: "team", Type: "c", Name: "eng", TeamMain: true},
		{ID: "disc", Type: "p", Name: "spike", ParentRoomID: "team"},
		{ID: "dm", Type: "d", Name: "alice"},
		{ID: "priv", Type: "p", Name: "secret"},
	}
	if err := s.SaveRooms(rooms); err != nil {
		t.Fatalf("SaveRooms: %v", err)
	}
	for _, room := range rooms {
		if err := s.SaveSubscriptions([]rocket.Subscription{{
			RoomID: room.ID, Type: room.Type, Name: room.Name, Open: true,
			TeamMain: room.TeamMain, ParentRoomID: room.ParentRoomID,
		}}); err != nil {
			t.Fatalf("SaveSubscriptions: %v", err)
		}
	}

	want := map[string]model.Kind{
		"team": model.KindTeam,
		"disc": model.KindDiscussion,
		"dm":   model.KindDirect,
		"priv": model.KindPrivate,
	}
	list, err := s.Rooms()
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if len(list) != len(want) {
		t.Fatalf("got %d rooms, want %d", len(list), len(want))
	}
	for _, room := range list {
		if room.Kind != want[room.ID] {
			t.Errorf("%s kind = %v, want %v", room.ID, room.Kind, want[room.ID])
		}
	}
}

func TestRoomTypeLookupPrefersRoomThenSubscription(t *testing.T) {
	s := openStore(t)
	if err := s.SaveSubscriptions([]rocket.Subscription{{RoomID: "r1", Type: "p", Open: true}}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}

	roomType, err := s.RoomType("r1")
	if err != nil {
		t.Fatalf("RoomType: %v", err)
	}
	if roomType != "p" {
		t.Errorf("room type = %q, want p", roomType)
	}

	// An unknown room reports empty rather than failing, so callers can fetch it.
	roomType, err = s.RoomType("nope")
	if err != nil {
		t.Fatalf("RoomType(unknown): %v", err)
	}
	if roomType != "" {
		t.Errorf("unknown room type = %q, want empty", roomType)
	}
}

func TestSaveMessagesUpsertsAndOrders(t *testing.T) {
	s := openStore(t)
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)

	messages := []rocket.Message{
		{ID: "m2", RoomID: "r1", Msg: "second", Timestamp: ts(base.Add(time.Minute)),
			User: rocket.User{ID: "u1", Username: "alice"}},
		{ID: "m1", RoomID: "r1", Msg: "first", Timestamp: ts(base),
			User: rocket.User{ID: "u1", Username: "alice", Name: "Alice"}},
	}
	if err := s.SaveMessages(messages); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	// Re-saving the same ids must update rather than duplicate: realtime and REST
	// both deliver the same message.
	messages[0].Msg = "second (edited)"
	if err := s.SaveMessages(messages); err != nil {
		t.Fatalf("SaveMessages again: %v", err)
	}

	timeline, err := s.RoomTimeline("r1", 10)
	if err != nil {
		t.Fatalf("RoomTimeline: %v", err)
	}
	if len(timeline) != 2 {
		t.Fatalf("got %d messages, want 2", len(timeline))
	}
	if timeline[0].ID != "m1" || timeline[1].ID != "m2" {
		t.Errorf("expected oldest-first order, got %s then %s", timeline[0].ID, timeline[1].ID)
	}
	if timeline[1].Text != "second (edited)" {
		t.Errorf("upsert did not apply: %q", timeline[1].Text)
	}
	if timeline[0].Author != "Alice" {
		t.Errorf("author = %q, want Alice", timeline[0].Author)
	}
}

func TestOwnMessagesAreFlagged(t *testing.T) {
	s := openStore(t)
	if err := s.SaveMessages([]rocket.Message{
		{ID: "mine", RoomID: "r1", Msg: "hi", Timestamp: ts(time.Now()),
			User: rocket.User{ID: "self", Username: "me"}},
		{ID: "theirs", RoomID: "r1", Msg: "hey", Timestamp: ts(time.Now()),
			User: rocket.User{ID: "other", Username: "them"}},
	}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	timeline, err := s.RoomTimeline("r1", 10)
	if err != nil {
		t.Fatalf("RoomTimeline: %v", err)
	}
	for _, msg := range timeline {
		wantOwn := msg.ID == "mine"
		if msg.Own != wantOwn {
			t.Errorf("%s own = %v, want %v", msg.ID, msg.Own, wantOwn)
		}
	}
}

func TestTimelineHidesThreadRepliesUnlessMirrored(t *testing.T) {
	s := openStore(t)
	base := time.Now().Add(-time.Hour)

	if err := s.SaveMessages([]rocket.Message{
		{ID: "parent", RoomID: "r1", Msg: "topic", Timestamp: ts(base), ThreadCount: 2,
			ThreadLastAt: tsPtr(base.Add(2 * time.Minute)), User: rocket.User{Username: "alice"}},
		{ID: "reply-hidden", RoomID: "r1", Msg: "in thread", Timestamp: ts(base.Add(time.Minute)),
			ThreadParentID: "parent", User: rocket.User{Username: "bob"}},
		{ID: "reply-shown", RoomID: "r1", Msg: "also to channel",
			Timestamp: ts(base.Add(2 * time.Minute)), ThreadParentID: "parent",
			ShowInParent: true, User: rocket.User{Username: "carol"}},
	}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	timeline, err := s.RoomTimeline("r1", 10)
	if err != nil {
		t.Fatalf("RoomTimeline: %v", err)
	}
	got := map[string]bool{}
	for _, msg := range timeline {
		got[msg.ID] = true
	}
	if !got["parent"] || !got["reply-shown"] {
		t.Errorf("timeline should include the parent and the mirrored reply: %v", got)
	}
	if got["reply-hidden"] {
		t.Error("timeline must hide plain thread replies")
	}

	replies, err := s.ThreadReplies("parent")
	if err != nil {
		t.Fatalf("ThreadReplies: %v", err)
	}
	if len(replies) != 2 {
		t.Fatalf("got %d replies, want 2", len(replies))
	}
	if replies[0].ID != "reply-hidden" {
		t.Errorf("replies should be oldest-first, got %s first", replies[0].ID)
	}

	parents, err := s.ThreadParents("r1", 10)
	if err != nil {
		t.Fatalf("ThreadParents: %v", err)
	}
	if len(parents) != 1 || parents[0].ID != "parent" {
		t.Errorf("unexpected thread parents: %+v", parents)
	}
}

func TestTimelinePagingAndBounds(t *testing.T) {
	s := openStore(t)
	base := time.Now().Add(-10 * time.Hour).UTC().Truncate(time.Millisecond)

	var messages []rocket.Message
	for i := 0; i < 10; i++ {
		messages = append(messages, rocket.Message{
			ID:        "m" + string(rune('a'+i)),
			RoomID:    "r1",
			Msg:       "text",
			Timestamp: ts(base.Add(time.Duration(i) * time.Minute)),
			User:      rocket.User{Username: "alice"},
		})
	}
	if err := s.SaveMessages(messages); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	newest, err := s.NewestTimestamp("r1")
	if err != nil {
		t.Fatalf("NewestTimestamp: %v", err)
	}
	if !newest.Equal(base.Add(9 * time.Minute)) {
		t.Errorf("newest = %v", newest)
	}
	oldest, err := s.OldestTimestamp("r1")
	if err != nil {
		t.Fatalf("OldestTimestamp: %v", err)
	}
	if !oldest.Equal(base) {
		t.Errorf("oldest = %v", oldest)
	}

	// RoomTimeline returns the newest page, oldest-first within it.
	page, err := s.RoomTimeline("r1", 4)
	if err != nil {
		t.Fatalf("RoomTimeline: %v", err)
	}
	if len(page) != 4 || page[3].ID != "mj" {
		t.Fatalf("unexpected newest page: %+v", ids(page))
	}

	older, err := s.RoomTimelineBefore("r1", page[0].At, 3)
	if err != nil {
		t.Fatalf("RoomTimelineBefore: %v", err)
	}
	if len(older) != 3 {
		t.Fatalf("got %d older messages, want 3", len(older))
	}
	for _, msg := range older {
		if !msg.At.Before(page[0].At) {
			t.Errorf("%s is not older than the page start", msg.ID)
		}
	}

	// An empty room yields zero times rather than an error.
	if empty, err := s.NewestTimestamp("nope"); err != nil || !empty.IsZero() {
		t.Errorf("empty room newest = %v, %v", empty, err)
	}
}

func TestUnreadAccountingAndDivider(t *testing.T) {
	s := openStore(t)
	lastSeen := time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Millisecond)

	if err := s.SaveSubscriptions([]rocket.Subscription{{
		RoomID: "r1", Type: "c", Name: "general", Open: true,
		Unread: 2, UserMentions: 1, Alert: true, LastSeen: tsPtr(lastSeen),
	}}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}
	if err := s.SaveMessages([]rocket.Message{
		{ID: "old", RoomID: "r1", Msg: "before", Timestamp: ts(lastSeen.Add(-time.Minute)),
			User: rocket.User{Username: "alice"}},
		{ID: "new1", RoomID: "r1", Msg: "after", Timestamp: ts(lastSeen.Add(time.Minute)),
			User: rocket.User{Username: "alice"}},
		{ID: "new2", RoomID: "r1", Msg: "after too", Timestamp: ts(lastSeen.Add(2 * time.Minute)),
			User: rocket.User{Username: "bob"}},
	}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	count, err := s.CountAfter("r1", lastSeen)
	if err != nil {
		t.Fatalf("CountAfter: %v", err)
	}
	if count != 2 {
		t.Errorf("unread after last-seen = %d, want 2", count)
	}

	stored, err := s.LastSeen("r1")
	if err != nil {
		t.Fatalf("LastSeen: %v", err)
	}
	if !stored.Equal(lastSeen) {
		t.Errorf("last seen = %v, want %v", stored, lastSeen)
	}

	readAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.ClearUnread("r1", readAt); err != nil {
		t.Fatalf("ClearUnread: %v", err)
	}
	rooms, err := s.Rooms()
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if rooms[0].Unread != 0 || rooms[0].Mentions() != 0 || rooms[0].Alert {
		t.Errorf("unread state not cleared: %+v", rooms[0])
	}
	if !rooms[0].LastSeenAt.Equal(readAt) {
		t.Errorf("last seen after clear = %v, want %v", rooms[0].LastSeenAt, readAt)
	}
}

func TestLastSeenNeverMovesBackwards(t *testing.T) {
	s := openStore(t)
	recent := time.Now().UTC().Truncate(time.Millisecond)
	older := recent.Add(-time.Hour)

	if err := s.SaveSubscriptions([]rocket.Subscription{
		{RoomID: "r1", Open: true, LastSeen: tsPtr(recent)},
	}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}
	// A stale delta from the server must not resurrect an old divider position.
	if err := s.SaveSubscriptions([]rocket.Subscription{
		{RoomID: "r1", Open: true, LastSeen: tsPtr(older)},
	}); err != nil {
		t.Fatalf("SaveSubscriptions (stale): %v", err)
	}

	got, err := s.LastSeen("r1")
	if err != nil {
		t.Fatalf("LastSeen: %v", err)
	}
	if !got.Equal(recent) {
		t.Errorf("last seen = %v, want %v", got, recent)
	}
}

func TestDeleteMessageAndSubscription(t *testing.T) {
	s := openStore(t)
	if err := s.SaveMessages([]rocket.Message{
		{ID: "m1", RoomID: "r1", Msg: "bye", Timestamp: ts(time.Now()),
			User: rocket.User{Username: "alice"}},
	}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}
	if err := s.SaveSubscriptions([]rocket.Subscription{{RoomID: "r1", Open: true}}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}

	if err := s.DeleteMessage("m1"); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if _, found, err := s.Message("m1"); err != nil || found {
		t.Errorf("message still present (found=%v, err=%v)", found, err)
	}

	if err := s.DeleteSubscription("r1"); err != nil {
		t.Fatalf("DeleteSubscription: %v", err)
	}
	rooms, err := s.Rooms()
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if len(rooms) != 0 {
		t.Errorf("expected no rooms, got %d", len(rooms))
	}
}

func TestHistoryStateWidensBounds(t *testing.T) {
	s := openStore(t)
	mid := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.SaveHistoryState("r1", store.HistoryState{
		OldestTS: mid, NewestTS: mid, SyncedAt: mid,
	}); err != nil {
		t.Fatalf("SaveHistoryState: %v", err)
	}
	// A page of older history should widen the window, not replace it.
	if err := s.SaveHistoryState("r1", store.HistoryState{
		OldestTS: mid.Add(-time.Hour), NewestTS: mid.Add(-time.Hour),
		ReachedEnd: true, SyncedAt: mid,
	}); err != nil {
		t.Fatalf("SaveHistoryState (older): %v", err)
	}

	state, err := s.HistoryState("r1")
	if err != nil {
		t.Fatalf("HistoryState: %v", err)
	}
	if !state.OldestTS.Equal(mid.Add(-time.Hour)) {
		t.Errorf("oldest = %v", state.OldestTS)
	}
	if !state.NewestTS.Equal(mid) {
		t.Errorf("newest = %v, want %v", state.NewestTS, mid)
	}
	if !state.ReachedEnd {
		t.Error("reached-end should stick once set")
	}
}

func TestResetClearsEverything(t *testing.T) {
	s := openStore(t)
	if err := s.SaveSubscriptions([]rocket.Subscription{{RoomID: "r1", Open: true}}); err != nil {
		t.Fatalf("SaveSubscriptions: %v", err)
	}
	if err := s.SaveMessages([]rocket.Message{
		{ID: "m1", RoomID: "r1", Timestamp: ts(time.Now())},
	}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}
	if err := s.SetMeta("account", "server#user"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	if err := s.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	rooms, err := s.Rooms()
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if len(rooms) != 0 {
		t.Errorf("expected no rooms after reset, got %d", len(rooms))
	}
	if account, err := s.Meta("account"); err != nil || account != "" {
		t.Errorf("meta after reset = %q, %v", account, err)
	}
}

func ids(messages []model.Message) []string {
	out := make([]string, len(messages))
	for i, msg := range messages {
		out[i] = msg.ID
	}
	return out
}

// Clicking a room marks it read, which bumps the subscription's _updatedAt.
// That must not be mistaken for activity: the sidebar has to stay put so the
// user's mental map of where rooms sit survives opening one.
func TestOpeningARoomDoesNotReorderTheSidebar(t *testing.T) {
	s := openStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Three rooms with clearly separated message activity.
	for i, spec := range []struct {
		id       string
		name     string
		activity time.Time
	}{
		{"busy", "busy", now.Add(-1 * time.Minute)},
		{"middle", "middle", now.Add(-1 * time.Hour)},
		{"quiet", "quiet", now.Add(-24 * time.Hour)},
	} {
		if err := s.SaveRooms([]rocket.Room{{
			ID: spec.id, Type: "c", Name: spec.name,
			LastMessage: tsPtr(spec.activity), UpdatedAt: ts(spec.activity),
		}}); err != nil {
			t.Fatalf("SaveRooms: %v", err)
		}
		if err := s.SaveSubscriptions([]rocket.Subscription{{
			RoomID: spec.id, Type: "c", Name: spec.name, Open: true,
			UpdatedAt: ts(spec.activity),
		}}); err != nil {
			t.Fatalf("SaveSubscriptions: %v", err)
		}
		_ = i
	}

	assertOrder := func(context string, want ...string) {
		t.Helper()
		rooms, err := s.Rooms()
		if err != nil {
			t.Fatalf("Rooms: %v", err)
		}
		var got []string
		for _, room := range rooms {
			got = append(got, room.ID)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", context, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: order = %v, want %v", context, got, want)
				return
			}
		}
	}

	assertOrder("initially", "busy", "middle", "quiet")

	// Open the quietest room. The server responds by bumping that subscription's
	// _updatedAt, which is what arrives on the next sync.
	if err := s.ClearUnread("quiet", now); err != nil {
		t.Fatalf("ClearUnread: %v", err)
	}
	if err := s.SaveSubscriptions([]rocket.Subscription{{
		RoomID: "quiet", Type: "c", Name: "quiet", Open: true,
		UpdatedAt: ts(now), LastSeen: tsPtr(now),
	}}); err != nil {
		t.Fatalf("SaveSubscriptions after read: %v", err)
	}

	assertOrder("after opening the quiet room", "busy", "middle", "quiet")
}

// Real activity, on the other hand, must reorder — including a message that has
// only arrived over the websocket and is not yet reflected in rooms.get.
func TestNewMessageMovesARoomUp(t *testing.T) {
	s := openStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	for _, spec := range []struct {
		id       string
		activity time.Time
	}{
		{"busy", now.Add(-1 * time.Minute)},
		{"quiet", now.Add(-24 * time.Hour)},
	} {
		if err := s.SaveRooms([]rocket.Room{{
			ID: spec.id, Type: "c", Name: spec.id, LastMessage: tsPtr(spec.activity),
		}}); err != nil {
			t.Fatalf("SaveRooms: %v", err)
		}
		if err := s.SaveSubscriptions([]rocket.Subscription{{
			RoomID: spec.id, Type: "c", Name: spec.id, Open: true,
			UpdatedAt: ts(spec.activity),
		}}); err != nil {
			t.Fatalf("SaveSubscriptions: %v", err)
		}
	}

	rooms, err := s.Rooms()
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if rooms[0].ID != "busy" {
		t.Fatalf("expected busy first, got %s", rooms[0].ID)
	}

	// A message lands in the quiet room. rooms.get has not caught up yet.
	if err := s.SaveMessages([]rocket.Message{{
		ID: "m1", RoomID: "quiet", Msg: "something new",
		Timestamp: ts(now), User: rocket.User{ID: "u1", Username: "alice"},
	}}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	rooms, err = s.Rooms()
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if rooms[0].ID != "quiet" {
		var got []string
		for _, room := range rooms {
			got = append(got, room.ID)
		}
		t.Errorf("a new message did not move the room up: %v", got)
	}
}

// Reactions identify people by username, not id, so the store has to be told
// both to know which reactions are the user's own.
func TestReactionsAreMarkedAsOwn(t *testing.T) {
	s := openStore(t)
	if err := s.SaveMessages([]rocket.Message{{
		ID: "m1", RoomID: "r1", Msg: "nice", Timestamp: ts(time.Now()),
		User: rocket.User{ID: "other", Username: "alice"},
		Reactions: map[string]rocket.Reaction{
			":+1:":   {Usernames: []string{"alice", "me"}},
			":tada:": {Usernames: []string{"alice"}},
		},
	}}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	timeline, err := s.RoomTimeline("r1", 10)
	if err != nil {
		t.Fatalf("RoomTimeline: %v", err)
	}
	if len(timeline) != 1 {
		t.Fatalf("got %d messages", len(timeline))
	}

	found := map[string]model.Reaction{}
	for _, reaction := range timeline[0].Reactions {
		found[reaction.Emoji] = reaction
	}
	if got := found[":+1:"]; !got.Mine {
		t.Errorf(":+1: should be marked as ours: %+v", got)
	}
	if got := found[":+1:"]; got.Count() != 2 {
		t.Errorf(":+1: count = %d, want 2", got.Count())
	}
	if got := found[":tada:"]; got.Mine {
		t.Errorf(":tada: should not be ours: %+v", got)
	}

	// Reactions render in a stable order regardless of map iteration.
	if timeline[0].Reactions[0].Emoji != ":+1:" {
		t.Errorf("reactions not sorted: %+v", timeline[0].Reactions)
	}
}
func TestMentionCandidatesMergeMembersAndSpeakers(t *testing.T) {
	s := openStore(t)
	now := time.Now().Truncate(time.Millisecond)

	if err := s.SaveRoomMembers("r1", []rocket.User{
		{ID: "u1", Username: "dana", Name: "Dana Scully"},
		{ID: "u2", Username: "alice", Name: "Alice Adams"},
		{ID: "u3", Username: "me", Name: "Me"},
	}); err != nil {
		t.Fatalf("SaveRoomMembers: %v", err)
	}
	if err := s.SaveMessages([]rocket.Message{
		{ID: "m1", RoomID: "r1", Msg: "hi", Timestamp: ts(now.Add(-time.Minute)),
			User: rocket.User{ID: "u4", Username: "zoe", Name: "Zoe Zheng"}},
		{ID: "m2", RoomID: "r2", Msg: "elsewhere", Timestamp: ts(now),
			User: rocket.User{ID: "u5", Username: "otherroom"}},
	}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	members, err := s.MentionCandidates("r1")
	if err != nil {
		t.Fatalf("MentionCandidates: %v", err)
	}

	var names []string
	for _, member := range members {
		names = append(names, member.Username)
	}
	// Recent speakers first, then members who have not spoken, alphabetically.
	want := []string{"zoe", "alice", "dana"}
	if len(names) != len(want) {
		t.Fatalf("candidates = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", names, want)
		}
	}
	if members[0].Name != "Zoe Zheng" {
		t.Errorf("display name = %q, want Zoe Zheng", members[0].Name)
	}
}

func TestSaveRoomMembersReplacesTheRoster(t *testing.T) {
	s := openStore(t)
	if err := s.SaveRoomMembers("r1", []rocket.User{{Username: "gone"}, {Username: "stays"}}); err != nil {
		t.Fatalf("first SaveRoomMembers: %v", err)
	}
	if err := s.SaveRoomMembers("r1", []rocket.User{{Username: "stays"}, {Username: "joined"}}); err != nil {
		t.Fatalf("second SaveRoomMembers: %v", err)
	}

	members, err := s.MentionCandidates("r1")
	if err != nil {
		t.Fatalf("MentionCandidates: %v", err)
	}
	for _, member := range members {
		if member.Username == "gone" {
			t.Error("a member who left is still offered")
		}
	}
	if len(members) != 2 {
		t.Errorf("candidates = %+v, want stays and joined", members)
	}
}

func TestMentionCandidatesMergeMembersAndSpeakers(t *testing.T) {
	s := openStore(t)
	now := time.Now().Truncate(time.Millisecond)

	if err := s.SaveRoomMembers("r1", []rocket.User{
		{ID: "u1", Username: "dana", Name: "Dana Scully"},
		{ID: "u2", Username: "alice", Name: "Alice Adams"},
		{ID: "u3", Username: "me", Name: "Me"},
	}); err != nil {
		t.Fatalf("SaveRoomMembers: %v", err)
	}
	if err := s.SaveMessages([]rocket.Message{
		{ID: "m1", RoomID: "r1", Msg: "hi", Timestamp: ts(now.Add(-time.Minute)),
			User: rocket.User{ID: "u4", Username: "zoe", Name: "Zoe Zheng"}},
		{ID: "m2", RoomID: "r2", Msg: "elsewhere", Timestamp: ts(now),
			User: rocket.User{ID: "u5", Username: "otherroom"}},
	}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	members, err := s.MentionCandidates("r1")
	if err != nil {
		t.Fatalf("MentionCandidates: %v", err)
	}

	var names []string
	for _, member := range members {
		names = append(names, member.Username)
	}
	// Recent speakers first, then members who have not spoken, alphabetically.
	want := []string{"zoe", "alice", "dana"}
	if len(names) != len(want) {
		t.Fatalf("candidates = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", names, want)
		}
	}
	if members[0].Name != "Zoe Zheng" {
		t.Errorf("display name = %q, want Zoe Zheng", members[0].Name)
	}
}

func TestSaveRoomMembersReplacesTheRoster(t *testing.T) {
	s := openStore(t)
	if err := s.SaveRoomMembers("r1", []rocket.User{{Username: "gone"}, {Username: "stays"}}); err != nil {
		t.Fatalf("first SaveRoomMembers: %v", err)
	}
	if err := s.SaveRoomMembers("r1", []rocket.User{{Username: "stays"}, {Username: "joined"}}); err != nil {
		t.Fatalf("second SaveRoomMembers: %v", err)
	}

	members, err := s.MentionCandidates("r1")
	if err != nil {
		t.Fatalf("MentionCandidates: %v", err)
	}
	for _, member := range members {
		if member.Username == "gone" {
			t.Error("a member who left is still offered")
		}
	}
	if len(members) != 2 {
		t.Errorf("candidates = %+v, want stays and joined", members)
	}
}
