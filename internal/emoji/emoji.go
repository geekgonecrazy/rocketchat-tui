// Package emoji maps between `:shortcode:` text and Unicode glyphs.
//
// Rocket.Chat stores and transmits shortcodes — message text arrives containing
// literal `:joy:`, and the reaction API is keyed by shortcode — so a client
// needs both directions: shortcode to glyph for display, glyph to shortcode for
// anything sent back.
//
//go:generate go run gen.go
package emoji

import (
	"sort"
	"strings"
	"sync"
)

// Emoji is one entry in the table.
type Emoji struct {
	// Shortcode is the canonical name, without surrounding colons.
	Shortcode string
	// Glyph is the Unicode sequence.
	Glyph string
	// Aliases are alternative shortcodes for the same glyph.
	Aliases []string
	// Category groups emoji the way a picker would.
	Category string
	// Tags are extra search terms, e.g. "happy" for :grinning:.
	Tags []string
}

// Names returns the canonical shortcode followed by any aliases.
func (e Emoji) Names() []string {
	names := make([]string, 0, 1+len(e.Aliases))
	names = append(names, e.Shortcode)
	names = append(names, e.Aliases...)
	return names
}

var (
	indexOnce  sync.Once
	byName     map[string]Emoji // every shortcode and alias
	byGlyph    map[string]Emoji // canonical glyph lookup
	sortedKeys []string
)

func buildIndex() {
	indexOnce.Do(func() {
		byName = make(map[string]Emoji, len(table)*2)
		byGlyph = make(map[string]Emoji, len(table))
		sortedKeys = make([]string, 0, len(table)*2)

		for _, e := range table {
			for _, name := range e.Names() {
				if _, taken := byName[name]; !taken {
					byName[name] = e
					sortedKeys = append(sortedKeys, name)
				}
			}
			if _, taken := byGlyph[e.Glyph]; !taken {
				byGlyph[e.Glyph] = e
			}
		}
		sort.Strings(sortedKeys)
	})
}

// Count is how many emoji the table holds.
func Count() int { return len(table) }

// Lookup resolves a shortcode to its glyph. The name may be given with or
// without surrounding colons.
func Lookup(name string) (string, bool) {
	buildIndex()
	e, ok := byName[normalize(name)]
	if !ok {
		return "", false
	}
	return e.Glyph, true
}

// Shortcode resolves a glyph back to its canonical shortcode, without colons.
func Shortcode(glyph string) (string, bool) {
	buildIndex()
	e, ok := byGlyph[glyph]
	if !ok {
		return "", false
	}
	return e.Shortcode, true
}

// normalize strips surrounding colons and lowercases.
func normalize(name string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(name), ":"))
}

// Replace substitutes every known `:shortcode:` in text with its glyph.
//
// Two rules keep this from mangling ordinary text. An unknown shortcode is left
// exactly as written, because a name we do not recognise is more useful shown
// than silently deleted. And a shortcode must not open directly after an
// alphanumeric character, which is what stops "a:b:c" becoming "a🅱️c" and
// "10:30:45" losing its middle — `:b:` really is an emoji, so matching it
// anywhere would corrupt timestamps, ratios and URLs.
func Replace(text string) string {
	if !strings.ContainsRune(text, ':') {
		return text
	}
	buildIndex()

	var b strings.Builder
	b.Grow(len(text))

	for i := 0; i < len(text); {
		if text[i] != ':' || !opensShortcode(text, i) {
			b.WriteByte(text[i])
			i++
			continue
		}
		end := closingColon(text, i)
		if end < 0 {
			b.WriteByte(text[i])
			i++
			continue
		}
		if e, ok := byName[strings.ToLower(text[i+1:end])]; ok {
			b.WriteString(e.Glyph)
			i = end + 1
			continue
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

// opensShortcode reports whether a colon may begin a shortcode: only at the
// start of the text, or after something that is not a letter or digit. Adjacent
// codes still work, since the preceding character is then a colon.
func opensShortcode(text string, i int) bool {
	if i == 0 {
		return true
	}
	prev := text[i-1]
	switch {
	case prev >= 'a' && prev <= 'z', prev >= 'A' && prev <= 'Z', prev >= '0' && prev <= '9':
		return false
	default:
		return true
	}
}

// closingColon returns the index of the colon closing a shortcode that opens at
// start, or -1 when the run is not shortcode-shaped.
func closingColon(text string, start int) int {
	const maxShortcode = 64
	for i := start + 1; i < len(text) && i-start <= maxShortcode; i++ {
		c := text[i]
		if c == ':' {
			if i == start+1 {
				return -1 // "::" is not a shortcode
			}
			return i
		}
		if !isShortcodeByte(c) {
			return -1
		}
	}
	return -1
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

// Match is one search result.
type Match struct {
	Emoji
	// Name is the shortcode that matched, which may be an alias.
	Name string
}

// Search returns emoji whose shortcode or tags match the query, best first.
//
// Ranking is: exact name, then name prefix, then name substring, then tag. An
// empty query returns the start of the table so a picker has something to show
// before anything is typed.
func Search(query string, limit int) []Match {
	buildIndex()
	if limit <= 0 {
		limit = 20
	}

	query = normalize(query)
	if query == "" {
		matches := make([]Match, 0, limit)
		for _, e := range table {
			if len(matches) == limit {
				break
			}
			matches = append(matches, Match{Emoji: e, Name: e.Shortcode})
		}
		return matches
	}

	type scored struct {
		match Match
		rank  int
	}
	var results []scored
	seen := make(map[string]bool)

	for _, name := range sortedKeys {
		e := byName[name]
		rank := -1
		switch {
		case name == query:
			rank = 0
		case strings.HasPrefix(name, query):
			rank = 1
		case strings.Contains(name, query):
			rank = 2
		case matchesTag(e, query):
			rank = 3
		}
		if rank < 0 || seen[e.Glyph] {
			continue
		}
		seen[e.Glyph] = true
		results = append(results, scored{Match{Emoji: e, Name: name}, rank})
	}

	// Shorter names first within a rank: ":joy:" should beat ":joy_cat:".
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].rank != results[j].rank {
			return results[i].rank < results[j].rank
		}
		if len(results[i].match.Name) != len(results[j].match.Name) {
			return len(results[i].match.Name) < len(results[j].match.Name)
		}
		return results[i].match.Name < results[j].match.Name
	})

	matches := make([]Match, 0, min(limit, len(results)))
	for _, result := range results {
		if len(matches) == limit {
			break
		}
		matches = append(matches, result.match)
	}
	return matches
}

func matchesTag(e Emoji, query string) bool {
	for _, tag := range e.Tags {
		if strings.HasPrefix(tag, query) {
			return true
		}
	}
	return false
}

// Common is a small set of quick-reaction emoji, in the order a picker should
// offer them.
func Common() []Match {
	names := []string{
		"+1", "-1", "smile", "joy", "heart", "tada", "eyes",
		"rocket", "white_check_mark", "x", "pray", "fire",
	}
	buildIndex()
	matches := make([]Match, 0, len(names))
	for _, name := range names {
		if e, ok := byName[name]; ok {
			matches = append(matches, Match{Emoji: e, Name: name})
		}
	}
	return matches
}
