package render

import (
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/emoji"
)

// EmojiPickerState is the input for the emoji list, used both by the composer's
// inline autocomplete and by the reaction picker.
type EmojiPickerState struct {
	// Title labels the box, e.g. "React" — empty for the inline completer, which
	// should stay unobtrusive.
	Title string
	// Query is what the user has typed after the colon.
	Query string
	// Matches are the candidates, best first.
	Matches []emoji.Match
	// Cursor is the highlighted index.
	Cursor int
	Width  int
	// MaxRows bounds the height; the list scrolls to keep the cursor visible.
	MaxRows int
}

// EmojiPicker renders the candidate list as a compact box.
//
// It returns the lines bottom-anchored by the caller: the completer floats just
// above the composer, so the last line is the one nearest the text being typed.
func EmojiPicker(theme Theme, state EmojiPickerState) []string {
	if state.Width <= 0 || state.MaxRows <= 0 {
		return nil
	}

	if len(state.Matches) == 0 {
		label := "no emoji matching :" + state.Query
		return []string{theme.Faint.Render(Truncate("  "+label, state.Width))}
	}

	rows := min(state.MaxRows, len(state.Matches))
	offset := 0
	if state.Cursor >= rows {
		offset = state.Cursor - rows + 1
	}
	if offset > len(state.Matches)-rows {
		offset = max(0, len(state.Matches)-rows)
	}

	lines := make([]string, 0, rows+1)
	if state.Title != "" {
		header := state.Title
		if state.Query != "" {
			header += "  :" + state.Query
		}
		header += "  " + itoa(len(state.Matches)) + " matches"
		lines = append(lines, theme.SidebarTitle.Render(Pad("  "+header, state.Width)))
	}

	for row := 0; row < rows; row++ {
		index := offset + row
		match := state.Matches[index]

		// Glyphs are two cells wide in most terminals; pad to a fixed column so
		// the names line up whatever the emoji.
		glyph := match.Glyph
		if gap := 2 - Width(glyph); gap > 0 {
			glyph += strings.Repeat(" ", gap)
		}

		label := glyph + "  :" + match.Name + ":"
		if index == state.Cursor {
			lines = append(lines, theme.SidebarSelected.Render(Pad("  "+label, state.Width)))
			continue
		}
		lines = append(lines, theme.Muted.Render(Pad("  "+label, state.Width)))
	}
	return lines
}

// itoa avoids pulling strconv into this file for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
