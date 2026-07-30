package render

import (
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/emoji"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
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

// MentionPickerState is the input for the composer's "@" and "#" completers.
type MentionPickerState struct {
	// Query is what the user has typed after the sigil.
	Query string
	// Matches are the candidates, best first.
	Matches []model.Mention
	// Cursor is the highlighted index.
	Cursor int
	Width  int
	// MaxRows bounds the height; the list scrolls to keep the cursor visible.
	MaxRows int
}

// MentionPicker renders the candidate list as a compact box, floating just
// above the composer the way the emoji completer does. Each candidate carries
// its own sigil, so people and rooms render through the same list.
func MentionPicker(theme Theme, state MentionPickerState) []string {
	if state.Width <= 0 || state.MaxRows <= 0 || len(state.Matches) == 0 {
		return nil
	}

	rows := min(state.MaxRows, len(state.Matches))
	offset := 0
	if state.Cursor >= rows {
		offset = state.Cursor - rows + 1
	}
	if offset > len(state.Matches)-rows {
		offset = max(0, len(state.Matches)-rows)
	}

	lines := make([]string, 0, rows)
	for row := 0; row < rows; row++ {
		index := offset + row
		label := state.Matches[index].Label()
		if index == state.Cursor {
			lines = append(lines, theme.SidebarSelected.Render(Pad("  "+label, state.Width)))
			continue
		}
		lines = append(lines, theme.Muted.Render(Pad("  "+label, state.Width)))
	}
	return lines
}

// PathMatchesState is the input for the attach prompt's completion hint.
type PathMatchesState struct {
	// Matches are the directory entries the typed path could still become.
	Matches []string
	Width   int
	// MaxRows bounds the height. Names that do not fit are counted rather than
	// dropped, so the list never implies the choice is narrower than it is.
	MaxRows int
}

// PathMatches renders completion candidates as a packed strip.
//
// Unlike the emoji and mention completers this list has no cursor: a path is
// completed with tab the way a shell does it, and enter belongs to the prompt.
// Names are therefore packed across the line rather than given a row each,
// which fits far more of them into the two or three rows this can spare.
func PathMatches(theme Theme, state PathMatchesState) []string {
	if state.Width <= 2 || state.MaxRows <= 0 || len(state.Matches) == 0 {
		return nil
	}

	const gap = 2
	budget := state.Width - 2 // the two-space indent every row carries

	var (
		rows  [][]string
		row   []string
		width int
	)
	for index, name := range state.Matches {
		need := Width(name)
		if len(row) > 0 {
			need += gap
		}

		if len(row) > 0 && width+need > budget {
			if len(rows) == state.MaxRows-1 {
				// Last row: say how many names did not make it rather than
				// silently stopping partway through the alphabet.
				row = withOverflow(row, width, len(state.Matches)-index, budget, gap)
				break
			}
			rows = append(rows, row)
			row, width = nil, 0
			need = Width(name)
		}
		row = append(row, name)
		width += need
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	lines := make([]string, 0, len(rows))
	for _, names := range rows {
		packed := "  " + strings.Join(names, strings.Repeat(" ", gap))
		lines = append(lines, theme.Muted.Render(Pad(Truncate(packed, state.Width), state.Width)))
	}
	return lines
}

// withOverflow appends a "+N more" marker to the last row, dropping names off
// the end until it fits.
//
// The marker has to survive intact: a row truncated mid-count says nothing, and
// a row truncated just after the last name that fits claims to be the whole
// list. Every name dropped to make room is one more the marker counts.
func withOverflow(row []string, width, remaining, budget, gap int) []string {
	for {
		marker := "+" + itoa(remaining) + " more"
		need := Width(marker)
		if len(row) > 0 {
			need += gap
		}
		if width+need <= budget || len(row) == 0 {
			return append(row, marker)
		}
		last := row[len(row)-1]
		row = row[:len(row)-1]
		width -= Width(last) + gap
		remaining++
	}
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
