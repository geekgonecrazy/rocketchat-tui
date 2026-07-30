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
var specialMentions = []model.Mention{
	{Sigil: "@", Value: "all", Name: "notify everyone in this room"},
	{Sigil: "@", Value: "here", Name: "notify everyone online"},
}

// mentionPicker is the composer's inline mention completer, for both the "@"
// that names a person and the "#" that names a room.
//
// Like the emoji completer it follows the text rather than being opened by a
// key: it appears as soon as a sigil opens a mention and disappears when what is
// typed stops looking like one. Unlike the emoji completer it opens on the bare
// sigil, because the candidate list is short and account-specific — seeing who
// is here, or which rooms you are in, is the point, not just filtering a list
// you already know.
//
// One picker serves both sigils rather than two nearly identical ones: only the
// source of the candidates differs, and a single picker makes it structurally
// impossible for the two to be open at once and fight over the same keys.
type mentionPicker struct {
	open   bool
	sigil  byte
	query  string
	cursor int

	matches []model.Mention

	// tokenStart is where the sigil being completed begins in the composer value,
	// used to splice the mention back in.
	tokenStart int
}

func (p mentionPicker) active() bool { return p.open }

// selected returns the highlighted candidate.
func (p mentionPicker) selected() (model.Mention, bool) {
	if !p.open || p.cursor < 0 || p.cursor >= len(p.matches) {
		return model.Mention{}, false
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

// mentionSource supplies the candidates for whichever sigil is being typed. It
// is a function rather than two slices so that the room list, which is long and
// needs converting, is only walked when a "#" is actually open.
type mentionSource func(sigil byte) []model.Mention

// sync updates the completer from the composer's current text.
func (p *mentionPicker) sync(text string, source mentionSource) {
	start, sigil, query, ok := mentionToken(text)
	if !ok {
		p.close()
		return
	}

	if !p.open || p.tokenStart != start || p.sigil != sigil {
		p.cursor = 0
	}
	p.open = true
	p.sigil = sigil
	p.tokenStart = start
	p.query = query
	p.refresh(source)
}

// refresh recomputes matches for the current query, keeping the cursor in range.
func (p *mentionPicker) refresh(source mentionSource) {
	if !p.open {
		return
	}
	// The group mentions are typed like people, so they belong in the "@" list —
	// and nowhere near the "#" one.
	var extras []model.Mention
	if p.sigil == '@' {
		extras = specialMentions
	}
	p.matches = matchMentions(source(p.sigil), extras, p.query, mentionLimit)
	if len(p.matches) == 0 {
		// Nothing matches, so get out of the way rather than sitting on top of the
		// timeline while someone types an address or a slug we do not know.
		p.close()
		return
	}
	if p.cursor >= len(p.matches) {
		p.cursor = max(0, len(p.matches)-1)
	}
}

// complete splices the chosen mention into text in place of the token.
func (p mentionPicker) complete(text string) (string, bool) {
	match, ok := p.selected()
	if !ok || p.tokenStart > len(text) {
		return text, false
	}
	return text[:p.tokenStart] + match.Sigil + match.Value + " ", true
}

// mentionToken finds a trailing "@prefix" or "#prefix" in text and returns where
// the sigil starts, which one it is, and what follows it.
//
// Completion only triggers at the end of the input, which is where typing
// happens, and only when the sigil opens at a boundary — otherwise every email
// address and every "issue#42" would pop the list open halfway through.
func mentionToken(text string) (start int, sigil byte, query string, ok bool) {
	if text == "" {
		return 0, 0, "", false
	}

	i := len(text)
	for i > 0 && isMentionByte(text[i-1]) {
		i--
	}
	if i == 0 || !isMentionSigil(text[i-1]) {
		return 0, 0, "", false
	}
	at := i - 1

	// A mention opens a word: after whitespace, after punctuation such as "(",
	// or at the very start. Anything else is an address or a stray character.
	if at > 0 && !isMentionBoundary(text[at-1]) {
		return 0, 0, "", false
	}
	return at, text[at], strings.ToLower(text[i:]), true
}

// matchMentions ranks candidates against what has been typed.
//
// Prefix matches come first — typing "ja" means Jane before Sanjay — and each
// group keeps the order it arrived in, which is most-recently-active first.
//
// extras are ranked on their value alone and land at the end of the prefix
// group, so a real candidate is never displaced by "@all" while someone is
// typing a name, and the descriptive text that stands in for their display name
// never makes them match something they have nothing to do with.
func matchMentions(candidates, extras []model.Mention, query string, limit int) []model.Mention {
	var prefix, contains []model.Mention

	for _, candidate := range candidates {
		if candidate.Value == "" {
			continue
		}
		value := strings.ToLower(candidate.Value)
		name := strings.ToLower(candidate.Name)
		switch {
		case query == "" || strings.HasPrefix(value, query):
			prefix = append(prefix, candidate)
		case strings.Contains(value, query) || strings.Contains(name, query):
			contains = append(contains, candidate)
		}
	}
	for _, extra := range extras {
		if strings.HasPrefix(extra.Value, query) {
			prefix = append(prefix, extra)
		}
	}

	matches := append(prefix, contains...)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// mentionCandidates supplies the completer with whatever the sigil being typed
// names: the people in the open room for "@", the rooms themselves for "#".
//
// The rooms are the ones the account is subscribed to, which is the same list
// the sidebar shows. Public channels the user has not joined are mentionable on
// the server but are not offered here — finding them would mean a search
// endpoint and a debounce, where this list is already local and already sorted
// by how recently each room saw a message.
func (m chatModel) mentionCandidates(sigil byte) []model.Mention {
	if sigil == '#' {
		mentions := make([]model.Mention, 0, len(m.rooms))
		for _, room := range m.rooms {
			if mention, ok := room.Mention(); ok {
				mentions = append(mentions, mention)
			}
		}
		return mentions
	}

	mentions := make([]model.Mention, 0, len(m.members))
	for _, member := range m.members {
		mentions = append(mentions, member.Mention())
	}
	return mentions
}

// isMentionByte reports whether c can appear in a username or a room slug.
// Rocket.Chat allows letters, digits, dots, dashes and underscores in both.
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

// isMentionSigil reports whether c opens a mention.
func isMentionSigil(c byte) bool { return c == '@' || c == '#' }

// isMentionBoundary reports whether a sigil following c opens a mention.
func isMentionBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '(', '[', '{', '<', '"', '\'', ',', ';', ':':
		return true
	default:
		return false
	}
}
