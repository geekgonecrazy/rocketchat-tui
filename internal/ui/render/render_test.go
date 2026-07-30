package render

import (
	"strings"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// plainTheme strips styling so assertions can look at text, not escape codes.
func plainTheme() Theme { return Theme{} }

func TestWrapRespectsWidthAndWordBoundaries(t *testing.T) {
	lines := Wrap("the quick brown fox jumps over the lazy dog", 12)
	for _, line := range lines {
		if Width(line) > 12 {
			t.Errorf("line %q is %d cells wide, want <= 12", line, Width(line))
		}
	}
	if joined := strings.Join(lines, " "); joined != "the quick brown fox jumps over the lazy dog" {
		t.Errorf("wrapping lost or added text: %q", joined)
	}
}

func TestWrapHardSplitsOverlongWords(t *testing.T) {
	lines := Wrap(strings.Repeat("x", 25)+" tail", 10)
	for _, line := range lines {
		if Width(line) > 10 {
			t.Errorf("line %q exceeds 10 cells", line)
		}
	}
	if !strings.Contains(strings.Join(lines, ""), "tail") {
		t.Error("trailing word was dropped")
	}
}

func TestWrapPreservesBlankLines(t *testing.T) {
	lines := Wrap("first\n\nsecond", 20)
	if len(lines) != 3 || lines[0] != "first" || lines[1] != "" || lines[2] != "second" {
		t.Errorf("got %q", lines)
	}
}

func TestWrapHandlesEmptyAndZeroWidth(t *testing.T) {
	if lines := Wrap("", 10); len(lines) != 1 || lines[0] != "" {
		t.Errorf("empty text = %q", lines)
	}
	if lines := Wrap("anything", 0); len(lines) != 1 || lines[0] != "" {
		t.Errorf("zero width = %q", lines)
	}
}

func TestTruncateAndPad(t *testing.T) {
	if got := Truncate("hello world", 8); Width(got) != 8 {
		t.Errorf("Truncate width = %d (%q), want 8", Width(got), got)
	}
	if got := Truncate("hi", 8); got != "hi" {
		t.Errorf("Truncate should leave short strings alone, got %q", got)
	}
	if got := Pad("hi", 5); got != "hi   " {
		t.Errorf("Pad = %q", got)
	}
	if got := Pad("too long here", 5); Width(got) != 5 {
		t.Errorf("Pad should truncate, got %q", got)
	}
}

func TestWidthIgnoresANSI(t *testing.T) {
	styled := "\x1b[31mred\x1b[0m"
	if Width(styled) != 3 {
		t.Errorf("Width(%q) = %d, want 3", styled, Width(styled))
	}
}

func TestRuleCentresLabel(t *testing.T) {
	rule := Rule("new messages", 40, "-")
	if Width(rule) != 40 {
		t.Errorf("rule width = %d, want 40", Width(rule))
	}
	if !strings.Contains(rule, "new messages") {
		t.Errorf("rule lost its label: %q", rule)
	}
	// A label wider than the rule must not overflow the line.
	narrow := Rule("a very long label indeed", 8, "-")
	if Width(narrow) > 8 {
		t.Errorf("narrow rule width = %d, want <= 8", Width(narrow))
	}
}

func TestTimelinePlacesUnreadDivider(t *testing.T) {
	lastSeen := time.Now().Add(-time.Hour)
	messages := []model.Message{
		{ID: "a", Username: "alice", Author: "Alice", Text: "seen already",
			At: lastSeen.Add(-time.Minute)},
		{ID: "b", Username: "bob", Author: "Bob", Text: "brand new",
			At: lastSeen.Add(time.Minute)},
	}

	view := Timeline(plainTheme(), TimelineState{
		Messages:   messages,
		UnreadFrom: lastSeen,
		Width:      40,
		Cursor:     -1,
	})

	if view.UnreadLine < 0 {
		t.Fatal("expected an unread divider")
	}
	if !strings.Contains(view.Lines[view.UnreadLine], "new messages") {
		t.Errorf("divider line = %q", view.Lines[view.UnreadLine])
	}

	// The divider must sit between the two messages.
	if view.UnreadLine <= view.MessageLine[0] {
		t.Error("divider should come after the already-seen message")
	}
	if view.UnreadLine > view.MessageLine[1] {
		t.Error("divider should come before the new message")
	}
}

func TestTimelineOmitsDividerWhenNothingIsNew(t *testing.T) {
	lastSeen := time.Now()
	view := Timeline(plainTheme(), TimelineState{
		Messages: []model.Message{
			{ID: "a", Username: "alice", Text: "old", At: lastSeen.Add(-time.Hour)},
		},
		UnreadFrom: lastSeen,
		Width:      40,
		Cursor:     -1,
	})
	if view.UnreadLine != -1 {
		t.Errorf("unexpected divider at line %d", view.UnreadLine)
	}
}

func TestTimelineIgnoresOwnMessagesForDivider(t *testing.T) {
	lastSeen := time.Now().Add(-time.Hour)
	view := Timeline(plainTheme(), TimelineState{
		Messages: []model.Message{
			// Our own message is newer than last-seen but must not raise a divider:
			// you have obviously read what you just sent.
			{ID: "mine", Username: "me", Text: "sent by me",
				At: lastSeen.Add(time.Minute), Own: true},
		},
		UnreadFrom: lastSeen,
		Width:      40,
		Cursor:     -1,
	})
	if view.UnreadLine != -1 {
		t.Errorf("own message triggered a divider at line %d", view.UnreadLine)
	}
}

func TestTimelineGroupsConsecutiveMessages(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	view := Timeline(plainTheme(), TimelineState{
		Messages: []model.Message{
			{ID: "a", Username: "alice", Author: "Alice", Text: "one", At: base},
			{ID: "b", Username: "alice", Author: "Alice", Text: "two", At: base.Add(time.Minute)},
			{ID: "c", Username: "bob", Author: "Bob", Text: "three", At: base.Add(2 * time.Minute)},
		},
		Width:  40,
		Cursor: -1,
	})

	rendered := strings.Join(view.Lines, "\n")
	if strings.Count(rendered, "Alice") != 1 {
		t.Errorf("expected Alice's header once, got:\n%s", rendered)
	}
	if strings.Count(rendered, "Bob") != 1 {
		t.Errorf("expected Bob's header once, got:\n%s", rendered)
	}
}

func TestTimelineSeparatesDistantMessagesFromSameAuthor(t *testing.T) {
	base := time.Now().Add(-3 * time.Hour)
	view := Timeline(plainTheme(), TimelineState{
		Messages: []model.Message{
			{ID: "a", Username: "alice", Author: "Alice", Text: "one", At: base},
			{ID: "b", Username: "alice", Author: "Alice", Text: "two", At: base.Add(time.Hour)},
		},
		Width:  40,
		Cursor: -1,
	})
	if count := strings.Count(strings.Join(view.Lines, "\n"), "Alice"); count != 2 {
		t.Errorf("expected two headers for messages an hour apart, got %d", count)
	}
}

func TestTimelineShowsThreadAndReactionAffordances(t *testing.T) {
	view := Timeline(plainTheme(), TimelineState{
		Messages: []model.Message{{
			ID: "a", Username: "alice", Author: "Alice", Text: "let's discuss",
			At: time.Now().Add(-time.Hour), ThreadCount: 3,
			ThreadLastAt: time.Now().Add(-time.Minute),
			Reactions:    []model.Reaction{{Emoji: ":+1:", Usernames: []string{"bob", "carol"}}},
			Attachments:  []model.Attachment{{Title: "report.pdf"}},
		}},
		Width:  50,
		Cursor: -1,
	})

	rendered := strings.Join(view.Lines, "\n")
	for _, want := range []string{"3 replies", ":+1: 2", "report.pdf"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected %q in:\n%s", want, rendered)
		}
	}
}

