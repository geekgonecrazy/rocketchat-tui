package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

// headerRows is how many rows the header occupies before the body starts.
const headerRows = 2

// wheelStep is how far one wheel notch scrolls.
const wheelStep = 3

// region identifies which part of the screen a point falls in.
type region int

const (
	regionNone region = iota
	regionSidebar
	regionBody
	regionComposer
)

// locate maps screen coordinates onto a region plus the row within it.
func (m chatModel) locate(x, y int) (region, int) {
	bodyHeight := m.bodyHeight()
	switch {
	case y < headerRows:
		return regionNone, 0
	case y < headerRows+bodyHeight:
		row := y - headerRows
		if x < m.sidebarWidth() {
			return regionSidebar, row
		}
		if x == m.sidebarWidth() {
			return regionNone, 0 // the divider column
		}
		return regionBody, row
	case y == headerRows+bodyHeight:
		return regionNone, 0 // typing line
	case y < headerRows+bodyHeight+1+m.composerHeight():
		return regionComposer, 0
	default:
		return regionNone, 0 // status bar
	}
}

// handleMouse turns a mouse event into the same actions the keyboard performs,
// so nothing is reachable only by clicking.
func (m chatModel) handleMouse(msg tea.MouseMsg) (chatModel, tea.Cmd) {
	where, row := m.locate(msg.X, msg.Y)

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.scrollRegion(where, -wheelStep)
	case tea.MouseButtonWheelDown:
		return m.scrollRegion(where, wheelStep)
	case tea.MouseButtonLeft:
		// Only act on the press; the matching release would repeat the action.
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
	default:
		return m, nil
	}

	switch where {
	case regionSidebar:
		return m.clickSidebar(row)
	case regionBody:
		// Columns are measured from the start of the message pane, past the
		// sidebar and its divider.
		return m.clickBody(row, msg.X-m.sidebarWidth()-1)
	case regionComposer:
		m.focus = focusComposer
		cmd := m.syncComposerFocus()
		return m, cmd
	}
	return m, nil
}

func (m chatModel) scrollRegion(where region, delta int) (chatModel, tea.Cmd) {
	switch where {
	case regionSidebar:
		// The sidebar has no independent scroll offset: it follows the cursor.
		m.moveRoomCursor(delta)
	case regionBody:
		m.scrollBy(delta)
	}
	return m, nil
}

// clickSidebar opens the room under the pointer.
func (m chatModel) clickSidebar(row int) (chatModel, tea.Cmd) {
	rowCount := m.bodyHeight() - render.SidebarHeaderRows
	if row < render.SidebarHeaderRows || rowCount <= 0 {
		// The title row doubles as the filter input, so clicking it starts one.
		m.focus = focusRooms
		cmd := m.syncComposerFocus()
		return m, cmd
	}

	offset := render.SidebarOffset(m.roomCursor, rowCount, len(m.visible))
	index := offset + row - render.SidebarHeaderRows
	if index < 0 || index >= len(m.visible) {
		return m, nil
	}

	m.roomCursor = index
	if m.visible[index].ID == m.activeRoom {
		// Clicking the room already open should not reload it; just focus the list.
		m.focus = focusRooms
		cmd := m.syncComposerFocus()
		return m, cmd
	}
	cmd := m.openRoom(m.visible[index].ID)
	return m, cmd
}

// reactionAt returns the reaction whose chip covers a column on a reaction line.
//
// Spans are measured from the start of the content, so the two-cell gutter the
// timeline draws has to be added back before comparing.
func (m chatModel) reactionAt(line, column int) (string, bool) {
	const gutter = 2
	for _, span := range m.body.ReactionSpans[line] {
		if column >= span.Start+gutter && column < span.End+gutter {
			return span.Emoji, true
		}
	}
	return "", false
}

// messageIDAt is the id of the message at an index in the visible pane.
func (m chatModel) messageIDAt(index int) string {
	switch m.mode {
	case bodyThread:
		if index >= 0 && index < len(m.threadReplies) {
			return m.threadReplies[index].ID
		}
	case bodyThreadList:
		if index >= 0 && index < len(m.threads) {
			return m.threads[index].ID
		}
	default:
		if index >= 0 && index < len(m.messages) {
			return m.messages[index].ID
		}
	}
	return ""
}

// clickBody selects the message under the pointer, or opens a thread when the
// click lands on a thread affordance.
func (m chatModel) clickBody(row, column int) (chatModel, tea.Cmd) {
	line := m.scroll + row
	if line < 0 || line >= len(m.body.Lines) {
		return m, nil
	}

	m.focus = focusMessages
	m.composer.Blur()

	// A click on a reaction chip toggles it, which is the quickest way to add a
	// +1 to something someone else already reacted to.
	if index, ok := m.body.ReactionLine[line]; ok {
		if shortcode, hit := m.reactionAt(line, column); hit {
			m.setCursor(index)
			m.rebuildBody()
			m.core.React(m.messageIDAt(index), shortcode,
				!m.alreadyReacted(m.messageIDAt(index), shortcode))
			return m, nil
		}
	}

	// A click straight on "↳ N replies" opens that thread.
	if index, ok := m.body.HintLine[line]; ok && m.mode == bodyTimeline {
		m.setCursor(index)
		m.rebuildBody()
		return m.openSelectedThread()
	}

	index := m.messageAtLine(line)
	if index < 0 {
		return m, nil
	}

	// In the thread list every row is a thread, so a click opens it directly.
	if m.mode == bodyThreadList {
		m.setCursor(index)
		m.rebuildBody()
		return m.openSelectedThread()
	}

	m.setCursor(index)
	m.rebuildBody()
	m.ensureCursorVisible(index)
	return m, nil
}

// messageAtLine returns the index of the message occupying a line, or -1 for
// lines that belong to no message (separators, rules, padding).
func (m chatModel) messageAtLine(line int) int {
	found := -1
	for index, start := range m.body.MessageLine {
		if start > line {
			break
		}
		found = index
	}
	return found
}
