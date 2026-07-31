package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// Slash commands are split between here and the core. The core owns everything
// that talks to the server; this file owns the ones that act on rctui itself —
// quitting, attaching a file off this machine, moving the sidebar cursor — none
// of which a server could do on our behalf. The registry says which is which,
// and the core builds it (see app/commands.go).

// runComposedCommand executes the "/name params" the composer holds.
//
// An unrecognised command is never posted as a message. Someone who types
// "/inivte @jane" meant to invite her, and sending the typo to the room as text
// is both useless and public, so the line stays in the composer to be corrected.
func (m chatModel) runComposedCommand(name, params string) (chatModel, tea.Cmd) {
	command, known := model.FindCommand(m.commands, name)
	if !known {
		return m.notify("no such command: /"+name, true)
	}

	if command.Scope == model.ScopeClient {
		return m.runClientCommand(command, params)
	}
	if m.activeRoom == "" {
		return m.notify("/"+name+" needs an open room", true)
	}
	threadID := ""
	if m.mode == bodyThread {
		threadID = m.threadID
	}
	// Typing a command is not composing a message, and the room should not be
	// told we are still writing one.
	m.core.StopTyping(m.activeRoom)
	m.core.RunCommand(m.activeRoom, threadID, name, params)
	return m.clearComposer(), nil
}

// runClientCommand handles the commands rctui implements itself.
func (m chatModel) runClientCommand(command model.Command, params string) (chatModel, tea.Cmd) {
	switch command.Name {
	case "exit", "quit":
		return m, tea.Quit

	case "upload":
		// The composer keeps the line when the path is bad, so it is cleared by
		// attachFromCommand only once the file is known to exist.
		return m.attachFromCommand(params)

	case "help":
		m.showHelp = true
		return m.clearComposer(), nil

	case "settings":
		// The pane takes the keyboard, so the composer has to give it up: leaving
		// focus there would send the arrow keys to a text box nobody can see.
		m.showHelp = false
		m.settings.open = true
		m.settings.cursor = 0
		m.focus = focusMessages
		m = m.clearComposer()
		cmd := m.syncComposerFocus()
		return m, cmd

	case "open":
		return m.openRoomByName(params)
	}
	return m.notify("/"+command.Name+" is not implemented", true)
}

// openRoomByName is the client's own /open: it moves to a room already in the
// sidebar rather than asking the server to open one, which is what "open" means
// when the room list is on screen.
func (m chatModel) openRoomByName(params string) (chatModel, tea.Cmd) {
	query := strings.ToLower(strings.Trim(strings.TrimSpace(params), "#@"))
	if query == "" {
		return m.notify("/open takes a room name", true)
	}

	// Exact match first: a room called "dev" should win over "developers", which
	// is otherwise whichever the sidebar happens to hold first.
	var partial string
	for _, room := range m.rooms {
		for _, candidate := range []string{room.Name, room.DisplayName} {
			if candidate == "" {
				continue
			}
			candidate = strings.ToLower(candidate)
			if candidate == query {
				return m.openAndClear(room.ID)
			}
			if partial == "" && strings.Contains(candidate, query) {
				partial = room.ID
			}
		}
	}
	if partial != "" {
		return m.openAndClear(partial)
	}
	return m.notify("no room here called "+query, true)
}

func (m chatModel) openAndClear(roomID string) (chatModel, tea.Cmd) {
	m = m.clearComposer()
	cmd := m.openRoom(roomID)
	return m, cmd
}

// clearComposer empties the box and closes whatever was completing into it.
func (m chatModel) clearComposer() chatModel {
	m.composer.Reset()
	m.composer.SetHeight(1)
	m.cmdPicker.close()
	m.mentions.close()
	m.picker.close()
	m.rebuildBody()
	return m
}

// acceptCommand replaces what has been typed with the highlighted command.
func (m chatModel) acceptCommand() (chatModel, tea.Cmd) {
	completed, ok := m.cmdPicker.complete()
	if !ok {
		m.cmdPicker.close()
		return m, nil
	}
	m.composer.SetValue(completed)
	m.cmdPicker.close()
	m.rebuildBody()
	return m, nil
}