func TestTimelineRendersSystemMessages(t *testing.T) {
	view := Timeline(plainTheme(), TimelineState{
		Messages: []model.Message{
			{ID: "a", Username: "alice", Author: "Alice", SystemType: "uj", At: time.Now()},
		},
		Width:  40,
		Cursor: -1,
	})
	if !strings.Contains(strings.Join(view.Lines, "\n"), "Alice joined the channel") {
		t.Errorf("system message not rendered: %q", view.Lines)
	}
}

func TestTimelineLinesNeverExceedWidth(t *testing.T) {
	long := strings.Repeat("some quite long message text ", 10)
	view := Timeline(plainTheme(), TimelineState{
		Messages: []model.Message{
			{ID: "a", Username: "alice", Author: "Alice", Text: long, At: time.Now()},
		},
		Width:  30,
		Cursor: 0,
	})
	for _, line := range view.Lines {
		if Width(line) > 30 {
			t.Errorf("line exceeds width 30 (%d): %q", Width(line), line)
		}
	}
}

func TestTimelineEmptyState(t *testing.T) {
	view := Timeline(plainTheme(), TimelineState{Width: 30, Cursor: -1, Empty: "nothing here"})
	if !strings.Contains(strings.Join(view.Lines, "\n"), "nothing here") {
		t.Errorf("empty placeholder missing: %q", view.Lines)
	}
}

