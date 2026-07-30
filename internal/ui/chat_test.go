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

// newTestChat builds a sized chat model. The core is real but not running: its
// action channel is buffered, so UI calls are recorded and never block.
func newTestChat(t *testing.T) chatModel {
	t.Helper()
	client, err := rocket.NewClient("https://chat.example.com")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	core := app.New(client, nil, nil)

	m := newChatModel(core, render.DefaultTheme(), "tester", "chat.example.com", t.TempDir())
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func press(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func event(m chatModel, e app.Event) chatModel {
	m, _ = m.Update(coreEventMsg{event: e})
	return m
}

func sampleRooms() []model.Room {
	now := time.Now()
	// Slugs and display names differ the way they do on a real server: a
	// subscription carries both, and a DM's name is the other person.
	return []model.Room{
		{ID: "r1", Name: "general", DisplayName: "general", Kind: model.KindChannel, Unread: 3, Alert: true,
			LastMessageAt: now},
		{ID: "r2", Name: "alice", DisplayName: "alice", Kind: model.KindDirect, UserMentions: 1,
			LastMessageAt: now.Add(-time.Minute)},
		{ID: "r3", Name: "random", DisplayName: "random", Kind: model.KindChannel,
			LastMessageAt: now.Add(-time.Hour)},
	}
}

func TestChatOpensFirstRoomOnFirstSnapshot(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms(), Totals: app.Totals{Unread: 3, Mentions: 1, UnreadRooms: 2}})

	if m.activeRoom != "r1" {
		t.Errorf("active room = %q, want r1", m.activeRoom)
	}
	// Landing in a room should leave the user ready to type.
	if m.focus != focusComposer {
		t.Errorf("focus = %v, want composer", m.focus)
	}
}

func TestChatDoesNotReopenRoomOnLaterSnapshots(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.activeRoom = "r3"

	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	if m.activeRoom != "r3" {
		t.Errorf("active room changed to %q on a routine refresh", m.activeRoom)
	}
}

func TestChatRendersMessagesAndUnreadDivider(t *testing.T) {
	lastSeen := time.Now().Add(-time.Hour)
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Room:   model.Room{ID: "r1", DisplayName: "general", Kind: model.KindChannel},
		Messages: []model.Message{
			{ID: "a", Username: "alice", Author: "Alice", Text: "old news", At: lastSeen.Add(-time.Minute)},
			{ID: "b", Username: "bob", Author: "Bob", Text: "fresh news", At: lastSeen.Add(time.Minute)},
		},
		UnreadFrom:  lastSeen,
		UnreadCount: 1,
	})

	view := m.View()
	for _, want := range []string{"Alice", "old news", "Bob", "fresh news", "new messages"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	if m.body.UnreadLine < 0 {
		t.Error("expected the model to know where the unread divider is")
	}
}

func TestChatScrollsToUnreadDividerOnOpen(t *testing.T) {
	lastSeen := time.Now().Add(-time.Hour)
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})

	// A long backlog with only the last message unread: the divider must be on
	// screen, not scrolled past.
	var messages []model.Message
	for i := 0; i < 80; i++ {
		messages = append(messages, model.Message{
			ID:       "m" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Username: "alice", Author: "Alice", Text: "backlog message",
			At: lastSeen.Add(time.Duration(i-79) * time.Minute),
		})
	}
	messages = append(messages, model.Message{
		ID: "new", Username: "bob", Author: "Bob", Text: "the new one",
		At: lastSeen.Add(time.Minute),
	})

	m = event(m, app.TimelineUpdated{
		RoomID: "r1", Messages: messages, UnreadFrom: lastSeen, UnreadCount: 1,
	})

	if m.body.UnreadLine < 0 {
		t.Fatal("no unread divider was rendered")
	}
	if m.scroll > m.body.UnreadLine || m.body.UnreadLine >= m.scroll+m.bodyHeight() {
		t.Errorf("divider at line %d is outside the visible window [%d, %d)",
			m.body.UnreadLine, m.scroll, m.scroll+m.bodyHeight())
	}
	if !strings.Contains(m.View(), "new messages") {
		t.Error("the unread rule should be visible after opening a room with unreads")
	}
}

func TestChatTabCyclesFocus(t *testing.T) {
	m := newTestChat(t)
	if m.focus != focusRooms {
		t.Fatalf("initial focus = %v, want rooms", m.focus)
	}
	for _, want := range []focusArea{focusMessages, focusComposer, focusRooms} {
		m, _ = m.Update(press("tab"))
		if m.focus != want {
			t.Fatalf("focus = %v, want %v", m.focus, want)
		}
	}
}

func TestChatRoomCursorMovesAndOpens(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusRooms

	m, _ = m.Update(press("j"))
	if m.roomCursor != 1 {
		t.Errorf("cursor = %d, want 1", m.roomCursor)
	}
	m, _ = m.Update(press("enter"))
	if m.activeRoom != "r2" {
		t.Errorf("active room = %q, want r2", m.activeRoom)
	}

	// The cursor must not run off the end of the list.
	m.focus = focusRooms
	for i := 0; i < 10; i++ {
		m, _ = m.Update(press("j"))
	}
	if m.roomCursor != len(m.visible)-1 {
		t.Errorf("cursor = %d, want %d", m.roomCursor, len(m.visible)-1)
	}
}

