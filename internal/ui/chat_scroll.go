package ui

import "github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"

// Cursor movement, scrolling, and layout rebuilds for the chat screen.
//
// Layout is rebuilt when state changes rather than on every frame, so View only
// has to slice a window out of pre-rendered lines.

// ---- sidebar + cursors ------------------------------------------------------

func (m *chatModel) applyFilter() {
	m.visible = render.FilterRooms(m.rooms, m.filter)
	if m.roomCursor >= len(m.visible) {
		m.roomCursor = max(0, len(m.visible)-1)
	}
}

func (m *chatModel) moveRoomCursor(delta int) {
	if len(m.visible) == 0 {
		m.roomCursor = 0
		return
	}
	m.roomCursor = clamp(m.roomCursor+delta, 0, len(m.visible)-1)
}

func (m *chatModel) moveMessageCursor(delta int) {
	count, cursor := m.cursorTarget()
	if count == 0 {
		m.scrollBy(delta)
		return
	}
	if cursor < 0 {
		// First move selects the newest message, so navigation starts where the
		// eye already is.
		cursor = count - 1
	} else {
		cursor = clamp(cursor+delta, 0, count-1)
	}
	m.setCursor(cursor)
	m.rebuildBody()
	m.ensureCursorVisible(cursor)
}

// cursorTarget returns the item count and current cursor for the active mode.
func (m chatModel) cursorTarget() (count, cursor int) {
	switch m.mode {
	case bodyThread:
		return len(m.threadReplies), m.threadCursor
	case bodyThreadList:
		return len(m.threads), m.threadsIndex
	default:
		return len(m.messages), m.msgCursor
	}
}

func (m *chatModel) setCursor(cursor int) {
	switch m.mode {
	case bodyThread:
		m.threadCursor = cursor
	case bodyThreadList:
		m.threadsIndex = cursor
	default:
		m.msgCursor = cursor
		m.cursorMsgID = ""
		if cursor >= 0 && cursor < len(m.messages) {
			m.cursorMsgID = m.messages[cursor].ID
		}
	}
}

// restoreMessageCursor keeps the selection on the same message after the list
// is rebuilt.
func (m *chatModel) restoreMessageCursor() {
	if m.cursorMsgID == "" {
		m.msgCursor = -1
		return
	}
	m.msgCursor = -1
	for i, msg := range m.messages {
		if msg.ID == m.cursorMsgID {
			m.msgCursor = i
			return
		}
	}
}

// ---- scrolling --------------------------------------------------------------

func (m *chatModel) rebuildBody() {
	width := m.bodyWidth()
	if width <= 0 {
		return
	}

	switch m.mode {
	case bodyThread:
		m.body = render.Thread(m.theme, render.ThreadState{
			Parent:  m.threadParent,
			Replies: m.threadReplies,
			Width:   width,
			Cursor:  m.threadCursor,
			Loading: m.threadParent.ID == "",
		})

	case bodyThreadList:
		lines := render.ThreadList(m.theme, render.ThreadListState{
			Threads: m.threads,
			Width:   width,
			Cursor:  m.threadsIndex,
		})
		// Each thread occupies exactly two lines, which is all the anchor table
		// needs to keep the cursor on screen.
		anchors := make([]int, len(m.threads))
		for i := range anchors {
			anchors[i] = i * 2
		}
		m.body = render.TimelineView{Lines: lines, MessageLine: anchors, UnreadLine: -1}

	default:
		m.body = render.Timeline(m.theme, render.TimelineState{
			Messages:    m.messages,
			UnreadFrom:  m.unreadFrom,
			Width:       width,
			Cursor:      m.msgCursor,
			Focused:     m.focus == focusMessages,
			HasMore:     m.hasMore,
			LoadingMore: m.loadingMore,
			Empty:       "No messages yet — say something.",
		})
	}
	m.clampScroll()
}

func (m chatModel) maxScroll() int {
	return max(0, len(m.body.Lines)-m.bodyHeight())
}

func (m *chatModel) clampScroll() {
	limit := m.maxScroll()
	if m.pinnedToBottom {
		m.scroll = limit
		return
	}
	m.scroll = clamp(m.scroll, 0, limit)
}

func (m *chatModel) scrollBy(delta int) {
	if delta == 0 {
		return
	}
	m.scroll = clamp(m.scroll+delta, 0, m.maxScroll())
	m.pinnedToBottom = m.scroll >= m.maxScroll()
}

func (m *chatModel) scrollToBottom() {
	m.pinnedToBottom = true
	m.scroll = m.maxScroll()
}

func (m *chatModel) ensureCursorVisible(cursor int) {
	if cursor < 0 || cursor >= len(m.body.MessageLine) {
		return
	}
	line := m.body.MessageLine[cursor]
	height := m.bodyHeight()
	switch {
	case line < m.scroll:
		m.scroll = line
	case line >= m.scroll+height:
		m.scroll = line - height + 1
	}
	m.scroll = clamp(m.scroll, 0, m.maxScroll())
	m.pinnedToBottom = m.scroll >= m.maxScroll()
}

// positionAtUnread scrolls so the "new messages" rule sits near the top, which
// is the whole point of having the rule.
func (m *chatModel) positionAtUnread() {
	m.jumpToUnread = false
	if m.body.UnreadLine < 0 {
		m.scrollToBottom()
		return
	}
	m.scroll = clamp(m.body.UnreadLine-1, 0, m.maxScroll())
	m.pinnedToBottom = m.scroll >= m.maxScroll()
}