func TestSidebarRendersFixedHeightAndBadges(t *testing.T) {
	rooms := []model.Room{
		{ID: "r1", DisplayName: "general", Kind: model.KindChannel, Unread: 4},
		{ID: "r2", DisplayName: "alice", Kind: model.KindDirect, UserMentions: 2},
		{ID: "r3", DisplayName: "quiet", Kind: model.KindChannel},
	}
	lines := Sidebar(plainTheme(), SidebarState{
		Rooms: rooms, Cursor: 0, ActiveRoomID: "r1",
		Width: 24, Height: 8, Focused: true,
	})

	if len(lines) != 8 {
		t.Fatalf("got %d lines, want exactly 8", len(lines))
	}
	for i, line := range lines {
		if Width(line) != 24 {
			t.Errorf("line %d is %d cells wide, want 24: %q", i, Width(line), line)
		}
	}

	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "@2") {
		t.Error("mention badge missing")
	}
	if !strings.Contains(rendered, "4") {
		t.Error("unread badge missing")
	}
	if !strings.Contains(rendered, "@alice") {
		t.Error("DM should render with an @ prefix")
	}
}

func TestSidebarScrollsToKeepCursorVisible(t *testing.T) {
	var rooms []model.Room
	for i := 0; i < 20; i++ {
		rooms = append(rooms, model.Room{
			ID:          "r" + string(rune('a'+i)),
			DisplayName: "room-" + string(rune('a'+i)),
			Kind:        model.KindChannel,
		})
	}
	lines := Sidebar(plainTheme(), SidebarState{
		Rooms: rooms, Cursor: 19, Width: 20, Height: 6, Focused: true,
	})
	if !strings.Contains(strings.Join(lines, "\n"), "room-t") {
		t.Errorf("cursor room not visible:\n%s", strings.Join(lines, "\n"))
	}
}

func TestFilterRooms(t *testing.T) {
	rooms := []model.Room{
		{ID: "r1", DisplayName: "General", Name: "general"},
		{ID: "r2", DisplayName: "Engineering", Name: "eng"},
		{ID: "r3", DisplayName: "alice", Name: "alice"},
	}
	if got := FilterRooms(rooms, "eng"); len(got) != 1 || got[0].ID != "r2" {
		t.Errorf("filter eng = %+v", got)
	}
	if got := FilterRooms(rooms, "GENERAL"); len(got) != 1 || got[0].ID != "r1" {
		t.Errorf("upper-case filter = %+v, want the General room", got)
	}
	if got := FilterRooms(rooms, "  ENG  "); len(got) != 1 || got[0].ID != "r2" {
		t.Errorf("filter should be trimmed and case-insensitive, got %+v", got)
	}
	if got := FilterRooms(rooms, ""); len(got) != 3 {
		t.Errorf("empty filter should pass everything, got %d", len(got))
	}
}

func TestSortRoomsOrdersByActivityAloneNotUnread(t *testing.T) {
	now := time.Now()
	rooms := []model.Room{
		{ID: "quiet-recent", DisplayName: "quiet", LastMessageAt: now},
		{ID: "unread", DisplayName: "unread", Unread: 1, LastMessageAt: now.Add(-time.Hour)},
		{ID: "mention", DisplayName: "mention", UserMentions: 1, LastMessageAt: now.Add(-2 * time.Hour)},
	}
	model.SortRooms(rooms)
	// Strictly most-recent-first: unread and mentions must not reorder anything,
	// or reading a room would move it.
	if rooms[0].ID != "quiet-recent" || rooms[1].ID != "unread" || rooms[2].ID != "mention" {
		t.Errorf("unexpected order: %s, %s, %s", rooms[0].ID, rooms[1].ID, rooms[2].ID)
	}
}

func TestSortRoomsIsStableWhenUnreadClears(t *testing.T) {
	now := time.Now()
	build := func(unread int) []model.Room {
		return []model.Room{
			{ID: "a", DisplayName: "a", LastMessageAt: now.Add(-1 * time.Hour)},
			{ID: "b", DisplayName: "b", LastMessageAt: now.Add(-2 * time.Hour), Unread: unread, Alert: unread > 0},
			{ID: "c", DisplayName: "c", LastMessageAt: now.Add(-3 * time.Hour)},
		}
	}

	withUnread := build(3)
	model.SortRooms(withUnread)
	cleared := build(0)
	model.SortRooms(cleared)

	for i := range withUnread {
		if withUnread[i].ID != cleared[i].ID {
			t.Fatalf("reading a room reordered the sidebar: %s vs %s at %d",
				withUnread[i].ID, cleared[i].ID, i)
		}
	}
}

