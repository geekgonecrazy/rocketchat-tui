package rocket_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

func newClient(t *testing.T, server *fakerc.Server) *rocket.Client {
	t.Helper()
	client, err := rocket.NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func TestTimestampDecodesBothWireFormats(t *testing.T) {
	tests := []struct {
		name string
		json string
		want time.Time
	}{
		{"rest iso string", `"2026-07-30T12:34:56.789Z"`,
			time.Date(2026, 7, 30, 12, 34, 56, 789000000, time.UTC)},
		{"ddp ejson date", `{"$date":1785587696789}`, time.UnixMilli(1785587696789).UTC()},
		{"raw millis", `1785587696789`, time.UnixMilli(1785587696789).UTC()},
		{"null", `null`, time.Time{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ts rocket.Timestamp
			if err := json.Unmarshal([]byte(tc.json), &ts); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.json, err)
			}
			if !ts.Time.Equal(tc.want) {
				t.Errorf("got %v, want %v", ts.Time, tc.want)
			}
		})
	}
}

func TestNormalizeServerURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"chat.example.com", "https://chat.example.com"},
		{"https://chat.example.com/", "https://chat.example.com"},
		{"http://localhost:3000", "http://localhost:3000"},
		{"https://example.com/rocket/", "https://example.com/rocket"},
		// A scheme-less loopback address is a local dev server, which is served
		// over plain HTTP; anything else defaults to HTTPS.
		{"localhost:3000", "http://localhost:3000"},
		{"127.0.0.1:3000", "http://127.0.0.1:3000"},
		{"localhost", "http://localhost"},
		{"chat.example.com:3000", "https://chat.example.com:3000"},
	}
	for _, tc := range tests {
		client, err := rocket.NewClient(tc.in)
		if err != nil {
			t.Fatalf("NewClient(%q): %v", tc.in, err)
		}
		if got := client.ServerURL(); got != tc.want {
			t.Errorf("NewClient(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if _, err := rocket.NewClient("  "); err == nil {
		t.Error("expected an error for an empty server URL")
	}
	if _, err := rocket.NewClient("ftp://example.com"); err == nil {
		t.Error("expected an error for an unsupported scheme")
	}
}

func TestWebSocketURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://chat.example.com", "wss://chat.example.com/websocket"},
		{"http://localhost:3000", "ws://localhost:3000/websocket"},
		{"https://example.com/rocket", "wss://example.com/rocket/websocket"},
		{"localhost:3000", "ws://localhost:3000/websocket"},
	}
	for _, tc := range tests {
		client, err := rocket.NewClient(tc.in)
		if err != nil {
			t.Fatalf("NewClient(%q): %v", tc.in, err)
		}
		if got := client.WebSocketURL(); got != tc.want {
			t.Errorf("WebSocketURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoginWithPassword(t *testing.T) {
	server := fakerc.New(t)
	client := newClient(t, server)

	me, err := client.LoginWithPassword(context.Background(), fakerc.Username, fakerc.Password, "")
	if err != nil {
		t.Fatalf("LoginWithPassword: %v", err)
	}
	if me.Username != fakerc.Username {
		t.Errorf("username = %q, want %q", me.Username, fakerc.Username)
	}
	if me.Email != "tester@example.com" {
		t.Errorf("email = %q, want tester@example.com", me.Email)
	}
	if creds := client.Credentials(); creds.AuthToken != fakerc.AuthToken || creds.UserID != fakerc.UserID {
		t.Errorf("credentials not adopted: %+v", creds)
	}
}

func TestLoginWithPasswordRejectsBadCredentials(t *testing.T) {
	server := fakerc.New(t)
	client := newClient(t, server)

	_, err := client.LoginWithPassword(context.Background(), fakerc.Username, "wrong", "")
	if err == nil {
		t.Fatal("expected a login failure")
	}
	var apiErr *rocket.APIError
	if !errors.As(err, &apiErr) || !apiErr.Unauthorized() {
		t.Fatalf("expected an unauthorized APIError, got %v", err)
	}
}

func TestLoginRequestsTOTPThenSucceeds(t *testing.T) {
	server := fakerc.New(t)
	server.RequireTOTP = true
	client := newClient(t, server)

	_, err := client.LoginWithPassword(context.Background(), fakerc.Username, fakerc.Password, "")
	if !errors.Is(err, rocket.ErrTOTPRequired) {
		t.Fatalf("expected ErrTOTPRequired, got %v", err)
	}

	if _, err := client.LoginWithPassword(context.Background(), fakerc.Username, fakerc.Password, fakerc.TOTPCode); err != nil {
		t.Fatalf("login with TOTP: %v", err)
	}
	if !client.Authenticated() {
		t.Error("client should be authenticated after a TOTP login")
	}
}

func TestLoginWithToken(t *testing.T) {
	server := fakerc.New(t)
	client := newClient(t, server)

	me, err := client.LoginWithToken(context.Background(), fakerc.AuthToken)
	if err != nil {
		t.Fatalf("LoginWithToken: %v", err)
	}
	if me.ID != fakerc.UserID {
		t.Errorf("user id = %q, want %q", me.ID, fakerc.UserID)
	}

	if _, err := client.LoginWithToken(context.Background(), "not-a-real-token"); err == nil {
		t.Error("expected a failure for an invalid token")
	}
}

func TestUnauthenticatedCallIsRejectedLocally(t *testing.T) {
	server := fakerc.New(t)
	client := newClient(t, server)

	if _, err := client.Me(context.Background()); err == nil {
		t.Fatal("expected an error when calling an authenticated endpoint without a token")
	}
}

func TestSubscriptionsAndRooms(t *testing.T) {
	server := fakerc.New(t)
	lastSeen := time.Now().Add(-time.Hour)
	server.AddRoom("room-1", "c", "general", map[string]any{"topic": "all things"})
	server.AddSubscription("room-1", "c", "general", 3, 1, lastSeen, nil)

	client := newClient(t, server)
	if _, err := client.LoginWithToken(context.Background(), fakerc.AuthToken); err != nil {
		t.Fatalf("login: %v", err)
	}

	subs, err := client.Subscriptions(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("Subscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d subscriptions, want 1", len(subs))
	}
	if subs[0].Unread != 3 || subs[0].UserMentions != 1 {
		t.Errorf("unread/mentions = %d/%d, want 3/1", subs[0].Unread, subs[0].UserMentions)
	}
	if subs[0].LastSeen == nil || !subs[0].LastSeen.Time.Equal(lastSeen.UTC().Truncate(time.Nanosecond)) {
		t.Errorf("last seen not decoded: %v", subs[0].LastSeen)
	}

	rooms, err := client.Rooms(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("Rooms: %v", err)
	}
	if len(rooms) != 1 || rooms[0].Topic != "all things" {
		t.Errorf("unexpected rooms: %+v", rooms)
	}
}

func TestHistoryPagesBackwards(t *testing.T) {
	server := fakerc.New(t)
	server.AddRoom("room-1", "c", "general", nil)
	base := time.Now().Add(-24 * time.Hour)
	for i := 0; i < 10; i++ {
		server.AddMessage("m"+string(rune('0'+i)), "room-1", "alice", "message",
			base.Add(time.Duration(i)*time.Minute), nil)
	}

	client := newClient(t, server)
	if _, err := client.LoginWithToken(context.Background(), fakerc.AuthToken); err != nil {
		t.Fatalf("login: %v", err)
	}

	first, err := client.History(context.Background(), rocket.HistoryQuery{
		RoomID: "room-1", RoomType: "c", Count: 4,
	})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(first) != 4 {
		t.Fatalf("got %d messages, want 4", len(first))
	}
	// Newest first.
	if !first[0].Timestamp.Time.After(first[1].Timestamp.Time) {
		t.Error("expected messages newest-first")
	}

	oldest := first[len(first)-1].Timestamp.Time
	second, err := client.History(context.Background(), rocket.HistoryQuery{
		RoomID: "room-1", RoomType: "c", Count: 4, Before: oldest,
	})
	if err != nil {
		t.Fatalf("History page 2: %v", err)
	}
	if len(second) != 4 {
		t.Fatalf("page 2 returned %d messages, want 4", len(second))
	}
	for _, msg := range second {
		if !msg.Timestamp.Time.Before(oldest) {
			t.Errorf("page 2 message %s is not older than %v", msg.ID, oldest)
		}
	}
}

func TestHistoryRequiresKnownRoomType(t *testing.T) {
	server := fakerc.New(t)
	client := newClient(t, server)
	if _, err := client.LoginWithToken(context.Background(), fakerc.AuthToken); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := client.History(context.Background(), rocket.HistoryQuery{RoomID: "r"}); err == nil {
		t.Error("expected an error when the room type is unknown")
	}
}

func TestSendMessageAndThread(t *testing.T) {
	server := fakerc.New(t)
	server.AddRoom("room-1", "c", "general", nil)
	server.AddMessage("parent-1", "room-1", "alice", "let's discuss",
		time.Now().Add(-time.Hour), map[string]any{"tcount": 2})
	server.AddMessage("reply-1", "room-1", "bob", "first reply",
		time.Now().Add(-30*time.Minute), map[string]any{"tmid": "parent-1"})

	client := newClient(t, server)
	if _, err := client.LoginWithToken(context.Background(), fakerc.AuthToken); err != nil {
		t.Fatalf("login: %v", err)
	}

	threads, total, err := client.ThreadsList(context.Background(), "room-1", 10, 0)
	if err != nil {
		t.Fatalf("ThreadsList: %v", err)
	}
	if total != 1 || len(threads) != 1 || threads[0].ID != "parent-1" {
		t.Fatalf("unexpected thread list: total=%d threads=%+v", total, threads)
	}

	replies, err := client.ThreadMessages(context.Background(), "parent-1", 0, 0)
	if err != nil {
		t.Fatalf("ThreadMessages: %v", err)
	}
	if len(replies) != 1 || replies[0].ThreadParentID != "parent-1" {
		t.Fatalf("unexpected replies: %+v", replies)
	}

	sent, err := client.Send(context.Background(), rocket.SendOptions{
		RoomID: "room-1", Text: "my reply", ThreadID: "parent-1", AlsoSendToChannel: true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.Msg != "my reply" || sent.ThreadParentID != "parent-1" {
		t.Errorf("unexpected sent message: %+v", sent)
	}
	if posted := server.SentMessages(); len(posted) != 1 || posted[0].ThreadID != "parent-1" {
		t.Errorf("server recorded %+v", posted)
	}
}

func TestMarkRead(t *testing.T) {
	server := fakerc.New(t)
	client := newClient(t, server)
	if _, err := client.LoginWithToken(context.Background(), fakerc.AuthToken); err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := client.MarkRead(context.Background(), "room-1"); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if read := server.ReadRooms(); len(read) != 1 || read[0] != "room-1" {
		t.Errorf("server recorded reads %v", read)
	}
}
