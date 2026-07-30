package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// Key handling for the chat screen, split by which pane has focus.

func (m chatModel) handleKey(msg tea.KeyMsg) (chatModel, tea.Cmd) {
	pressed := msg.String()

	// The reaction picker is modal, so it takes input before anything else.
	if m.picker.mode == pickerReact {
		return m.handleReactPickerKey(msg)
	}

	// The completer claims its navigation keys before the global bindings do:
	// tab would otherwise cycle focus rather than accept a suggestion.
	if m.picker.mode == pickerComplete {
		switch pressed {
		case "up", "ctrl+p":
			m.picker.move(-1)
			return m, nil
		case "down", "ctrl+n":
			m.picker.move(1)
			return m, nil
		case "tab", "enter":
			return m.acceptCompletion()
		case "esc":
			m.picker.close()
			m.rebuildBody()
			return m, nil
		}
	}

	// Filter entry captures almost everything, so it is handled first.
	if m.filtering {
		return m.handleFilterKey(msg)
	}

	// Global keys. While composing, only non-printable combinations qualify, or
	// typing "?" would open help instead of appearing in the message.
	switch pressed {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+r":
		m.core.Refresh()
		m.notice, m.noticeErr = "resyncing…", false
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearNoticeMsg{} })
	case "ctrl+l":
		if m.activeRoom != "" {
			m.core.MarkRead(m.activeRoom)
		}
		return m, nil
	case "ctrl+t":
		// Global, not focus-dependent: opening a room focuses the composer, so a
		// plain letter could never reach the thread list — it would just be typed.
		if m.activeRoom != "" {
			return m.toggleThreadList()
		}
		return m, nil
	case "tab":
		m.focus = (m.focus + 1) % 3
		cmd := m.syncComposerFocus()
		return m, cmd
	case "shift+tab":
		m.focus = (m.focus + 2) % 3
		cmd := m.syncComposerFocus()
		return m, cmd
	}

	if m.focus != focusComposer {
		switch pressed {
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "q":
			return m, tea.Quit
		}
	}
	if m.showHelp {
		// Any other key dismisses help.
		m.showHelp = false
		return m, nil
	}

	switch m.focus {
	case focusRooms:
		return m.handleRoomsKey(pressed)
	case focusMessages:
		return m.handleMessagesKey(pressed)
	default:
		return m.handleComposerKey(msg)
	}
}

func (m chatModel) handleFilterKey(msg tea.KeyMsg) (chatModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter = ""
		m.applyFilter()
	case "enter":
		m.filtering = false
		if len(m.visible) > 0 {
			cmd := m.openRoom(m.visible[m.roomCursor].ID)
			return m, cmd
		}
	case "backspace":
		if m.filter != "" {
			runes := []rune(m.filter)
			m.filter = string(runes[:len(runes)-1])
			m.applyFilter()
		}
	case "up", "ctrl+p":
		m.moveRoomCursor(-1)
	case "down", "ctrl+n":
		m.moveRoomCursor(1)
	case "ctrl+c":
		return m, tea.Quit
	default:
		if len(msg.Runes) > 0 {
			m.filter += string(msg.Runes)
			m.applyFilter()
		}
	}
	return m, nil
}

func (m chatModel) handleRoomsKey(pressed string) (chatModel, tea.Cmd) {
	switch pressed {
	case "up", "k":
		m.moveRoomCursor(-1)
	case "down", "j":
		m.moveRoomCursor(1)
	case "home", "g":
		m.roomCursor = 0
	case "end", "G":
		m.roomCursor = max(0, len(m.visible)-1)
	case "/":
		m.filtering = true
		m.filter = ""
		m.applyFilter()
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.applyFilter()
		}
	case "enter", "l", "right":
		if len(m.visible) > 0 && m.roomCursor < len(m.visible) {
			cmd := m.openRoom(m.visible[m.roomCursor].ID)
			return m, cmd
		}
	}
	return m, nil
}

