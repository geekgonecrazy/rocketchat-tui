package render

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// SidebarState is everything the room list needs to draw itself.
type SidebarState struct {
	Rooms        []model.Room
	Cursor       int // index into Rooms
	ActiveRoomID string
	Width        int
	Height       int
	Focused      bool
	Filter       string
	Filtering    bool
}

// Sidebar renders the room list as exactly Height lines.
//
// Rooms arrive pre-sorted with unreads and mentions first, so the list itself
// answers "what needs my attention" without a separate section.
func Sidebar(theme Theme, state SidebarState) []string {
	if state.Width <= 0 || state.Height <= 0 {
		return nil
	}

	lines := make([]string, 0, state.Height)

	// Header doubles as the filter input when filtering.
	title := "Rooms"
	if state.Filtering || state.Filter != "" {
		title = "/" + state.Filter
		if state.Filtering {
			title += "▏"
		}
	}
	lines = append(lines, theme.SidebarTitle.Render(Pad(title, state.Width)))

	rowCount := state.Height - 1
	if rowCount <= 0 {
		return lines[:state.Height]
	}

	offset := SidebarOffset(state.Cursor, rowCount, len(state.Rooms))

	for row := 0; row < rowCount; row++ {
		index := offset + row
		if index >= len(state.Rooms) {
			lines = append(lines, strings.Repeat(" ", state.Width))
			continue
		}
		lines = append(lines, sidebarRow(theme, state, state.Rooms[index], index))
	}
	return lines
}

// SidebarOffset is the index of the first room shown, given the cursor and how
// many rows are available. Exported so that hit-testing a click can invert it.
func SidebarOffset(cursor, rowCount, total int) int {
	if rowCount <= 0 {
		return 0
	}
	offset := 0
	if cursor >= rowCount {
		offset = cursor - rowCount + 1
	}
	if offset > total-rowCount {
		offset = max(0, total-rowCount)
	}
	return offset
}

// SidebarHeaderRows is how many rows the sidebar spends on its title before the
// room rows begin.
const SidebarHeaderRows = 1

func sidebarRow(theme Theme, state SidebarState, room model.Room, index int) string {
	isActive := room.ID == state.ActiveRoomID
	isCursor := index == state.Cursor

	gutter := " "
	switch {
	case isCursor && state.Focused:
		gutter = "▌"
	case isActive:
		gutter = "│"
	}

	badge, badgeStyle := sidebarBadge(theme, room)
	badgeWidth := Width(badge)
	gap := 0
	if badgeWidth > 0 {
		gap = 1
	}
	nameWidth := max(1, state.Width-Width(gutter)-badgeWidth-gap)
	label := Pad(room.Label(), nameWidth)

	if isActive {
		// One background across the whole row, so the open room is unmistakable
		// even when it has nothing unread.
		row := gutter + label
		if badgeWidth > 0 {
			row += " " + badge
		}
		return theme.SidebarSelected.Render(Pad(row, state.Width))
	}

	nameStyle := theme.SidebarRoom
	switch {
	case room.Mentions() > 0:
		nameStyle = theme.MentionBadge
	case room.HasUnread():
		nameStyle = theme.SidebarUnread
	}

	var b strings.Builder
	b.WriteString(theme.SidebarCursor.Render(gutter))
	b.WriteString(nameStyle.Render(label))
	if badgeWidth > 0 {
		b.WriteString(" ")
		b.WriteString(badgeStyle.Render(badge))
	}
	return b.String()
}

// sidebarBadge picks the trailing indicator: mentions win over plain unreads,
// and a bare alert (unread with no count) falls back to a dot.
func sidebarBadge(theme Theme, room model.Room) (string, lipgloss.Style) {
	switch {
	case room.Mentions() > 0:
		return "@" + strconv.Itoa(room.Mentions()), theme.MentionBadge
	case room.Unread > 0:
		return strconv.Itoa(room.Unread), theme.Badge
	case room.Alert:
		return "•", theme.Badge
	default:
		return "", theme.Faint
	}
}

// FilterRooms narrows the list by a case-insensitive substring of the label.
func FilterRooms(rooms []model.Room, filter string) []model.Room {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return rooms
	}
	out := make([]model.Room, 0, len(rooms))
	for _, room := range rooms {
		haystack := strings.ToLower(room.DisplayName + " " + room.Name)
		if strings.Contains(haystack, filter) {
			out = append(out, room)
		}
	}
	return out
}