func TestChatRoomFilter(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusRooms

	m, _ = m.Update(press("/"))
	if !m.filtering {
		t.Fatal("expected filter mode")
	}
	for _, r := range "rand" {
		m, _ = m.Update(press(string(r)))
	}
	if len(m.visible) != 1 || m.visible[0].ID != "r3" {
		t.Fatalf("filtered rooms = %+v", m.visible)
	}
	if !strings.Contains(m.View(), "/rand") {
		t.Error("filter text should appear in the sidebar header")
	}

	// Backspace removes one character and re-filters.
	m, _ = m.Update(press("backspace"))
	if m.filter != "ran" {
		t.Errorf("filter = %q, want ran", m.filter)
	}
	for i := 0; i < 3; i++ {
		m, _ = m.Update(press("backspace"))
	}
	if m.filter != "" || len(m.visible) != 3 {
		t.Errorf("clearing the filter left %q with %d rooms visible", m.filter, len(m.visible))
	}

	m, _ = m.Update(press("esc"))
	if m.filtering || m.filter != "" {
		t.Error("esc should leave filter mode and clear the filter")
	}
}

func TestChatMessageCursorSelectsNewestFirst(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Messages: []model.Message{
			{ID: "a", Username: "alice", Text: "one", At: time.Now().Add(-2 * time.Minute)},
			{ID: "b", Username: "alice", Text: "two", At: time.Now().Add(-time.Minute)},
		},
	})
	m.focus = focusMessages

	m, _ = m.Update(press("k"))
	if m.msgCursor != 1 {
		t.Errorf("first move selected index %d, want the newest (1)", m.msgCursor)
	}
	m, _ = m.Update(press("k"))
	if m.msgCursor != 0 {
		t.Errorf("cursor = %d, want 0", m.msgCursor)
	}
	// Cannot move above the oldest message.
	m, _ = m.Update(press("k"))
	if m.msgCursor != 0 {
		t.Errorf("cursor = %d, want it clamped at 0", m.msgCursor)
	}
}

func TestChatSelectionSurvivesOlderHistoryLoad(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Messages: []model.Message{
			{ID: "b", Username: "alice", Text: "two", At: base.Add(time.Minute)},
			{ID: "c", Username: "alice", Text: "three", At: base.Add(2 * time.Minute)},
		},
	})
	m.focus = focusMessages
	m, _ = m.Update(press("k")) // selects "c"
	if m.cursorMsgID != "c" {
		t.Fatalf("selected %q, want c", m.cursorMsgID)
	}

	// Paging older shifts every index; the selection must follow the message.
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Messages: []model.Message{
			{ID: "a", Username: "alice", Text: "one", At: base},
			{ID: "b", Username: "alice", Text: "two", At: base.Add(time.Minute)},
			{ID: "c", Username: "alice", Text: "three", At: base.Add(2 * time.Minute)},
		},
	})
	if m.msgCursor != 2 || m.cursorMsgID != "c" {
		t.Errorf("cursor = %d (%q), want index 2 and id c", m.msgCursor, m.cursorMsgID)
	}
}

func TestChatEnterOnMessageOpensThread(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Messages: []model.Message{
			{ID: "parent", Username: "alice", Author: "Alice", Text: "let's discuss",
				At: time.Now().Add(-time.Minute), ThreadCount: 2},
		},
	})
	m.focus = focusMessages
	m, _ = m.Update(press("k"))
	m, _ = m.Update(press("enter"))

	if m.mode != bodyThread {
		t.Fatalf("mode = %v, want thread", m.mode)
	}
	if m.threadID != "parent" {
		t.Errorf("thread id = %q, want parent", m.threadID)
	}

	m = event(m, app.ThreadUpdated{
		RoomID:   "r1",
		ThreadID: "parent",
		Parent:   model.Message{ID: "parent", Username: "alice", Author: "Alice", Text: "let's discuss", At: time.Now()},
		Replies: []model.Message{
			{ID: "reply", Username: "bob", Author: "Bob", Text: "good idea", At: time.Now()},
		},
	})

	view := m.View()
	for _, want := range []string{"let's discuss", "good idea", "thread"} {
		if !strings.Contains(view, want) {
			t.Errorf("thread view missing %q:\n%s", want, view)
		}
	}

	// Escape returns to the timeline.
	m, _ = m.Update(press("esc"))
	if m.mode != bodyTimeline || m.threadID != "" {
		t.Errorf("esc did not close the thread (mode=%v, id=%q)", m.mode, m.threadID)
	}
}

func TestChatThreadListToggle(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.ThreadListUpdated{
		RoomID: "r1",
		Threads: []model.Message{
			{ID: "t1", Username: "alice", Author: "Alice", Text: "design review",
				ThreadCount: 4, At: time.Now().Add(-time.Hour), ThreadLastAt: time.Now()},
		},
	})
	m.focus = focusMessages

	m, _ = m.Update(press("t"))
	if m.mode != bodyThreadList {
		t.Fatalf("mode = %v, want thread list", m.mode)
	}
	if !strings.Contains(m.View(), "design review") {
		t.Errorf("thread list not rendered:\n%s", m.View())
	}

	// Enter opens the highlighted thread.
	m, _ = m.Update(press("enter"))
	if m.mode != bodyThread || m.threadID != "t1" {
		t.Errorf("mode=%v id=%q, want thread t1", m.mode, m.threadID)
	}
}

func TestChatComposerSendsAndClears(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer
	m.composer.Focus()

	for _, r := range "hello" {
		m, _ = m.Update(press(string(r)))
	}
	if m.composer.Value() != "hello" {
		t.Fatalf("composer = %q", m.composer.Value())
	}
	if !strings.Contains(m.View(), "hello") {
		t.Error("typed text should be visible")
	}

	m, _ = m.Update(press("enter"))
	if m.composer.Value() != "" {
		t.Errorf("composer should be cleared after send, got %q", m.composer.Value())
	}
}