func (m chatModel) handleMessagesKey(pressed string) (chatModel, tea.Cmd) {
	switch pressed {
	case "up", "k":
		m.moveMessageCursor(-1)
	case "down", "j":
		m.moveMessageCursor(1)
	case "pgup", "ctrl+b":
		m.scrollBy(-m.bodyHeight() / 2)
	case "pgdown", "ctrl+f", " ":
		m.scrollBy(m.bodyHeight() / 2)
	case "home":
		m.scroll = 0
		m.pinnedToBottom = false
	case "G", "end":
		m.scrollToBottom()
	case "g":
		if m.mode == bodyTimeline && m.hasMore {
			m.core.LoadOlder(m.activeRoom)
		}
	case "u":
		m.positionAtUnread()
	case "t":
		return m.toggleThreadList()
	case "r", "+":
		return m.openReactPicker()
	case "enter":
		return m.openSelectedThread()
	case "esc", "h", "left":
		if m.mode != bodyTimeline {
			return m.showTimeline()
		}
		m.focus = focusRooms
	}
	return m, nil
}

func (m chatModel) handleComposerKey(msg tea.KeyMsg) (chatModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.send()
	case "esc":
		if m.mode == bodyThread {
			return m.showTimeline()
		}
		m.focus = focusMessages
		m.composer.Blur()
		return m, nil
	case "pgup":
		m.scrollBy(-m.bodyHeight() / 2)
		return m, nil
	case "pgdown":
		m.scrollBy(m.bodyHeight() / 2)
		return m, nil
	}

	before := m.composer.Value()
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	after := m.composer.Value()

	// Grow the box with the message, up to a few lines.
	m.composer.SetHeight(clamp(strings.Count(after, "\n")+1, 1, 4))

	// The completer follows the text rather than being summoned by a key, so it
	// has to be recomputed on every edit.
	if after != before {
		m.picker.syncCompletion(after)
		m.rebuildBody() // the picker changes how much room the body has
	}

	// Typing indicators follow actual edits, the way the web client does it.
	if after != before && m.activeRoom != "" {
		if strings.TrimSpace(after) == "" {
			m.core.StopTyping(m.activeRoom)
		} else {
			m.core.UserTyping(m.activeRoom)
		}
	}
	return m, cmd
}

// ---- actions ----------------------------------------------------------------

func (m *chatModel) openRoom(roomID string) tea.Cmd {
	if roomID == "" {
		return nil
	}
	if m.activeRoom != "" && m.activeRoom != roomID {
		m.core.StopTyping(m.activeRoom)
	}
	m.activeRoom = roomID
	m.mode = bodyTimeline
	m.threadID = ""
	m.threadReplies = nil
	m.threadParent = model.Message{}
	m.messages = nil
	m.threads = nil
	m.msgCursor = -1
	m.cursorMsgID = ""
	m.scroll = 0
	m.pinnedToBottom = true
	m.jumpToUnread = true
	m.composer.Reset()
	m.composer.SetHeight(1)
	m.focus = focusComposer
	m.composer.Focus()

	// Keep the sidebar cursor on the room we just opened.
	for i, room := range m.visible {
		if room.ID == roomID {
			m.roomCursor = i
			break
		}
	}

	m.core.OpenRoom(roomID)
	m.rebuildBody()
	return textarea.Blink
}

func (m chatModel) send() (chatModel, tea.Cmd) {
	text := strings.TrimSpace(m.composer.Value())
	if text == "" || m.activeRoom == "" || m.room.ReadOnly {
		return m, nil
	}
	threadID := ""
	if m.mode == bodyThread {
		threadID = m.threadID
	}
	m.core.Send(m.activeRoom, threadID, text)
	m.composer.Reset()
	m.composer.SetHeight(1)
	m.pinnedToBottom = true
	m.scrollToBottom()
	return m, nil
}

func (m chatModel) toggleThreadList() (chatModel, tea.Cmd) {
	if m.mode == bodyThreadList {
		return m.showTimeline()
	}
	m.mode = bodyThreadList
	m.threadsIndex = 0
	m.scroll = 0
	m.pinnedToBottom = false
	m.core.RefreshThreadList(m.activeRoom)
	m.rebuildBody()
	return m, nil
}

func (m chatModel) showTimeline() (chatModel, tea.Cmd) {
	m.mode = bodyTimeline
	m.threadID = ""
	m.core.CloseThread()
	m.scroll = 0
	m.pinnedToBottom = true
	m.rebuildBody()
	m.scrollToBottom()
	return m, nil
}

