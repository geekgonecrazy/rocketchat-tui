package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

// TestFullScreenSnapshot renders a realistic screen: it asserts every feature is
// visible at once, and logs the frame so the layout can be eyeballed with -v.
func TestFullScreenSnapshot(t *testing.T) {
	base := time.Now().Add(-2 * time.Hour)
	lastSeen := base.Add(30 * time.Minute)

	m := newTestChat(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 96, Height: 28})
	m = event(m, app.RoomsUpdated{
		Rooms: []model.Room{
			{ID: "r1", DisplayName: "general", Kind: model.KindChannel, Unread: 3, Alert: true, Topic: "company wide chatter", LastMessageAt: time.Now()},
			{ID: "r2", DisplayName: "alice", Kind: model.KindDirect, UserMentions: 2, LastMessageAt: time.Now().Add(-time.Minute)},
			{ID: "r3", DisplayName: "engineering", Kind: model.KindTeam, Unread: 1, LastMessageAt: time.Now().Add(-time.Hour)},
			{ID: "r4", DisplayName: "auth-spike", Kind: model.KindDiscussion, LastMessageAt: time.Now().Add(-2 * time.Hour)},
			{ID: "r5", DisplayName: "secret-plans", Kind: model.KindPrivate, LastMessageAt: time.Now().Add(-3 * time.Hour)},
			{ID: "r6", DisplayName: "random", Kind: model.KindChannel, LastMessageAt: time.Now().Add(-4 * time.Hour)},
		},
		Totals: app.Totals{Unread: 4, Mentions: 2, UnreadRooms: 3},
	})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Room:   model.Room{ID: "r1", DisplayName: "general", Kind: model.KindChannel, Topic: "company wide chatter"},
		Messages: []model.Message{
			{ID: "a", Username: "alice", Author: "Alice", Text: "Morning! I pushed the auth refactor, it needs a second pair of eyes before we cut the release.", At: base},
			{ID: "b", Username: "alice", Author: "Alice", Text: "Specifically the token refresh path.", At: base.Add(time.Minute)},
			{ID: "c", Username: "bob", Author: "Bob", Text: "On it.", At: base.Add(5 * time.Minute),
				ThreadCount: 3, ThreadLastAt: time.Now().Add(-10 * time.Minute),
				Reactions: []model.Reaction{{Emoji: ":+1:", Usernames: []string{"alice", "carol"}}}},
			{ID: "d", Username: "carol", Author: "Carol", SystemType: "uj", At: base.Add(10 * time.Minute)},
			{ID: "e", Username: "carol", Author: "Carol", Text: "Left a few notes in the thread. Also attaching the perf numbers.", At: lastSeen.Add(time.Minute),
				Attachments: []model.Attachment{{Title: "perf-before-after.png", Text: "42% faster on cold start"}}},
			{ID: "f", Username: "tester", Author: "Test Tester", Text: "Nice, I'll review after standup.", At: lastSeen.Add(3 * time.Minute), Own: true},
		},
		UnreadFrom: lastSeen, UnreadCount: 2, HasMore: true,
	})
	m = event(m, app.StatusChanged{Connection: rocket.Connected})
	m = event(m, app.TypingUpdated{RoomID: "r1", Users: model.TypingUsers{"alice", "bob"}})
	m.focus = focusComposer
	for _, r := range "sounds good" {
		m, _ = m.Update(press(string(r)))
	}

	view := m.View()
	want := []string{
		"# general", "company wide chatter", // header: room and topic
		"@2", "3 unread", // header: attention counters
		"@alice", "& engineering", "\u21b3 auth-spike", // sidebar: DM, team, discussion
		"Today",                            // date separator
		"new messages",                     // unread divider
		"\u21b3 3 replies",                 // thread affordance
		":+1: 2",                           // reactions
		"\U0001f4ce perf-before-after.png", // attachment
		"Carol joined the channel",         // system message
		"alice and bob are typing\u2026",   // typing indicator
		"sounds good",                      // composer contents
		"\u25cf connected",                 // status bar
	}
	for _, fragment := range want {
		if !strings.Contains(view, fragment) {
			t.Errorf("snapshot missing %q", fragment)
		}
	}
	for i, line := range strings.Split(view, "\n") {
		if render.Width(line) > 96 {
			t.Errorf("line %d is %d cells wide, want <= 96", i, render.Width(line))
		}
	}

	t.Logf("\n%s\n", view)
}