func TestChatComposerIgnoresEmptySend(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	for _, r := range "   " {
		m, _ = m.Update(press(string(r)))
	}
	m, _ = m.Update(press("enter"))
	// Whitespace-only input is not a message; the composer keeps what was typed
	// rather than silently posting blanks.
	if m.composer.Value() == "" {
		t.Error("whitespace-only input should not have been sent and cleared")
	}
}

func TestChatComposerCharactersAreNotTreatedAsCommands(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	// "?" opens help only outside the composer; "/" must not start a room filter.
	for _, r := range "?/tq" {
		m, _ = m.Update(press(string(r)))
	}
	if m.showHelp {
		t.Error("typing ? in the composer should not open help")
	}
	if m.filtering {
		t.Error("typing / in the composer should not start filtering")
	}
	if m.composer.Value() != "?/tq" {
		t.Errorf("composer = %q, want %q", m.composer.Value(), "?/tq")
	}
}

func TestChatShowsTypingIndicatorForOpenRoomOnly(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})

	m = event(m, app.TypingUpdated{RoomID: "r2", Users: model.TypingUsers{"zoe"}})
	if strings.Contains(m.View(), "zoe is typing") {
		t.Error("another room's typing indicator must not show")
	}

	m = event(m, app.TypingUpdated{RoomID: "r1", Users: model.TypingUsers{"alice", "bob"}})
	if !strings.Contains(m.View(), "alice and bob are typing") {
		t.Errorf("typing indicator missing:\n%s", m.View())
	}

	m = event(m, app.TypingUpdated{RoomID: "r1", Users: nil})
	if strings.Contains(m.View(), "typing") {
		t.Error("indicator should clear when everyone stops")
	}
}

func TestChatStatusBarReflectsConnection(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})

	m = event(m, app.StatusChanged{Connection: rocket.Connected})
	if !strings.Contains(m.View(), "connected") {
		t.Error("status bar should show a connected state")
	}

	m = event(m, app.StatusChanged{Connection: rocket.Disconnected, Syncing: true})
	view := m.View()
	if !strings.Contains(view, "disconnected") {
		t.Error("status bar should show a disconnected state")
	}
	if !strings.Contains(view, "syncing") {
		t.Error("status bar should show sync activity")
	}
}

func TestChatNoticeIsShownAndCleared(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})

	m = event(m, app.Notice{Text: "something went wrong", IsErr: true})
	if !strings.Contains(m.View(), "something went wrong") {
		t.Error("notice should be visible in the status bar")
	}

	m, _ = m.Update(clearNoticeMsg{})
	if strings.Contains(m.View(), "something went wrong") {
		t.Error("notice should have been cleared")
	}
}

func TestChatHelpOverlayToggles(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusRooms

	m, _ = m.Update(press("?"))
	if !m.showHelp {
		t.Fatal("expected help to be shown")
	}
	if !strings.Contains(m.View(), "Keys") {
		t.Errorf("help overlay not rendered:\n%s", m.View())
	}

	m, _ = m.Update(press("?"))
	if m.showHelp {
		t.Error("expected help to toggle off")
	}
}

func TestChatHeaderShowsUnreadTotals(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{
		Rooms:  sampleRooms(),
		Totals: app.Totals{Unread: 4, Mentions: 2, UnreadRooms: 2},
	})
	view := m.View()
	if !strings.Contains(view, "@2") {
		t.Error("header should show the mention count")
	}
	if !strings.Contains(view, "2 unread") {
		t.Error("header should show the unread room count")
	}
}

func TestChatViewFitsTerminalAtManySizes(t *testing.T) {
	sizes := []struct{ w, h int }{{100, 30}, {80, 24}, {60, 15}, {40, 10}, {200, 60}}
	for _, size := range sizes {
		m := newTestChat(t)
		m, _ = m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = event(m, app.RoomsUpdated{Rooms: sampleRooms(), Totals: app.Totals{Unread: 3}})
		m = event(m, app.TimelineUpdated{
			RoomID: "r1",
			Messages: []model.Message{{
				ID: "a", Username: "alice", Author: "Alice",
				Text: strings.Repeat("a fairly long message that needs wrapping ", 5),
				At:   time.Now(),
			}},
		})
		m = event(m, app.TypingUpdated{RoomID: "r1", Users: model.TypingUsers{"bob"}})

		lines := strings.Split(m.View(), "\n")
		if len(lines) > size.h {
			t.Errorf("%dx%d produced %d lines, want <= %d", size.w, size.h, len(lines), size.h)
		}
		for i, line := range lines {
			if render.Width(line) > size.w {
				t.Errorf("%dx%d line %d is %d cells wide, want <= %d",
					size.w, size.h, i, render.Width(line), size.w)
			}
		}
	}
}

func TestChatReadOnlyRoomBlocksSending(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Room:   model.Room{ID: "r1", DisplayName: "announcements", ReadOnly: true},
	})
	m.focus = focusComposer
	for _, r := range "hello" {
		m, _ = m.Update(press(string(r)))
	}
	m, _ = m.Update(press("enter"))

	if m.composer.Value() != "hello" {
		t.Error("a read-only room must not consume the composer contents")
	}
	if !strings.Contains(m.View(), "read-only") {
		t.Errorf("read-only notice missing:\n%s", m.View())
	}
}