// openSelectedThread opens the thread on the selected message. Any message can
// anchor a thread, so this both opens existing threads and starts new ones.
func (m chatModel) openSelectedThread() (chatModel, tea.Cmd) {
	var threadID string
	switch m.mode {
	case bodyThreadList:
		if m.threadsIndex < len(m.threads) {
			threadID = m.threads[m.threadsIndex].ID
		}
	case bodyTimeline:
		if m.msgCursor >= 0 && m.msgCursor < len(m.messages) {
			selected := m.messages[m.msgCursor]
			threadID = selected.ID
			if selected.IsThreadReply() {
				threadID = selected.ThreadID
			}
		}
	}
	if threadID == "" {
		return m, nil
	}

	m.threadID = threadID
	m.mode = bodyThread
	m.threadCursor = -1
	m.scroll = 0
	m.pinnedToBottom = true
	m.focus = focusComposer
	m.composer.Focus()
	m.composer.Reset()
	m.core.OpenThread(m.activeRoom, threadID)
	m.rebuildBody()
	return m, textarea.Blink
}

// acceptCompletion replaces the ":prefix" being typed with the chosen glyph.
func (m chatModel) acceptCompletion() (chatModel, tea.Cmd) {
	completed, ok := m.picker.complete(m.composer.Value())
	if !ok {
		m.picker.close()
		return m, nil
	}
	m.composer.SetValue(completed)
	m.picker.close()
	m.rebuildBody()
	if m.activeRoom != "" {
		m.core.UserTyping(m.activeRoom)
	}
	return m, nil
}

// openReactPicker opens the modal picker for the selected message.
func (m chatModel) openReactPicker() (chatModel, tea.Cmd) {
	target := m.selectedMessage()
	if target == "" {
		m.notice, m.noticeErr = "select a message first", false
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearNoticeMsg{} })
	}
	m.picker.openReact(target)
	m.composer.Blur()
	m.rebuildBody()
	return m, nil
}

// selectedMessage is the id the cursor is on, in whichever pane is showing.
func (m chatModel) selectedMessage() string {
	switch m.mode {
	case bodyThread:
		if m.threadCursor >= 0 && m.threadCursor < len(m.threadReplies) {
			return m.threadReplies[m.threadCursor].ID
		}
		return m.threadID
	case bodyThreadList:
		if m.threadsIndex >= 0 && m.threadsIndex < len(m.threads) {
			return m.threads[m.threadsIndex].ID
		}
	default:
		if m.msgCursor >= 0 && m.msgCursor < len(m.messages) {
			return m.messages[m.msgCursor].ID
		}
	}
	return ""
}

// handleReactPickerKey drives the modal picker.
func (m chatModel) handleReactPickerKey(msg tea.KeyMsg) (chatModel, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.picker.close()
		m.rebuildBody()
		cmd := m.syncComposerFocus()
		return m, cmd
	case "up", "ctrl+p", "shift+tab":
		m.picker.move(-1)
		return m, nil
	case "down", "ctrl+n", "tab":
		m.picker.move(1)
		return m, nil
	case "enter":
		return m.applyReaction()
	case "backspace":
		if m.picker.query != "" {
			runes := []rune(m.picker.query)
			m.picker.query = string(runes[:len(runes)-1])
			m.picker.refresh()
			m.rebuildBody()
		}
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			m.picker.query += strings.ToLower(string(msg.Runes))
			m.picker.refresh()
			m.rebuildBody()
		}
		return m, nil
	}
}

// applyReaction toggles the highlighted emoji on the target message.
func (m chatModel) applyReaction() (chatModel, tea.Cmd) {
	match, ok := m.picker.selected()
	if !ok {
		m.picker.close()
		return m, nil
	}
	target := m.picker.target
	m.core.React(target, match.Name, !m.alreadyReacted(target, match.Name))
	m.picker.close()
	m.rebuildBody()
	cmd := m.syncComposerFocus()
	return m, cmd
}

// alreadyReacted reports whether the user has this reaction on a message, which
// is what makes the picker and clicks toggle rather than only ever add.
func (m chatModel) alreadyReacted(messageID, shortcode string) bool {
	want := ":" + strings.Trim(shortcode, ":") + ":"
	for _, list := range [][]model.Message{m.messages, m.threadReplies, {m.threadParent}} {
		for _, msg := range list {
			if msg.ID != messageID {
				continue
			}
			for _, reaction := range msg.Reactions {
				if reaction.Emoji == want {
					return reaction.Mine
				}
			}
		}
	}
	return false
}

func (m *chatModel) syncComposerFocus() tea.Cmd {
	if m.focus == focusComposer {
		m.composer.Focus()
		return textarea.Blink
	}
	m.composer.Blur()
	return nil
}
