package ui

import (
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// commandRows bounds how tall the command list gets.
const commandRows = 8

// commandLimit bounds how many candidates are offered. A server with a few apps
// installed can register dozens.
const commandLimit = 40

// commandPicker is the composer's inline "/" completer.
//
// Like the mention completer it opens on the bare sigil rather than waiting for
// a character, because the list is the point: which commands exist is a property
// of the server, and nobody can be expected to know it. It closes the moment a
// space is typed, which hands the rest of the line to the "@" completer — that
// is what makes "/invite @jo" complete a username.
type commandPicker struct {
	open   bool
	query  string
	cursor int

	matches []model.Command
}

func (p commandPicker) active() bool { return p.open }

// selected returns the highlighted candidate.
func (p commandPicker) selected() (model.Command, bool) {
	if !p.open || p.cursor < 0 || p.cursor >= len(p.matches) {
		return model.Command{}, false
	}
	return p.matches[p.cursor], true
}

func (p *commandPicker) close() { *p = commandPicker{} }

// typedInFull reports whether what has been typed already names the highlighted
// candidate, in which case there is nothing left to complete.
func (p commandPicker) typedInFull() bool {
	match, ok := p.selected()
	return ok && strings.EqualFold(match.Name, p.query)
}

// move steps the cursor, wrapping so a short list stays easy to cycle.
func (p *commandPicker) move(delta int) {
	if len(p.matches) == 0 {
		return
	}
	p.cursor = (p.cursor + delta + len(p.matches)) % len(p.matches)
}

// sync updates the completer from the composer's current text.
func (p *commandPicker) sync(text string, commands []model.Command) {
	query, ok := commandToken(text)
	if !ok {
		p.close()
		return
	}
	if !p.open {
		p.cursor = 0
	}
	p.open = true
	p.query = query
	p.refresh(commands)
}

// refresh recomputes matches for the current query, keeping the cursor in range.
func (p *commandPicker) refresh(commands []model.Command) {
	if !p.open {
		return
	}
	p.matches = matchCommands(commands, p.query, commandLimit)
	if len(p.matches) == 0 {
		// Nothing matches, so get out of the way. The line is still a command as
		// far as sending is concerned — it just is not one we can offer, and
		// sitting on the timeline saying so would be worse than saying nothing.
		p.close()
		return
	}
	if p.cursor >= len(p.matches) {
		p.cursor = max(0, len(p.matches)-1)
	}
}

// complete replaces what has been typed with the chosen command.
//
// The trailing space matters twice over: it closes this completer, and it is
// what the "@" completer needs before it will open on the argument.
func (p commandPicker) complete() (string, bool) {
	match, ok := p.selected()
	if !ok {
		return "", false
	}
	return "/" + match.Name + " ", true
}

// commandToken reports whether the composer holds a command name being typed.
//
// Only a line that is nothing but the name qualifies: the slash has to be the
// first character, and one space ends the name and with it the completion. That
// keeps the completer out of the way of arguments, and stops it reopening
// halfway through a path someone is typing into /upload.
func commandToken(text string) (query string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", false
	}
	name := text[1:]
	for i := 0; i < len(name); i++ {
		if !isCommandNameByte(name[i]) {
			return "", false
		}
	}
	return strings.ToLower(name), true
}

func isCommandNameByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '_':
		return true
	default:
		return false
	}
}

// descriptionQueryMin is how much has to be typed before descriptions are
// searched as well as names. Two characters match half the list by accident —
// "le" is in "leave", "file" and "people" — and a list that wide is no longer
// telling anyone anything.
const descriptionQueryMin = 3

// matchCommands ranks the registry against what has been typed.
//
// Prefix matches come first, then names that merely contain the query, then
// descriptions — so "/people" still finds /invite, whose name says nothing of
// the sort. Commands nobody here can run are never offered.
func matchCommands(commands []model.Command, query string, limit int) []model.Command {
	var prefix, contains, described []model.Command
	for _, command := range commands {
		if !command.Offerable() {
			continue
		}
		name := strings.ToLower(command.Name)
		switch {
		case query == "" || strings.HasPrefix(name, query):
			prefix = append(prefix, command)
		case strings.Contains(name, query):
			contains = append(contains, command)
		case len(query) >= descriptionQueryMin &&
			strings.Contains(strings.ToLower(command.Description), query):
			described = append(described, command)
		}
	}
	matches := append(append(prefix, contains...), described...)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}