func TestChatShowsUnreadHintOnlyWhenDividerIsOffScreen(t *testing.T) {
	lastSeen := time.Now().Add(-time.Hour)
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})

	var messages []model.Message
	for i := 0; i < 60; i++ {
		messages = append(messages, model.Message{
			ID:       "old" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Username: "alice", Author: "Alice", Text: "backlog",
			At: lastSeen.Add(time.Duration(i-60) * time.Minute),
		})
	}
	for i := 0; i < 40; i++ {
		messages = append(messages, model.Message{
			ID:       "new" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Username: "bob", Author: "Bob", Text: "unread message",
			At: lastSeen.Add(time.Duration(i+1) * time.Minute),
		})
	}

	m = event(m, app.TimelineUpdated{
		RoomID: "r1", Messages: messages, UnreadFrom: lastSeen, UnreadCount: 40,
	})

	// Opening scrolls to the divider, so no hint is needed.
	if strings.Contains(m.View(), "new messages · u to jump") {
		t.Error("hint should be hidden while the divider is visible")
	}

	// Scroll to the bottom, leaving the divider far above.
	m.focus = focusMessages
	m, _ = m.Update(press("G"))
	if m.body.UnreadLine >= m.scroll {
		t.Skip("divider still on screen at this terminal size")
	}
	if !strings.Contains(m.View(), "40 new messages") {
		t.Errorf("expected an unread jump hint:\n%s", m.View())
	}

	// "u" jumps back to the divider and the hint goes away.
	m, _ = m.Update(press("u"))
	if m.body.UnreadLine < m.scroll || m.body.UnreadLine >= m.scroll+m.bodyHeight() {
		t.Error("u should scroll the divider into view")
	}
	if strings.Contains(m.View(), "u to jump") {
		t.Error("hint should clear once the divider is visible again")
	}
}

// When the server flags a room unread without a count, the hint must not invent
// a number.
func TestChatUnreadHintOmitsCountWhenServerGivesNone(t *testing.T) {
	lastSeen := time.Now().Add(-30 * 24 * time.Hour)
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})

	var messages []model.Message
	for i := 0; i < 80; i++ {
		messages = append(messages, model.Message{
			ID:       "m" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Username: "bob", Author: "Bob", Text: "a message",
			At: lastSeen.Add(time.Duration(i+1) * time.Minute),
		})
	}
	m = event(m, app.TimelineUpdated{
		RoomID: "r1", Messages: messages, UnreadFrom: lastSeen, UnreadCount: 0,
	})

	m.focus = focusMessages
	m, _ = m.Update(press("G")) // scroll to the bottom, divider far above
	if m.body.UnreadLine >= m.scroll {
		t.Skip("divider still visible at this terminal size")
	}

	view := m.View()
	if !strings.Contains(view, "new messages · u to jump") {
		t.Errorf("expected an unnumbered hint:\n%s", view)
	}
	// The page held 80 messages; none of that may leak out as a count.
	for _, wrong := range []string{"80 new", "60 new", "40 new"} {
		if strings.Contains(view, wrong) {
			t.Errorf("hint invented a count (%s):\n%s", wrong, view)
		}
	}
}

// Opening a room focuses the composer, so a bare letter key can never reach the
// thread list: it just gets typed. Threads need a binding that works while the
// composer has focus.
func TestThreadListReachableWhileComposing(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	if m.focus != focusComposer {
		t.Fatalf("focus after opening a room = %v, want composer", m.focus)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if m.mode != bodyThreadList {
		t.Errorf("ctrl+t from the composer did not open the thread list (mode=%v)", m.mode)
	}
	if m.composer.Value() != "" {
		t.Errorf("the shortcut leaked into the composer: %q", m.composer.Value())
	}
}

func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

func wheel(x, y int, up bool) tea.MouseMsg {
	button := tea.MouseButtonWheelDown
	if up {
		button = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{X: x, Y: y, Button: button, Action: tea.MouseActionPress}
}

func TestMouseClickOnSidebarOpensRoom(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	if m.activeRoom != "r1" {
		t.Fatalf("expected r1 open, got %q", m.activeRoom)
	}

	// Sidebar rows start one line below the title, which sits at the top of the
	// body area.
	m, _ = m.Update(click(3, headerRows+render.SidebarHeaderRows+1))
	if m.activeRoom != "r2" {
		t.Errorf("clicking the second room opened %q, want r2", m.activeRoom)
	}
	if m.roomCursor != 1 {
		t.Errorf("room cursor = %d, want 1", m.roomCursor)
	}
}

func TestMouseClickOnAlreadyOpenRoomDoesNotReload(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID:   "r1",
		Messages: []model.Message{{ID: "a", Username: "alice", Text: "hi", At: time.Now()}},
	})

	m, _ = m.Update(click(3, headerRows+render.SidebarHeaderRows))
	if m.activeRoom != "r1" {
		t.Errorf("active room changed to %q", m.activeRoom)
	}
	if len(m.messages) != 1 {
		t.Error("clicking the open room should not clear its loaded messages")
	}
	if m.focus != focusRooms {
		t.Errorf("focus = %v, want rooms", m.focus)
	}
}

func TestMouseClickOnMessageSelectsIt(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Messages: []model.Message{
			{ID: "a", Username: "alice", Author: "Alice", Text: "first", At: base},
			{ID: "b", Username: "bob", Author: "Bob", Text: "second", At: base.Add(time.Hour)},
		},
	})

	// Click the line where the second message begins.
	target := m.body.MessageLine[1]
	m, _ = m.Update(click(m.sidebarWidth()+4, headerRows+target-m.scroll))

	if m.cursorMsgID != "b" {
		t.Errorf("selected %q, want b", m.cursorMsgID)
	}
	if m.focus != focusMessages {
		t.Errorf("focus = %v, want messages", m.focus)
	}
}

