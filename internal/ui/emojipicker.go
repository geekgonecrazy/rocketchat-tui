package ui

import (
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/emoji"
)

// pickerMode is what an open picker will do with the chosen emoji.
type pickerMode int

const (
	pickerOff pickerMode = iota
	// pickerComplete is the inline completer: it follows what is being typed in
	// the composer and inserts a glyph.
	pickerComplete
	// pickerReact is the modal picker opened on a message; it toggles a reaction.
	pickerReact
)

// pickerRows bounds how tall either picker gets.
const pickerRows = 6

// pickerLimit bounds how many candidates are considered.
const pickerLimit = 50

// emojiPicker is the shared state behind both the composer completer and the
// reaction picker. Only one can be open at a time, which keeps key handling
// unambiguous.
type emojiPicker struct {
	mode   pickerMode
	query  string
	cursor int

	matches []emoji.Match

	// tokenStart is where the ":" being completed begins in the composer value,
	// used to splice the glyph back in. Only meaningful in pickerComplete.
	tokenStart int
	// target is the message being reacted to. Only meaningful in pickerReact.
	target string
}

func (p emojiPicker) active() bool { return p.mode != pickerOff }

// selected returns the highlighted match.
func (p emojiPicker) selected() (emoji.Match, bool) {
	if !p.active() || p.cursor < 0 || p.cursor >= len(p.matches) {
		return emoji.Match{}, false
	}
	return p.matches[p.cursor], true
}

func (p *emojiPicker) close() { *p = emojiPicker{} }

// move steps the cursor, wrapping so a short list stays easy to cycle.
func (p *emojiPicker) move(delta int) {
	if len(p.matches) == 0 {
		return
	}
	p.cursor = (p.cursor + delta + len(p.matches)) % len(p.matches)
}

// refresh recomputes matches for the current query, keeping the cursor in range.
func (p *emojiPicker) refresh() {
	if p.mode == pickerReact && p.query == "" {
		// An empty reaction picker offers the usual suspects rather than the
		// alphabetical head of the table.
		p.matches = emoji.Common()
	} else {
		p.matches = emoji.Search(p.query, pickerLimit)
	}
	if p.cursor >= len(p.matches) {
		p.cursor = max(0, len(p.matches)-1)
	}
}

// openReact opens the modal picker for a message.
func (p *emojiPicker) openReact(messageID string) {
	*p = emojiPicker{mode: pickerReact, target: messageID}
	p.refresh()
}

// syncCompletion updates the inline completer from the composer's current text.
//
// The completer is not opened by a key; it follows the text, appearing as soon
// as what is being typed looks like a shortcode and vanishing when it does not.
// That is why it lives here rather than in the key handler.
func (p *emojiPicker) syncCompletion(text string) {
	if p.mode == pickerReact {
		return // the modal picker owns the input
	}

	start, query, ok := completionToken(text)
	if !ok {
		if p.mode == pickerComplete {
			p.close()
		}
		return
	}

	if p.mode != pickerComplete || p.tokenStart != start {
		p.cursor = 0
	}
	p.mode = pickerComplete
	p.tokenStart = start
	p.query = query
	p.refresh()

	if len(p.matches) == 0 {
		// Nothing matches, so get out of the way rather than showing an empty box
		// while someone types an ordinary colon.
		p.close()
	}
}

// completionToken finds a trailing ":prefix" in text and returns where the colon
// starts and what follows it.
//
// Completion only triggers at the end of the input, which is where typing
// happens, and only when the colon opens at a boundary — otherwise "10:30" and
// "http://" would pop the list open mid-sentence.
func completionToken(text string) (start int, query string, ok bool) {
	if text == "" {
		return 0, "", false
	}

	i := len(text)
	for i > 0 && isShortcodeByte(text[i-1]) {
		i--
	}
	if i == 0 || text[i-1] != ':' {
		return 0, "", false
	}
	colon := i - 1

	// At least one character typed after the colon, so a bare ":" stays quiet.
	if len(text)-i < 1 {
		return 0, "", false
	}
	if colon > 0 && isAlphanumeric(text[colon-1]) {
		return 0, "", false
	}
	return colon, strings.ToLower(text[i:]), true
}

// complete splices the chosen glyph into text in place of the token.
func (p emojiPicker) complete(text string) (string, bool) {
	match, ok := p.selected()
	if !ok || p.mode != pickerComplete || p.tokenStart > len(text) {
		return text, false
	}
	return text[:p.tokenStart] + match.Glyph + " ", true
}

func isShortcodeByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_', c == '-', c == '+':
		return true
	default:
		return false
	}
}

func isAlphanumeric(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return false
	}
}
