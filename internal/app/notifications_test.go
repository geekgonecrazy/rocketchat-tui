package app_test

import (
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
)

// notifications returns every Notification published so far.
func (h *harness) notifications() []app.Notification {
	var found []app.Notification
	for _, event := range h.snapshot() {
		if note, ok := event.(app.Notification); ok {
			found = append(found, note)
		}
	}
	return found
}

// waitForNotification blocks until one arrives, failing the test if none does.
func (h *harness) waitForNotification(what string) app.Notification {
	return waitFor(h.t, what, func() (app.Notification, bool) {
		notes := h.notifications()
		if len(notes) == 0 {
			return app.Notification{}, false
		}
		return notes[len(notes)-1], true
	})
}

// expectNoNotification gives the core a fair chance to publish one and fails if
// it does. It waits on a *different* event caused by the same push, so this is
// not a bare sleep: by the time that lands, the notification decision has been
// made and not taken.
func (h *harness) expectNoNotification(what string) {
	h.t.Helper()
	if notes := h.notifications(); len(notes) != 0 {
		h.t.Fatalf("%s should not notify, got %+v", what, notes)
	}
}

// mentionOf builds the mentions array a server sends when someone is named.
func mentionOf(userID, username string) map[string]any {
	return map[string]any{"_id": userID, "username": username}
}

func TestDirectMessageNotifies(t *testing.T) {
	h := newHarness(t)
	h.server.AddRoom("dm-1", "d", "alice", nil)
	h.server.AddSubscription("dm-1", "d", "alice", 0, 0, time.Now(), nil)
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("dm-1")

	h.server.PushMessage("dm-msg", "dm-1", "alice", "are you around?", time.Now(), nil)

	note := h.waitForNotification("a DM to notify")
	if note.Reason != app.NotifyDirect {
		t.Errorf("reason = %v, want NotifyDirect", note.Reason)
	}
	if note.Text != "are you around?" || note.RoomID != "dm-1" {
		t.Errorf("notification = %+v", note)
	}
	if note.Author != "alice" {
		t.Errorf("author = %q, want alice", note.Author)
	}
}

func TestMentionInAChannelNotifies(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now())
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("room-1")

	h.server.PushMessage("m-mention", "room-1", "bob", "@tester can you look?", time.Now(),
		map[string]any{"mentions": []any{mentionOf(fakerc.UserID, fakerc.Username)}})

	note := h.waitForNotification("a mention to notify")
	if note.Reason != app.NotifyMention {
		t.Errorf("reason = %v, want NotifyMention", note.Reason)
	}
	if note.RoomLabel != "# general" {
		t.Errorf("room label = %q, want the sidebar's own label", note.RoomLabel)
	}
}

// @all and @here arrive as mentions of pseudo-users by those names. Every other
// client treats them as addressed to you, and a release announcement nobody sees
// is the case this exists for.
func TestGroupMentionNotifies(t *testing.T) {
	for _, username := range []string{"all", "here"} {
		t.Run(username, func(t *testing.T) {
			h := newHarness(t)
			h.seedRoom("room-1", "general", 0, 0, time.Now())
			h.start()
			h.waitConnected()
			h.waitForRoomInSidebar("room-1")

			h.server.PushMessage("m-"+username, "room-1", "bob", "@"+username+" deploying", time.Now(),
				map[string]any{"mentions": []any{mentionOf("", username)}})

			if note := h.waitForNotification("@" + username + " to notify"); note.Reason != app.NotifyMention {
				t.Errorf("reason = %v, want NotifyMention", note.Reason)
			}
		})
	}
}

// The open room notifies too. Having a room on screen is not evidence anyone is
// looking at the screen.
func TestMentionInTheOpenRoomStillNotifies(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now())
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")

	h.server.PushMessage("m-open", "room-1", "bob", "@tester still there?", time.Now(),
		map[string]any{"mentions": []any{mentionOf(fakerc.UserID, fakerc.Username)}})

	h.waitForNotification("a mention in the room already on screen")
}

func TestOrdinaryChannelTrafficDoesNotNotify(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now())
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("room-1")

	h.server.PushMessage("chatter", "room-1", "bob", "morning all", time.Now(), nil)

	// The message landing in the cache is the fence: the core saves before it
	// decides, so by the time this is true the decision has been made.
	waitFor(t, "the message to be cached", func() (bool, bool) {
		_, found, err := h.cache.Message("chatter")
		return found, err == nil && found
	})
	h.expectNoNotification("ordinary channel traffic")
}

func TestOwnMessageDoesNotNotify(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now())
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")

	// A message from you that mentions you: the server echoes your own sends
	// back, and "@all" from you would otherwise notify you about yourself.
	h.server.PushMessage("mine", "room-1", fakerc.Username, "@all shipping now", time.Now(),
		map[string]any{
			"u":        map[string]any{"_id": fakerc.UserID, "username": fakerc.Username},
			"mentions": []any{mentionOf("", "all")},
		})

	waitFor(t, "the echo to reach the timeline", func() (bool, bool) {
		timeline, ok := h.lastTimeline("room-1")
		if !ok {
			return false, false
		}
		for _, msg := range timeline.Messages {
			if msg.ID == "mine" {
				return true, true
			}
		}
		return false, false
	})
	h.expectNoNotification("your own message")
}