func TestMouseClickOnThreadHintOpensThread(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Messages: []model.Message{{
			ID: "parent", Username: "alice", Author: "Alice", Text: "let's discuss",
			At: time.Now().Add(-time.Hour), ThreadCount: 3,
		}},
	})

	var hintLine = -1
	for line, index := range m.body.HintLine {
		if index == 0 {
			hintLine = line
		}
	}
	if hintLine < 0 {
		t.Fatal("no thread affordance was rendered")
	}

	m, _ = m.Update(click(m.sidebarWidth()+4, headerRows+hintLine-m.scroll))
	if m.mode != bodyThread {
		t.Errorf("mode = %v, want thread", m.mode)
	}
	if m.threadID != "parent" {
		t.Errorf("thread id = %q, want parent", m.threadID)
	}
}

func TestMouseWheelScrollsTheTimeline(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})

	var messages []model.Message
	for i := 0; i < 60; i++ {
		messages = append(messages, model.Message{
			ID:       "m" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Username: "alice", Author: "Alice", Text: "a message",
			At: time.Now().Add(time.Duration(i-60) * time.Minute),
		})
	}
	m = event(m, app.TimelineUpdated{RoomID: "r1", Messages: messages})

	atBottom := m.scroll
	m, _ = m.Update(wheel(m.sidebarWidth()+4, headerRows+2, true))
	if m.scroll >= atBottom {
		t.Errorf("wheel up did not scroll back (scroll %d -> %d)", atBottom, m.scroll)
	}
	scrolled := m.scroll
	m, _ = m.Update(wheel(m.sidebarWidth()+4, headerRows+2, false))
	if m.scroll <= scrolled {
		t.Errorf("wheel down did not scroll forward (scroll %d -> %d)", scrolled, m.scroll)
	}
}

func TestMouseClickOnComposerFocusesIt(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusRooms
	m.composer.Blur()

	composerRow := headerRows + m.bodyHeight() + 1
	m, _ = m.Update(click(5, composerRow))
	if m.focus != focusComposer {
		t.Errorf("focus = %v, want composer", m.focus)
	}
}

func TestMouseClicksOnChromeAreIgnored(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	before := m.activeRoom

	for _, point := range []struct{ x, y int }{
		{10, 0},                            // header
		{10, 1},                            // header rule
		{m.sidebarWidth(), headerRows + 2}, // the divider column
		{10, headerRows + m.bodyHeight()},  // typing line
		{10, m.height - 1},                 // status bar
	} {
		m, _ = m.Update(click(point.x, point.y))
	}
	if m.activeRoom != before {
		t.Errorf("a click on chrome changed the open room to %q", m.activeRoom)
	}
}

// --- emoji completion ---------------------------------------------------------

func TestComposerCompleterOpensWhileTyping(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	for _, r := range "nice :jo" {
		m, _ = m.Update(press(string(r)))
	}
	if m.picker.mode != pickerComplete {
		t.Fatalf("completer not open after typing %q (mode=%v)", ":jo", m.picker.mode)
	}
	if m.picker.query != "jo" {
		t.Errorf("query = %q, want jo", m.picker.query)
	}

	view := m.View()
	if !strings.Contains(view, ":joy:") || !strings.Contains(view, "😂") {
		t.Errorf("suggestion list missing joy:\n%s", view)
	}
}

func TestComposerCompleterStaysClosedForOrdinaryColons(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	// A time, and a URL: neither should summon the list.
	for _, r := range "standup at 10:30" {
		m, _ = m.Update(press(string(r)))
	}
	if m.picker.active() {
		t.Errorf("completer opened on a timestamp (query=%q)", m.picker.query)
	}

	m.composer.Reset()
	m.picker.close()
	for _, r := range "see http://x" {
		m, _ = m.Update(press(string(r)))
	}
	if m.picker.active() {
		t.Errorf("completer opened inside a URL (query=%q)", m.picker.query)
	}
}

func TestComposerCompleterClosesWhenNothingMatches(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	for _, r := range ":jo" {
		m, _ = m.Update(press(string(r)))
	}
	if !m.picker.active() {
		t.Fatal("expected the completer to be open")
	}
	for _, r := range "zzzqqq" {
		m, _ = m.Update(press(string(r)))
	}
	if m.picker.active() {
		t.Error("completer stayed open with no matches")
	}
}

func TestComposerCompletionInsertsGlyph(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	for _, r := range "shipped :tada" {
		m, _ = m.Update(press(string(r)))
	}
	if !m.picker.active() {
		t.Fatal("completer not open")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})

	if got := m.composer.Value(); got != "shipped 🎉 " {
		t.Errorf("composer = %q, want %q", got, "shipped 🎉 ")
	}
	if m.picker.active() {
		t.Error("completer should close after accepting")
	}
}

func TestComposerCompleterNavigationAndDismiss(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	for _, r := range ":sm" {
		m, _ = m.Update(press(string(r)))
	}
	if !m.picker.active() {
		t.Fatal("completer not open")
	}
	first, _ := m.picker.selected()
	m, _ = m.Update(press("down"))
	second, _ := m.picker.selected()
	if first.Name == second.Name {
		t.Error("down did not move the selection")
	}

	// Escape dismisses without touching the text.
	before := m.composer.Value()
	m, _ = m.Update(press("esc"))
	if m.picker.active() {
		t.Error("esc did not dismiss the completer")
	}
	if m.composer.Value() != before {
		t.Errorf("esc altered the composer: %q", m.composer.Value())
	}
}

