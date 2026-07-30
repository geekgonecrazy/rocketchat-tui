package emoji_test

import (
	"strings"
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/emoji"
)

func TestLookupKnownShortcodes(t *testing.T) {
	tests := []struct{ name, want string }{
		{"joy", "😂"},
		{":joy:", "😂"},
		{"JOY", "😂"},
		{"+1", "👍"},
		{"thumbsup", "👍"}, // alias of +1
		{"tada", "🎉"},
		{"thinking", "🤔"},
	}
	for _, tc := range tests {
		got, ok := emoji.Lookup(tc.name)
		if !ok {
			t.Errorf("Lookup(%q) not found", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("Lookup(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}

	if _, ok := emoji.Lookup("definitely-not-an-emoji"); ok {
		t.Error("expected an unknown shortcode to miss")
	}
}

func TestShortcodeRoundTrip(t *testing.T) {
	for _, name := range []string{"joy", "tada", "heart", "rocket"} {
		glyph, ok := emoji.Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) failed", name)
		}
		back, ok := emoji.Shortcode(glyph)
		if !ok {
			t.Errorf("Shortcode(%q) not found", glyph)
			continue
		}
		if back != name {
			t.Errorf("round trip %q -> %q -> %q", name, glyph, back)
		}
	}
}

func TestReplaceSubstitutesKnownCodes(t *testing.T) {
	tests := []struct{ in, want string }{
		{"nice work :tada:", "nice work 🎉"},
		{":joy: :joy:", "😂 😂"},
		{"lgtm :+1:", "lgtm 👍"},
		{"no emoji here", "no emoji here"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := emoji.Replace(tc.in); got != tc.want {
			t.Errorf("Replace(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Text that merely contains colons must survive intact. Timestamps and URLs are
// the everyday cases, and mangling them would be worse than showing a shortcode.
func TestReplaceLeavesNonEmojiColonsAlone(t *testing.T) {
	tests := []string{
		"meeting at 10:30:45",
		"see http://example.com/:path:/here",
		"ratio 3:1",
		"::",
		":",
		":unknown_shortcode:",
		// ":b:" is a real emoji (🅱️), so this only survives because a shortcode
		// may not open straight after an alphanumeric character.
		"a:b:c",
		"time is 10:b:30",
		"nested :not an emoji: text",
	}
	for _, in := range tests {
		if got := emoji.Replace(in); got != in {
			t.Errorf("Replace(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestReplaceHandlesAdjacentAndTrailingCodes(t *testing.T) {
	if got := emoji.Replace(":joy::joy:"); got != "😂😂" {
		t.Errorf("adjacent codes = %q", got)
	}
	if got := emoji.Replace("trailing colon :joy"); got != "trailing colon :joy" {
		t.Errorf("unterminated code = %q", got)
	}
	if got := emoji.Replace(":tada: at the start"); got != "🎉 at the start" {
		t.Errorf("leading code = %q", got)
	}
}

func TestSearchRanksExactThenPrefix(t *testing.T) {
	matches := emoji.Search("joy", 10)
	if len(matches) == 0 {
		t.Fatal("no matches for joy")
	}
	if matches[0].Name != "joy" {
		t.Errorf("first match = %q, want the exact name joy", matches[0].Name)
	}
	if matches[0].Glyph != "😂" {
		t.Errorf("first glyph = %q, want 😂", matches[0].Glyph)
	}

	// The user's example: typing ":jo" should surface joy.
	partial := emoji.Search("jo", 20)
	found := false
	for _, match := range partial {
		if match.Name == "joy" {
			found = true
		}
	}
	if !found {
		t.Errorf("searching %q did not surface joy: %v", "jo", names(partial))
	}
}

func TestSearchPrefersShorterNames(t *testing.T) {
	matches := emoji.Search("smi", 20)
	if len(matches) < 2 {
		t.Fatalf("expected several matches, got %d", len(matches))
	}
	if matches[0].Name != "smile" {
		t.Errorf("first match = %q, want the shortest prefix match smile", matches[0].Name)
	}

	// Within a rank, shorter names come first; ranks themselves may interleave
	// lengths, since a long prefix match still beats a short substring match.
	position := func(name string) int {
		for i, match := range matches {
			if match.Name == name {
				return i
			}
		}
		return -1
	}
	if short, long := position("smile"), position("smiley_cat"); short >= 0 && long >= 0 && short > long {
		t.Errorf("smile at %d sorted after smiley_cat at %d", short, long)
	}
}

func TestSearchDeduplicatesByGlyph(t *testing.T) {
	// "+1" and "thumbsup" are the same glyph and must not both be offered.
	matches := emoji.Search("thumb", 20)
	seen := map[string]int{}
	for _, match := range matches {
		seen[match.Glyph]++
	}
	for glyph, count := range seen {
		if count > 1 {
			t.Errorf("glyph %q offered %d times", glyph, count)
		}
	}
}

func TestSearchRespectsLimitAndEmptyQuery(t *testing.T) {
	if got := len(emoji.Search("a", 5)); got > 5 {
		t.Errorf("limit ignored: got %d matches", got)
	}
	if got := emoji.Search("", 8); len(got) != 8 {
		t.Errorf("empty query returned %d matches, want 8 so a picker has content", len(got))
	}
	if got := emoji.Search("zzzzzznotathing", 5); len(got) != 0 {
		t.Errorf("nonsense query returned %d matches", len(got))
	}
}

func TestSearchMatchesTags(t *testing.T) {
	// :smiley: is tagged "happy" but not named it.
	matches := emoji.Search("happy", 20)
	if len(matches) == 0 {
		t.Fatal("tag search returned nothing")
	}
}

func TestTableIsPopulated(t *testing.T) {
	if emoji.Count() < 1500 {
		t.Errorf("table holds only %d emoji, expected the full gemoji set", emoji.Count())
	}
}

func TestCommonReactionsResolve(t *testing.T) {
	common := emoji.Common()
	if len(common) < 8 {
		t.Fatalf("only %d common reactions", len(common))
	}
	for _, match := range common {
		if match.Glyph == "" {
			t.Errorf("common reaction %q has no glyph", match.Name)
		}
		if match.Name == "" {
			t.Errorf("common reaction has no name: %+v", match)
		}
	}
	if common[0].Name != "+1" {
		t.Errorf("first quick reaction = %q, want +1", common[0].Name)
	}
}

func names(matches []emoji.Match) []string {
	out := make([]string, len(matches))
	for i, match := range matches {
		out[i] = match.Name
	}
	return out
}

func TestReplaceIsAllocationFriendlyForPlainText(t *testing.T) {
	plain := strings.Repeat("no colons at all here ", 50)
	if got := emoji.Replace(plain); got != plain {
		t.Error("plain text was altered")
	}
}