func TestTypingText(t *testing.T) {
	tests := []struct {
		users model.TypingUsers
		want  string
	}{
		{nil, ""},
		{model.TypingUsers{"alice"}, "alice is typing…"},
		{model.TypingUsers{"alice", "bob"}, "alice and bob are typing…"},
		{model.TypingUsers{"alice", "bob", "carol"}, "alice, bob and carol are typing…"},
	}
	for _, tc := range tests {
		if got := tc.users.Text(); got != tc.want {
			t.Errorf("TypingUsers(%v).Text() = %q, want %q", tc.users, got, tc.want)
		}
	}
}

func TestTypingLineKeepsLayoutStable(t *testing.T) {
	blank := TypingLine(plainTheme(), nil, 30)
	if Width(blank) != 30 {
		t.Errorf("blank typing line is %d cells, want 30 so the layout cannot jump", Width(blank))
	}
	active := TypingLine(plainTheme(), model.TypingUsers{"alice"}, 30)
	if !strings.Contains(active, "alice is typing") {
		t.Errorf("typing line = %q", active)
	}
}

func TestHeaderFitsWidthAndShowsCounters(t *testing.T) {
	lines := Header(plainTheme(), HeaderState{
		Room:        model.Room{ID: "r1", DisplayName: "general", Kind: model.KindChannel, Topic: "chatter"},
		Width:       80,
		UnreadRooms: 3,
		Mentions:    2,
		Username:    "tester",
		ServerLabel: "chat.example.com",
	})
	if len(lines) != 2 {
		t.Fatalf("got %d header lines, want 2", len(lines))
	}
	if Width(lines[0]) > 80 {
		t.Errorf("header line is %d cells, want <= 80", Width(lines[0]))
	}
	if Width(lines[1]) != 80 {
		t.Errorf("header rule is %d cells, want 80", Width(lines[1]))
	}
	rendered := lines[0]
	for _, want := range []string{"# general", "@2", "3 unread", "tester"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected %q in header: %q", want, rendered)
		}
	}
}

func TestHeaderSurvivesNarrowTerminals(t *testing.T) {
	lines := Header(plainTheme(), HeaderState{
		Room:        model.Room{ID: "r1", DisplayName: "a-very-long-channel-name", Kind: model.KindChannel},
		Width:       20,
		Username:    "tester",
		ServerLabel: "chat.example.com",
	})
	if len(lines) != 2 {
		t.Fatalf("got %d header lines, want 2", len(lines))
	}
	if Width(lines[1]) != 20 {
		t.Errorf("rule width = %d, want 20", Width(lines[1]))
	}
}

func TestWindowAlwaysReturnsRequestedHeight(t *testing.T) {
	lines := []string{"one", "two", "three"}
	if got := Window(lines, 0, 5); len(got) != 5 {
		t.Errorf("got %d lines, want 5", len(got))
	}
	if got := Window(lines, 2, 2); len(got) != 2 || got[0] != "three" || got[1] != "" {
		t.Errorf("window at offset 2 = %q", got)
	}
	if got := Window(lines, 99, 2); len(got) != 2 || got[0] != "" {
		t.Errorf("out-of-range offset should pad, got %q", got)
	}
}

func TestChatFrameNeverExceedsTerminalSize(t *testing.T) {
	frame := Frame{
		Width:        60,
		Height:       12,
		Header:       []string{"header", "rule"},
		Sidebar:      []string{"room a", "room b"},
		Body:         []string{"line 1", "line 2", "line 3"},
		Typing:       "typing…",
		Composer:     []string{"rule", "> "},
		Status:       "status",
		SidebarWidth: 20,
	}
	out := Chat(plainTheme(), frame)
	lines := strings.Split(out, "\n")
	if len(lines) > 12 {
		t.Errorf("frame produced %d lines, want <= 12", len(lines))
	}
	for i, line := range lines {
		if Width(line) > 60 {
			t.Errorf("line %d is %d cells wide, want <= 60", i, Width(line))
		}
	}
}

func TestDayLabel(t *testing.T) {
	now := time.Now()
	if got := DayLabel(now); got != "Today" {
		t.Errorf("today = %q", got)
	}
	if got := DayLabel(now.AddDate(0, 0, -1)); got != "Yesterday" {
		t.Errorf("yesterday = %q", got)
	}
	if got := DayLabel(time.Time{}); got != "" {
		t.Errorf("zero time = %q", got)
	}
}
