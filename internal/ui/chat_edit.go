package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// Editing a message you already sent, recalled from the composer with ↑.
//
// The gesture is deliberately narrow: ↑ only recalls from an *empty* composer,
// so a half-typed message is never displaced, and it only ever offers messages
// from the context on screen. A thread's composer posts into the thread, so it
// recalls the thread's own messages; pulling a channel message into it would
// mean editing something the user cannot see.

func (m chatModel) editing() bool { return m.editID != "" }

// editCandidates is every message the ↑ gesture may offer, oldest first.
//
// "Visible in this context" is the whole rule: what the body pane is currently
// showing, filtered to the user's own real messages. System lines are not
// editable, and neither is anybody else's message.
func (m chatModel) editCandidates() []model.Message {
	var source []model.Message
	switch m.mode {
	case bodyThread:
		// The parent heads the thread pane, so it is on screen and counts —
		// but nothing else from the room does.
		if m.threadParent.ID != "" {
			source = append(source, m.threadParent)
		}
		source = append(source, m.threadReplies...)
	case bodyThreadList:
		// The rows here are thread parents from all over the room, and the
		// composer is not addressing any of them. Nothing to recall.
		return nil
	default:
		source = m.messages
	}

	candidates := make([]model.Message, 0, len(source))
	for _, msg := range source {
		if msg.Own && !msg.IsSystem() {
			candidates = append(candidates, msg)
		}
	}
	return candidates
}

// editTargetGone reports that the message being edited has left the context it
// was recalled from — deleted, or paged out from under the cursor. Committing
// then would write into thin air, so callers drop out of edit mode.
func (m chatModel) editTargetGone() bool {
	if !m.editing() {
		return false
	}
	for _, msg := range m.editCandidates() {
		if msg.ID == m.editID {
			return false
		}
	}
	return true
}

// beginEdit loads the newest editable message into the composer.
func (m chatModel) beginEdit() (chatModel, tea.Cmd) {
	if m.activeRoom == "" || m.room.ReadOnly {
		return m, nil
	}
	candidates := m.editCandidates()
	if len(candidates) == 0 {
		return m, nil
	}
	m.editDraft = m.composer.Value()
	m.editPrevCursorID = m.cursorMessageID()
	m.loadEdit(candidates[len(candidates)-1])
	return m, nil
}

// stepEdit moves to an older (-1) or newer (+1) message of the user's own.
// Stepping past the newest leaves edit mode, which is how ↓ gets back to a
// blank composer without reaching for esc.
func (m chatModel) stepEdit(delta int) (chatModel, tea.Cmd) {
	candidates := m.editCandidates()
	index := -1
	for i, msg := range candidates {
		if msg.ID == m.editID {
			index = i
			break
		}
	}
	if index < 0 {
		// The message went away underneath us — deleted, or paged out of the
		// window. Editing it would post into thin air.
		m = m.abandonEdit()
		return m.notify("that message is no longer here", false)
	}

	next := index + delta
	switch {
	case next < 0:
		return m, nil // already at the oldest we hold
	case next >= len(candidates):
		return m.cancelEdit()
	}
	m.loadEdit(candidates[next])
	return m, nil
}

// loadEdit puts a message's text in the composer and selects it in the body, so
// the message being rewritten is identifiable on screen and not just in the box.
func (m *chatModel) loadEdit(msg model.Message) {
	m.editID = msg.ID
	m.composer.SetValue(msg.Text)
	m.composer.SetHeight(clamp(strings.Count(msg.Text, "\n")+1, 1, 4))
	m.composer.CursorEnd()
	m.focus = focusComposer
	m.composer.Focus()
	// Completers are keyed to what is being typed; a message loaded wholesale
	// is not a keystroke.
	m.mentions.close()
	m.picker.close()
	m.selectMessage(msg.ID)
}

// cancelEdit returns the composer to whatever it held before, along with the
// selection that was there.
func (m chatModel) cancelEdit() (chatModel, tea.Cmd) {
	previous := m.editPrevCursorID
	m = m.abandonEdit()
	m.selectMessage(previous)
	return m, nil
}

// abandonEdit drops edit mode without touching the body selection. Callers that
// are staying on this screen restore the selection too; callers that are
// leaving it (a new room, a thread opening) are about to rebuild it anyway.
func (m chatModel) abandonEdit() chatModel {
	if !m.editing() {
		return m
	}
	draft := m.editDraft
	m.editID, m.editDraft, m.editPrevCursorID = "", "", ""
	m.composer.SetValue(draft)
	m.composer.SetHeight(clamp(strings.Count(draft, "\n")+1, 1, 4))
	m.composer.CursorEnd()
	return m
}

// commitEdit sends the rewritten text.
//
// Nothing is changed locally: the server decides whether an edit is allowed at
// all, and the timeline redraws from the copy it sends back.
func (m chatModel) commitEdit() (chatModel, tea.Cmd) {
	text := strings.TrimSpace(m.composer.Value())
	if text == "" {
		// Empty text deletes the message on the server. That is not what "I
		// cleared the box" means, so it is refused rather than obeyed.
		return m.notify("an empty message can't be saved — esc cancels", false)
	}

	target := m.editID
	unchanged := false
	for _, msg := range m.editCandidates() {
		if msg.ID == target {
			unchanged = msg.Text == text
			break
		}
	}
	if !unchanged {
		m.core.Edit(m.activeRoom, target, text)
	}

	previous := m.editPrevCursorID
	m = m.abandonEdit()
	m.selectMessage(previous)
	if unchanged {
		return m, nil
	}
	return m.notify("edit saved", false)
}

// selectMessage moves the body cursor onto a message by id, clearing the
// selection when the id is empty or no longer on screen.
func (m *chatModel) selectMessage(messageID string) {
	var list []model.Message
	switch m.mode {
	case bodyThread:
		list = m.threadReplies
	case bodyThreadList:
		return
	default:
		list = m.messages
	}

	target := -1
	if messageID != "" {
		for i, msg := range list {
			if msg.ID == messageID {
				target = i
				break
			}
		}
	}
	m.setCursor(target)
	m.rebuildBody()
	if target >= 0 {
		m.ensureCursorVisible(target)
	}
}

// cursorMessageID is the message the body cursor is on, empty when none is.
// Unlike selectedMessage it never falls back to the thread parent: this is used
// to put a selection back exactly as it was.
func (m chatModel) cursorMessageID() string {
	switch m.mode {
	case bodyThread:
		if m.threadCursor >= 0 && m.threadCursor < len(m.threadReplies) {
			return m.threadReplies[m.threadCursor].ID
		}
	case bodyThreadList:
		return ""
	default:
		if m.msgCursor >= 0 && m.msgCursor < len(m.messages) {
			return m.messages[m.msgCursor].ID
		}
	}
	return ""
}

// atComposerTop and atComposerBottom report whether ↑/↓ would leave the text in
// the box. Inside a multi-line message they move the cursor as usual; only at
// the edges do they step to another message.
func (m chatModel) atComposerTop() bool {
	return m.composer.Line() == 0 && m.composer.LineInfo().RowOffset == 0
}

func (m chatModel) atComposerBottom() bool {
	info := m.composer.LineInfo()
	return m.composer.Line() == m.composer.LineCount()-1 && info.RowOffset >= info.Height-1
}