func TestComposerEnterSendsWhenCompleterIsClosed(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	for _, r := range "plain message" {
		m, _ = m.Update(press(string(r)))
	}
	m, _ = m.Update(press("enter"))
	if m.composer.Value() != "" {
		t.Errorf("enter should have sent, composer = %q", m.composer.Value())
	}
}

// --- reactions ---------------------------------------------------------------

func reactableChat(t *testing.T) chatModel {
	t.Helper()
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Messages: []model.Message{{
			ID: "target", Username: "alice", Author: "Alice", Text: "nice work",
			At: time.Now().Add(-time.Minute),
			Reactions: []model.Reaction{
				{Emoji: ":+1:", Usernames: []string{"bob"}},
				{Emoji: ":tada:", Usernames: []string{"me"}, Mine: true},
			},
		}},
	})
	return m
}

func TestReactPickerOpensOnSelectedMessage(t *testing.T) {
	m := reactableChat(t)
	m.focus = focusMessages
	m, _ = m.Update(press("k")) // select the message
	m, _ = m.Update(press("r"))

	if m.picker.mode != pickerReact {
		t.Fatalf("react picker not open (mode=%v)", m.picker.mode)
	}
	if m.picker.target != "target" {
		t.Errorf("picker target = %q, want target", m.picker.target)
	}

	view := m.View()
	if !strings.Contains(view, "React") {
		t.Errorf("picker title missing:\n%s", view)
	}
	// The quick-reaction set should be offered before anything is typed.
	if len(m.picker.matches) == 0 {
		t.Error("no default suggestions offered")
	}
}

func TestReactPickerFiltersAndIsModal(t *testing.T) {
	m := reactableChat(t)
	m.focus = focusMessages
	m, _ = m.Update(press("k"))
	m, _ = m.Update(press("r"))

	for _, r := range "rocket" {
		m, _ = m.Update(press(string(r)))
	}
	if m.picker.query != "rocket" {
		t.Errorf("query = %q, want rocket", m.picker.query)
	}
	match, ok := m.picker.selected()
	if !ok || match.Glyph != "🚀" {
		t.Errorf("selected = %+v, want the rocket", match)
	}

	// Typing must not leak into the composer while the picker is modal.
	if m.composer.Value() != "" {
		t.Errorf("composer captured picker input: %q", m.composer.Value())
	}

	m, _ = m.Update(press("esc"))
	if m.picker.active() {
		t.Error("esc did not close the picker")
	}
}

func TestReactPickerWithNoSelectionExplainsItself(t *testing.T) {
	m := reactableChat(t)
	m.focus = focusMessages
	// No message selected yet.
	m, _ = m.Update(press("r"))

	if m.picker.active() {
		t.Error("picker opened with nothing selected")
	}
	if !strings.Contains(m.View(), "select a message first") {
		t.Errorf("expected an explanation in the status bar:\n%s", m.View())
	}
}

func TestAlreadyReactedDetectsOwnReaction(t *testing.T) {
	m := reactableChat(t)
	if !m.alreadyReacted("target", "tada") {
		t.Error("own reaction not detected")
	}
	if m.alreadyReacted("target", "+1") {
		t.Error("someone else's reaction counted as ours")
	}
	if m.alreadyReacted("target", "joy") {
		t.Error("absent reaction reported as ours")
	}
	if m.alreadyReacted("nope", "tada") {
		t.Error("unknown message reported as reacted")
	}
}

func TestClickOnReactionChipTogglesIt(t *testing.T) {
	m := reactableChat(t)

	var line int = -1
	for at := range m.body.ReactionLine {
		line = at
	}
	if line < 0 {
		t.Fatal("no reaction line rendered")
	}
	spans := m.body.ReactionSpans[line]
	if len(spans) < 2 {
		t.Fatalf("expected two reaction chips, got %d", len(spans))
	}

	// Click inside the first chip; it should select the message, not open a
	// thread or scroll.
	const gutter = 2
	x := m.sidebarWidth() + 1 + spans[0].Start + gutter
	m, _ = m.Update(click(x, headerRows+line-m.scroll))

	if m.mode != bodyTimeline {
		t.Errorf("clicking a reaction changed the pane mode to %v", m.mode)
	}
	if m.cursorMsgID != "target" {
		t.Errorf("selection = %q, want target", m.cursorMsgID)
	}
}

func TestClickBesideReactionsStillSelectsTheMessage(t *testing.T) {
	m := reactableChat(t)
	var line int = -1
	for at := range m.body.ReactionLine {
		line = at
	}
	if line < 0 {
		t.Fatal("no reaction line rendered")
	}

	// Far to the right of the chips: no reaction there, so it is an ordinary
	// message click.
	m, _ = m.Update(click(m.sidebarWidth()+1+60, headerRows+line-m.scroll))
	if m.cursorMsgID != "target" {
		t.Errorf("selection = %q, want target", m.cursorMsgID)
	}
}

func TestChatMarkUnreadFromTheRoomList(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusRooms
	m, _ = m.Update(press("j")) // move to the DM with alice

	m, _ = m.Update(press("U"))

	if !strings.Contains(m.notice, "alice") {
		t.Errorf("notice = %q, want it to name the room under the cursor", m.notice)
	}
	// Marking unread must not open the room: the point is to leave it alone.
	if m.activeRoom != "r1" {
		t.Errorf("active room = %q, want the originally open r1", m.activeRoom)
	}
}

