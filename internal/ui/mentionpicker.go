package ui

import (
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// mentionRows bounds how tall the mention list gets.
const mentionRows = 6

// mentionLimit bounds how many candidates are offered.
const mentionLimit = 30

// specialMentions are the group mentions the server understands. They are
// offered alongside people because they are typed the same way.
var specialMentions = []model.Member{
	{Username: "all", Name: "notify everyone in this room"},
	{Username: "here", Name: "notify everyone online"},
}

// mentionPicker is the composer's inline "@" completer.
//
// Like the emoji completer it follows the text rather than being opened by a
// key: it appears as soon as an "@" opens a mention and disappears when what is
// typed stops looking like one. Unlike the emoji completer it opens on the bare
// "@", because the candidate list is short and room-specific — seeing who is
// here is the point, not just filtering a list you already know.
type mentionPicker struct {
	open   bool
	query  string
	cursor int

	matches []model.Member

	// tokenStart is where the "@" being completed begins in the composer value,
	// used to splice the username back in.
	tokenStart int
}

func (p mentionPicker) active() bool { return p.open }

// selected returns the highlighted candidate.
func (p mentionPicker) selected() (model.Member, bool) {
	if !p.open || p.cursor < 0 || p.cursor >= len(p.matches) {
		return model.Member{}, false
	}
	return p.matches[p.cursor], true
}

func (p *mentionPicker) close() { *p = mentionPicker{} }

// move steps the cursor, wrapping so a short list stays easy to cycle.
func (p *mentionPicker) move(delta int) {
	if len(p.matches) == 0 {
		return
	}
	p.cursor = (p.cursor + delta + len(p.matches)) % len(p.matches)
}

// sync updates the completer from the composer's current text.
func (p *mentionPicker) sync(text string, candidates []model.Member) {
	start, query, ok := mentionToken(text)
	if !ok {
		p.close()
		return
	}

	if !p.open || p.tokenStart != start {
		p.cursor = 0
	}
	p.open = true
	p.tokenStart = start
	p.query = query
	p.refresh(candidates)

	if len(p.matches) == 0 {
		// Nothing matches, so get out of the way rather than sitting on top of the
		// timeline while someone types an address or a handle we do not know.
		p.close()
	}
}

// refresh recomputes matches for the current query, keeping the cursor in range.
func (p *mentionPicker) refresh(candidates []model.Member) {
	if !p.open {
		return
	}
	p.matches = matchMembers(candidates, p.query, mentionLimit)
	if p.cursor >= len(p.matches) {
		p.cursor = max(0, len(p.matches)-1)
	}
}

// complete splices the chosen username into text in place of the token.
func (p mentionPicker) complete(text string) (string, bool) {
	member, ok := p.selected()
	if !ok || p.tokenStart > len(text) {
		return text, false
	}
	return text[:p.tokenStart] + "@" + member.Username + " ", true
}

// mentionToken finds a trailing "@prefix" in text and returns where the "@"
// starts and what follows it.
//
// Completion only triggers at the end of the input, which is where typing
// happens, and only when the "@" opens at a boundary — otherwise every email
// address would pop the list open halfway through.
func mentionToken(text string) (start int, query string, ok bool) {
	if text == "" {
		return 0, "", false
	}

	i := len(text)
	for i > 0 && isMentionByte(text[i-1]) {
		i--
	}
	if i == 0 || text[i-1] != '@' {
		return 0, "", false
	}
	at := i - 1

	// A mention opens a word: after whitespace, after punctuation such as "(",
	// or at the very start. Anything else is an address or a stray character.
	if at > 0 && !isMentionBoundary(text[at-1]) {
		return 0, "", false
	}
	return at, strings.ToLower(text[i:]), true
}

// matchMembers ranks candidates against what has been typed.
//
// Prefix matches come first — typing "ja" means Jane before Sanjay — and each
// group keeps the order it arrived in, which is most-recently-spoken first.
// The group mentions are offered last so that a real person is never displaced
// by "@all" while someone is typing a name.
func matchMembers(candidates []model.Member, query string, limit int) []model.Member {
	var prefix, contains []model.Member

	add := func(member model.Member) {
		username := strings.ToLower(member.Username)
		name := strings.ToLower(member.Name)
		switch {
		case query == "" || strings.HasPrefix(username, query):
			prefix = append(prefix, member)
		case strings.Contains(username, query) || strings.Contains(name, query):
			contains = append(contains, member)
		}
	}

	for _, member := range candidates {
		if member.Username == "" {
			continue
		}
		add(member)
	}
	for _, member := range specialMentions {
		if strings.HasPrefix(member.Username, query) {
			prefix = append(prefix, member)
		}
	}

	matches := append(prefix, contains...)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// isMentionByte reports whether c can appear in a username. Rocket.Chat allows
// letters, digits, dots, dashes and underscores.
func isMentionByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '.', c == '-', c == '_':
		return true
	default:
		return false
	}
}

// isMentionBoundary reports whether an "@" following c opens a mention.
func isMentionBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '(', '[', '{', '<', '"', '\'', ',', ';', ':':
		return true
	default:
		return false
	}
}
