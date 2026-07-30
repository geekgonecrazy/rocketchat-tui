package rocket_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// waitForEvent drains the realtime stream until match returns true.
func waitForEvent[T rocket.Event](t *testing.T, events <-chan rocket.Event, match func(T) bool) T {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("realtime event channel closed before the expected event arrived")
			}
			if typed, isType := event.(T); isType && match(typed) {
				return typed
			}
		case <-deadline:
			var zero T
			t.Fatalf("timed out waiting for a %T event", zero)
			return zero
		}
	}
}

// startRealtime brings up a connected realtime client against the fake server.
func startRealtime(t *testing.T, server *fakerc.Server, token string) (*rocket.Realtime, <-chan rocket.Event) {
	t.Helper()

	client, err := rocket.NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetCredentials(rocket.Credentials{UserID: fakerc.UserID, AuthToken: token})

	realtime := rocket.NewRealtime(client.WebSocketURL(), client.Credentials, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go realtime.Run(ctx)

	events := realtime.Events()
	waitForEvent(t, events, func(e rocket.ConnStateEvent) bool { return e.State == rocket.Connected })
	return realtime, events
}

func TestRealtimeConnectsAndAuthenticates(t *testing.T) {
	server := fakerc.New(t)
	realtime, _ := startRealtime(t, server, fakerc.AuthToken)

	if state := realtime.State(); state != rocket.Connected {
		t.Errorf("state = %v, want connected", state)
	}
	if count := server.ConnCount(); count != 1 {
		t.Errorf("server sees %d connections, want 1", count)
	}
}

func TestRealtimeRetriesAfterRejectedLogin(t *testing.T) {
	server := fakerc.New(t)

	client, err := rocket.NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetCredentials(rocket.Credentials{UserID: fakerc.UserID, AuthToken: "stale-token"})

	realtime := rocket.NewRealtime(client.WebSocketURL(), client.Credentials, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go realtime.Run(ctx)

	// A rejected DDP login must surface as a disconnect carrying the reason,
	// not as a silent hang.
	event := waitForEvent(t, realtime.Events(), func(e rocket.ConnStateEvent) bool {
		return e.State == rocket.Disconnected && e.Err != nil
	})
	if !strings.Contains(event.Err.Error(), "login") {
		t.Errorf("error = %v, want it to mention login", event.Err)
	}
}

func TestRealtimeDeliversPushedMessage(t *testing.T) {
	server := fakerc.New(t)
	realtime, events := startRealtime(t, server, fakerc.AuthToken)
	realtime.SubscribeRoomMessages("room-1")

	sentAt := time.Now().Truncate(time.Millisecond)
	// Give the subscription a moment to reach the server before pushing.
	waitFor(t, func() bool { return server.ConnCount() == 1 })
	server.PushMessage("msg-1", "room-1", "alice", "hello from the server", sentAt, nil)

	event := waitForEvent(t, events, func(e rocket.MessageEvent) bool { return e.Message.ID == "msg-1" })
	if event.Message.Msg != "hello from the server" {
		t.Errorf("text = %q", event.Message.Msg)
	}
	if event.Message.RoomID != "room-1" {
		t.Errorf("room = %q, want room-1", event.Message.RoomID)
	}
	// The DDP path uses {"$date": ms}, so this also proves EJSON decoding works.
	if !event.Message.Timestamp.Time.Equal(sentAt.UTC()) {
		t.Errorf("timestamp = %v, want %v", event.Message.Timestamp.Time, sentAt.UTC())
	}
}

func TestRealtimeDeliversBothTypingShapes(t *testing.T) {
	server := fakerc.New(t)
	realtime, events := startRealtime(t, server, fakerc.AuthToken)
	realtime.SubscribeRoomActivity("room-1")

	server.PushTyping("room-1", "alice", true)
	modern := waitForEvent(t, events, func(e rocket.TypingEvent) bool { return e.Username == "alice" })
	if !modern.Typing || modern.RoomID != "room-1" {
		t.Errorf("modern typing event = %+v", modern)
	}

	server.PushLegacyTyping("room-1", "bob", true)
	legacy := waitForEvent(t, events, func(e rocket.TypingEvent) bool { return e.Username == "bob" })
	if !legacy.Typing || legacy.RoomID != "room-1" {
		t.Errorf("legacy typing event = %+v", legacy)
	}

	server.PushTyping("room-1", "alice", false)
	stopped := waitForEvent(t, events, func(e rocket.TypingEvent) bool {
		return e.Username == "alice" && !e.Typing
	})
	if stopped.Typing {
		t.Error("expected a stop-typing event")
	}
}

func TestRealtimeEmitsTypingInBothShapes(t *testing.T) {
	server := fakerc.New(t)
	realtime, _ := startRealtime(t, server, fakerc.AuthToken)

	if err := realtime.NotifyTyping(context.Background(), "room-1", "tester", true); err != nil {
		t.Fatalf("NotifyTyping: %v", err)
	}

	// The client announces on both stream names so the indicator works across
	// server versions.
	waitFor(t, func() bool {
		var sawModern, sawLegacy bool
		for _, notification := range server.Notifications() {
			if notification.EventName == "room-1/user-activity" && notification.Typing {
				sawModern = true
			}
			if notification.EventName == "room-1/typing" && notification.Typing {
				sawLegacy = true
			}
		}
		return sawModern && sawLegacy
	})

	if err := realtime.NotifyTyping(context.Background(), "room-1", "tester", false); err != nil {
		t.Fatalf("NotifyTyping(stop): %v", err)
	}
	waitFor(t, func() bool {
		for _, notification := range server.Notifications() {
			if notification.EventName == "room-1/typing" && !notification.Typing {
				return true
			}
		}
		return false
	})
}

func TestRealtimeDeliversSubscriptionChanges(t *testing.T) {
	server := fakerc.New(t)
	realtime, events := startRealtime(t, server, fakerc.AuthToken)
	realtime.SubscribeUserEvents(fakerc.UserID)

	server.PushSubscription("room-1", 7, 2)
	event := waitForEvent(t, events, func(e rocket.SubscriptionEvent) bool {
		return e.Subscription.RoomID == "room-1"
	})
	if event.Subscription.Unread != 7 || event.Subscription.UserMentions != 2 {
		t.Errorf("unread/mentions = %d/%d, want 7/2",
			event.Subscription.Unread, event.Subscription.UserMentions)
	}
	if event.Action != "updated" {
		t.Errorf("action = %q, want updated", event.Action)
	}
}

func TestRealtimeNotifyTypingFailsWhenDisconnected(t *testing.T) {
	realtime := rocket.NewRealtime("ws://127.0.0.1:1/websocket",
		func() rocket.Credentials { return rocket.Credentials{} }, nil)
	if err := realtime.NotifyTyping(context.Background(), "room-1", "tester", true); err == nil {
		t.Error("expected an error when not connected")
	}
}

// waitFor polls condition until it holds or the test times out.
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