func TestChatMarkUnreadFromASelectedMessage(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Messages: []model.Message{
			{ID: "a", Username: "alice", Text: "one", At: time.Now().Add(-2 * time.Minute)},
			{ID: "b", Username: "alice", Text: "two", At: time.Now().Add(-time.Minute)},
		},
	})
	m.focus = focusMessages
	m, _ = m.Update(press("k")) // select "b"

	m, _ = m.Update(press("U"))
	if !strings.Contains(m.notice, "from here") {
		t.Errorf("notice = %q, want the per-message wording", m.notice)
	}

	// u still jumps to the divider; the two are a case apart and must stay apart.
	m.notice = ""
	m, _ = m.Update(press("u"))
	if m.notice != "" {
		t.Errorf("u marked unread instead of jumping: %q", m.notice)
	}
}

func TestChatMarkUnreadFallsBackToTheRoomWithNoSelection(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Room:   model.Room{ID: "r1", Name: "general", DisplayName: "general", Kind: model.KindChannel},
		Messages: []model.Message{
			{ID: "a", Username: "alice", Text: "one", At: time.Now().Add(-time.Minute)},
		},
	})
	m.focus = focusMessages // cursor is on no message

	m, _ = m.Update(press("U"))
	if !strings.Contains(m.notice, "general") || strings.Contains(m.notice, "from here") {
		t.Errorf("notice = %q, want the whole room marked unread", m.notice)
	}
}

func TestChatRefusesToMarkUnreadFromYourOwnMessage(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Messages: []model.Message{
			{ID: "mine", Username: "tester", Text: "my own words", Own: true,
				At: time.Now().Add(-time.Minute)},
		},
	})
	m.focus = focusMessages
	m, _ = m.Update(press("k"))

	// The server refuses this, so the user hears why instead of watching the
	// divider appear and then vanish when the round trip comes back.
	m, _ = m.Update(press("U"))
	if !strings.Contains(m.notice, "own message") {
		t.Errorf("notice = %q, want the refusal", m.notice)
	}
}

// ---- editing ----------------------------------------------------------------

// editableTimeline is a room where two of the four messages are the user's own,
// so recall has to skip past other people and system lines to find them.
func editableTimeline() app.TimelineUpdated {
	base := time.Now().Add(-time.Hour)
	return app.TimelineUpdated{
		RoomID: "r1",
		Room:   model.Room{ID: "r1", DisplayName: "general", Kind: model.KindChannel},
		Messages: []model.Message{
			{ID: "m1", Username: "tester", Text: "first of mine", Own: true, At: base},
			{ID: "m2", Username: "alice", Author: "Alice", Text: "not yours", At: base.Add(time.Minute)},
			{ID: "m3", Username: "tester", Text: "joined the channel", Own: true,
				SystemType: "uj", At: base.Add(2 * time.Minute)},
			{ID: "m4", Username: "tester", Text: "second of mine", Own: true,
				At: base.Add(3 * time.Minute)},
		},
	}
}

func composing(t *testing.T) chatModel {
	t.Helper()
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, editableTimeline())
	m.focus = focusComposer
	m.composer.Focus()
	return m
}

func TestComposerUpRecallsOwnMessagesNewestFirst(t *testing.T) {
	m := composing(t)

	m, _ = m.Update(press("up"))
	if m.editID != "m4" || m.composer.Value() != "second of mine" {
		t.Fatalf("first ↑ edited %q with %q, want m4", m.editID, m.composer.Value())
	}
	// The message being rewritten is pointed at in the timeline, not just held
	// in the box.
	if m.cursorMsgID != "m4" {
		t.Errorf("timeline selection = %q, want m4", m.cursorMsgID)
	}

	// Again steps back past someone else's message and past a system line.
	m, _ = m.Update(press("up"))
	if m.editID != "m1" || m.composer.Value() != "first of mine" {
		t.Fatalf("second ↑ edited %q with %q, want m1", m.editID, m.composer.Value())
	}

	// There is nothing older of ours; the oldest stays loaded rather than the
	// composer emptying itself.
	m, _ = m.Update(press("up"))
	if m.editID != "m1" || m.composer.Value() != "first of mine" {
		t.Errorf("↑ past the oldest changed the edit to %q/%q", m.editID, m.composer.Value())
	}
}

func TestComposerDownWalksBackAndLeavesEditMode(t *testing.T) {
	m := composing(t)
	m, _ = m.Update(press("up"))
	m, _ = m.Update(press("up")) // on m1

	m, _ = m.Update(press("down"))
	if m.editID != "m4" {
		t.Fatalf("↓ moved to %q, want m4", m.editID)
	}

	m, _ = m.Update(press("down"))
	if m.editing() {
		t.Errorf("↓ past the newest should leave edit mode, still on %q", m.editID)
	}
	if m.composer.Value() != "" {
		t.Errorf("composer = %q, want the empty draft back", m.composer.Value())
	}
}

func TestComposerUpKeepsATypedDraft(t *testing.T) {
	m := composing(t)
	for _, r := range "half a thought" {
		m, _ = m.Update(press(string(r)))
	}

	m, _ = m.Update(press("up"))
	if m.editing() {
		t.Error("↑ with a draft in the box must not recall a message over it")
	}
	if m.composer.Value() != "half a thought" {
		t.Errorf("composer = %q, want the draft untouched", m.composer.Value())
	}
}