func TestSystemMessageDoesNotNotify(t *testing.T) {
	h := newHarness(t)
	h.server.AddRoom("dm-1", "d", "alice", nil)
	h.server.AddSubscription("dm-1", "d", "alice", 0, 0, time.Now(), nil)
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("dm-1")

	// In a DM, where every other message notifies — so this tests the system
	// check rather than the room check.
	h.server.PushMessage("joined", "dm-1", "alice", "alice", time.Now(),
		map[string]any{"t": "uj"})
	h.server.PushMessage("real", "dm-1", "alice", "hello", time.Now(), nil)

	note := h.waitForNotification("the real message to notify")
	if note.RoomID != "dm-1" || note.Text != "hello" {
		t.Errorf("notification = %+v, want the real message", note)
	}
	if notes := h.notifications(); len(notes) != 1 {
		t.Errorf("got %d notifications, want only the real message: %+v", len(notes), notes)
	}
}

// The server re-pushes a whole message when it is edited. Notifying again
// because someone fixed a typo is how a notification stops meaning anything.
func TestEditedMessageDoesNotNotifyAgain(t *testing.T) {
	h := newHarness(t)
	h.server.AddRoom("dm-1", "d", "alice", nil)
	h.server.AddSubscription("dm-1", "d", "alice", 0, 0, time.Now(), nil)
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("dm-1")

	at := time.Now()
	h.server.PushMessage("m1", "dm-1", "alice", "hello", at, nil)
	h.waitForNotification("the original message")

	h.server.PushMessage("m1", "dm-1", "alice", "hello!", at,
		map[string]any{"editedAt": map[string]any{"$date": at.Add(time.Minute).UnixMilli()}})
	h.server.PushMessage("m2", "dm-1", "alice", "and another thing", at.Add(2*time.Minute), nil)

	// The later message is the fence: once it has notified, the edit has been
	// through the same decision and did not.
	waitFor(t, "the later message to notify", func() (bool, bool) {
		for _, note := range h.notifications() {
			if note.Text == "and another thing" {
				return true, true
			}
		}
		return false, false
	})
	for _, note := range h.notifications() {
		if note.Text == "hello!" {
			t.Error("an edit notified as if it were new")
		}
	}
}

// The same message arriving twice — which happens whenever the server touches
// it, e.g. when someone reacts to it.
func TestTheSameMessageNotifiesOnce(t *testing.T) {
	h := newHarness(t)
	h.server.AddRoom("dm-1", "d", "alice", nil)
	h.server.AddSubscription("dm-1", "d", "alice", 0, 0, time.Now(), nil)
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("dm-1")

	at := time.Now()
	h.server.PushMessage("m1", "dm-1", "alice", "hello", at, nil)
	h.waitForNotification("the first delivery")
	h.server.PushMessage("m1", "dm-1", "alice", "hello", at, nil)
	h.server.PushMessage("m2", "dm-1", "alice", "second", at.Add(time.Minute), nil)

	waitFor(t, "the second message to notify", func() (bool, bool) {
		for _, note := range h.notifications() {
			if note.Text == "second" {
				return true, true
			}
		}
		return false, false
	})

	count := 0
	for _, note := range h.notifications() {
		if note.Text == "hello" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the same message notified %d times, want 1", count)
	}
}

// A reply in a thread you follow. The reply itself carries no follower list, so
// this is the case that needs the parent to have been cached.
func TestReplyInAFollowedThreadNotifies(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now())
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("room-1")

	at := time.Now()
	// The parent, listing us among its followers, the way the server sends it.
	h.server.PushMessage("parent", "room-1", "alice", "anyone seen the build?", at,
		map[string]any{"tcount": 1, "replies": []any{"alice-id", fakerc.UserID}})
	waitFor(t, "the parent to be cached", func() (bool, bool) {
		follows, err := h.cache.FollowsThread("parent", fakerc.UserID)
		return follows, err == nil && follows
	})

	h.server.PushMessage("reply", "room-1", "bob", "it went green", at.Add(time.Minute),
		map[string]any{"tmid": "parent"})

	note := h.waitForNotification("a followed thread's reply to notify")
	if note.Reason != app.NotifyThread {
		t.Errorf("reason = %v, want NotifyThread", note.Reason)
	}
	if note.Text != "it went green" {
		t.Errorf("text = %q", note.Text)
	}
}

func TestReplyInAThreadYouDoNotFollowIsQuiet(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now())
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("room-1")

	at := time.Now()
	h.server.PushMessage("parent", "room-1", "alice", "anyone seen the build?", at,
		map[string]any{"tcount": 1, "replies": []any{"alice-id", "carol-id"}})
	waitFor(t, "the parent to be cached", func() (bool, bool) {
		_, found, err := h.cache.Message("parent")
		return found, err == nil && found
	})

	h.server.PushMessage("reply", "room-1", "bob", "it went green", at.Add(time.Minute),
		map[string]any{"tmid": "parent"})
	waitFor(t, "the reply to be cached", func() (bool, bool) {
		_, found, err := h.cache.Message("reply")
		return found, err == nil && found
	})

	h.expectNoNotification("a thread you do not follow")
}

// A file with no message text still has to say something, or the notification
// is a name and a blank space.
func TestAttachmentWithNoTextDescribesItself(t *testing.T) {
	h := newHarness(t)
	h.server.AddRoom("dm-1", "d", "alice", nil)
	h.server.AddSubscription("dm-1", "d", "alice", 0, 0, time.Now(), nil)
	h.start()
	h.waitConnected()
	h.waitForRoomInSidebar("dm-1")

	h.server.PushMessage("file", "dm-1", "alice", "", time.Now(), map[string]any{
		"attachments": []any{map[string]any{
			"title": "diagram.png", "title_link": "/file-upload/diagram.png",
			"title_link_download": true,
		}},
	})

	if note := h.waitForNotification("a file to notify"); note.Text != "sent diagram.png" {
		t.Errorf("text = %q, want it to describe the file", note.Text)
	}
}
