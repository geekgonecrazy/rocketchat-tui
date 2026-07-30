package render

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// Width returns the printable cell width of s, ignoring ANSI escapes.
func Width(s string) int { return runewidth.StringWidth(stripANSI(s)) }

// HumanBytes formats a file size for a status line: three significant figures
// at most, so it stays the same handful of columns wide whatever the value.
func HumanBytes(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	value, units := float64(n), []string{"KB", "MB", "GB", "TB"}
	for _, unit := range units {
		value /= 1024
		if value < 1024 || unit == units[len(units)-1] {
			precision := 1
			if value >= 10 {
				precision = 0
			}
			return strconv.FormatFloat(value, 'f', precision, 64) + " " + unit
		}
	}
	return strconv.FormatInt(n, 10) + " B"
}

// Truncate shortens s to at most width cells, appending an ellipsis when cut.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if used+w > width-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

// Pad right-pads s with spaces to exactly width cells, truncating if longer.
func Pad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = Truncate(s, width)
	if gap := width - Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// Wrap breaks text into lines no wider than width cells, preferring word
// boundaries and preserving existing newlines. Returns at least one line.
func Wrap(text string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	var out []string
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		out = append(out, wrapParagraph(paragraph, width)...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func wrapParagraph(paragraph string, width int) []string {
	paragraph = strings.TrimRight(paragraph, " \t")
	if paragraph == "" {
		return []string{""}
	}

	var (
		lines   []string
		current strings.Builder
		used    int
	)
	flush := func() {
		lines = append(lines, current.String())
		current.Reset()
		used = 0
	}

	for _, word := range splitWords(paragraph) {
		wordWidth := Width(word)

		// A word longer than the line gets hard-split rather than overflowing.
		// The final chunk stays open so following words can share its line.
		if wordWidth > width {
			if used > 0 {
				flush()
			}
			chunks := hardSplit(word, width)
			for i, chunk := range chunks {
				if i == len(chunks)-1 {
					current.WriteString(chunk)
					used = Width(chunk)
					break
				}
				lines = append(lines, chunk)
			}
			continue
		}

		if used == 0 {
			current.WriteString(word)
			used = wordWidth
			continue
		}
		if used+1+wordWidth > width {
			flush()
			current.WriteString(word)
			used = wordWidth
			continue
		}
		current.WriteString(" ")
		current.WriteString(word)
		used += 1 + wordWidth
	}
	if used > 0 || len(lines) == 0 {
		flush()
	}
	return lines
}

func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' })
}

func hardSplit(word string, width int) []string {
	var (
		chunks []string
		buf    strings.Builder
		used   int
	)
	for _, r := range word {
		w := runewidth.RuneWidth(r)
		if used+w > width {
			chunks = append(chunks, buf.String())
			buf.Reset()
			used = 0
		}
		buf.WriteRune(r)
		used += w
	}
	if buf.Len() > 0 {
		chunks = append(chunks, buf.String())
	}
	return chunks
}

// Rule draws a horizontal line of width cells with an optional centred label.
func Rule(label string, width int, line string) string {
	if width <= 0 {
		return ""
	}
	if label == "" {
		return strings.Repeat(line, width)
	}
	label = " " + label + " "
	labelWidth := Width(label)
	if labelWidth >= width {
		return Truncate(label, width)
	}
	left := (width - labelWidth) / 2
	right := width - labelWidth - left
	return strings.Repeat(line, left) + label + strings.Repeat(line, right)
}

// stripANSI removes escape sequences so width maths sees only printable cells.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && (unicode.IsLetter(r)):
			inEscape = false
		case inEscape:
			// still inside the sequence
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Clock formats a message timestamp for the timeline.
func Clock(t time.Time) string {
	if t.IsZero() {
		return "--:--"
	}
	return t.Local().Format("15:04")
}

// DayLabel formats a date separator, using friendly names for recent days.
func DayLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.Local()
	today := time.Now().Local()
	switch {
	case sameDay(local, today):
		return "Today"
	case sameDay(local, today.AddDate(0, 0, -1)):
		return "Yesterday"
	case today.Sub(local) < 7*24*time.Hour:
		return local.Format("Monday")
	case local.Year() == today.Year():
		return local.Format("Mon 2 January")
	default:
		return local.Format("2 January 2006")
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// RelativeTime renders a compact "how long ago" string for thread activity.
func RelativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	elapsed := time.Since(t)
	switch {
	case elapsed < time.Minute:
		return "now"
	case elapsed < time.Hour:
		return strconv.Itoa(int(elapsed.Minutes())) + "m"
	case elapsed < 24*time.Hour:
		return strconv.Itoa(int(elapsed.Hours())) + "h"
	case elapsed < 7*24*time.Hour:
		return strconv.Itoa(int(elapsed.Hours()/24)) + "d"
	default:
		return t.Local().Format("2 Jan")
	}
}