func TestEscapeCancelsAnEditWithoutClosingTheThread(t *testing.T) {
	m := composing(t)
	m, _ = m.Update(press("up"))
	for _, r := range " more" {
		m, _ = m.Update(press(string(r)))
	}

	m, _ = m.Update(press("esc"))
	if m.editing() {
		t.Error("esc should leave edit mode")
	}
	if m.composer.Value() != "" {
		t.Errorf("composer = %q, want the edit discarded", m.composer.Value())
	}
	// esc while editing means "leave that message alone", not "leave the room".
	if m.focus != focusComposer {
		t.Errorf("focus = %v, want the composer to keep it", m.focus)
	}
}

func TestEditRefusesToSaveNothing(t *testing.T) {
	m := composing(t)
	m, _ = m.Update(press("up"))
	m.composer.SetValue("")

	m, _ = m.Update(press("enter"))
	if !m.editing() {
		t.Error("an empty edit should be refused, not committed")
	}
	if !strings.Contains(m.notice, "empty") {
		t.Errorf("notice = %q, want an explanation", m.notice)
	}
}

func TestEditCommitClearsTheComposer(t *testing.T) {
	m := composing(t)
	m, _ = m.Update(press("up"))
	m.composer.SetValue("second of mine, corrected")

	m, _ = m.Update(press("enter"))
	if m.editing() {
		t.Error("committing should leave edit mode")
	}
	if m.composer.Value() != "" {
		t.Errorf("composer = %q, want it clear for the next message", m.composer.Value())
	}
	if !strings.Contains(m.notice, "saved") {
		t.Errorf("notice = %q, want confirmation", m.notice)
	}
}

func TestEditModeIsVisible(t *testing.T) {
	m := composing(t)
	if strings.Contains(m.View(), "editing a sent message") {
		t.Fatal("the edit banner is showing before anything is being edited")
	}

	m, _ = m.Update(press("up"))
	view := m.View()
	if !strings.Contains(view, "editing a sent message") {
		t.Errorf("no edit banner while editing:\n%s", view)
	}
	if !strings.Contains(view, "esc cancel") {
		t.Errorf("status bar does not say how to get out:\n%s", view)
	}
}

// The composer inside a thread posts into that thread, so recall there has to
// stay inside it. Reaching a channel message would mean editing something that
// is not even on screen.
func TestEditInAThreadRecallsThreadMessagesOnly(t *testing.T) {
	m := composing(t)
	m.mode = bodyThread
	m.threadID = "m4"
	m = event(m, app.ThreadUpdated{
		RoomID:   "r1",
		ThreadID: "m4",
		Parent:   model.Message{ID: "m4", Username: "tester", Text: "second of mine", Own: true, At: time.Now()},
		Replies: []model.Message{
			{ID: "t1", Username: "alice", Author: "Alice", Text: "their reply", At: time.Now()},
			{ID: "t2", Username: "tester", Text: "my reply", Own: true, At: time.Now()},
		},
	})

	m, _ = m.Update(press("up"))
	if m.editID != "t2" || m.composer.Value() != "my reply" {
		t.Fatalf("↑ in a thread edited %q/%q, want the newest reply of ours",
			m.editID, m.composer.Value())
	}

	// Further back is the thread parent, which heads the pane and so is on
	// screen — and then nothing. The room's other messages are out of reach.
	m, _ = m.Update(press("up"))
	if m.editID != "m4" {
		t.Fatalf("↑ again edited %q, want the thread parent", m.editID)
	}
	m, _ = m.Update(press("up"))
	if m.editID != "m4" {
		t.Errorf("↑ escaped the thread and reached %q", m.editID)
	}
}

func TestThreadListOffersNothingToEdit(t *testing.T) {
	m := composing(t)
	m = event(m, app.ThreadListUpdated{
		RoomID: "r1",
		Threads: []model.Message{
			{ID: "m4", Username: "tester", Text: "second of mine", Own: true,
				ThreadCount: 2, At: time.Now()},
		},
	})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if m.mode != bodyThreadList {
		t.Fatalf("mode = %v, want the thread list", m.mode)
	}

	m, _ = m.Update(press("up"))
	if m.editing() {
		t.Errorf("the thread list recalled %q; its rows are not what the composer addresses", m.editID)
	}
}

func TestLeavingTheRoomAbandonsAnEdit(t *testing.T) {
	m := composing(t)
	m, _ = m.Update(press("up"))

	m.openRoom("r2")
	if m.editing() {
		t.Errorf("an edit survived the move to another room: %q", m.editID)
	}
	if m.composer.Value() != "" {
		t.Errorf("composer = %q, want a fresh box in the new room", m.composer.Value())
	}
}

func TestEditIsAbandonedWhenTheMessageGoesAway(t *testing.T) {
	m := composing(t)
	m, _ = m.Update(press("up"))

	// The message is deleted elsewhere and the timeline comes back without it.
	gone := editableTimeline()
	gone.Messages = gone.Messages[:3]
	m = event(m, gone)

	if m.editing() {
		t.Errorf("still editing %q after it left the timeline", m.editID)
	}
	if !strings.Contains(m.notice, "no longer here") {
		t.Errorf("notice = %q, want an explanation", m.notice)
	}
}

func TestReadOnlyRoomHasNothingToEdit(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Room:   model.Room{ID: "r1", DisplayName: "announcements", ReadOnly: true},
		Messages: []model.Message{
			{ID: "m1", Username: "tester", Text: "posted before it locked", Own: true,
				At: time.Now().Add(-time.Hour)},
		},
	})
	m.focus = focusComposer
	m.composer.Focus()

	m, _ = m.Update(press("up"))
	if m.editing() {
		t.Error("a read-only room accepts no edits, so it should offer none")
	}
}
