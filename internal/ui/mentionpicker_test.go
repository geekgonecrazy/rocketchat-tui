package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

func sampleMembers() []model.Member {
	return []model.Member{
		{Username: "alice", Name: "Alice Adams"},
		{Username: "bob", Name: "Bob Barker"},
		{Username: "charlie.brown", Name: "Charlie Brown"},
		{Username: "sanjay", Name: "Sanjay Patel"},
	}
}

// mentionChat is a chat model sitting in a room with a known roster, focused on
// the composer.
func mentionChat(t *testing.T) chatModel {
	t.Helper()
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.MembersUpdated{RoomID: m.activeRoom, Members: sampleMembers()})
	m.focus = focusComposer
	return m
}

func typeInto(m chatModel, text string) chatModel {
	for _, r := range text {
		m, _ = m.Update(press(string(r)))
	}
	return m
}

func TestMentionTokenRules(t *testing.T) {
	cases := []struct {
		text  string
		query string
		start int
		ok    bool
	}{
		{"@", "", 0, true},
		{"@al", "al", 0, true},
		{"hey @al", "al", 4, true},
		{"hey @Al", "al", 4, true},
		{"(@al", "al", 1, true},
		{"hey @charlie.brown", "charlie.brown", 4, true},
		// Not mentions: an address, a mid-word "@", and no "@" at all.
		{"aaron@fide", "", 0, false},
		{"x@y", "", 0, false},
		{"hello", "", 0, false},
		{"", "", 0, false},
		// The mention has to be what is being typed, not something further back.
		{"@alice says", "", 0, false},
	}

	for _, tc := range cases {
		start, query, ok := mentionToken(tc.text)
		if ok != tc.ok {
			t.Errorf("mentionToken(%q) ok = %v, want %v", tc.text, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if start != tc.start || query != tc.query {
			t.Errorf("mentionToken(%q) = (%d, %q), want (%d, %q)",
				tc.text, start, query, tc.start, tc.query)
		}
	}
}

func TestMentionMatchRanking(t *testing.T) {
	// "b" prefixes bob and appears inside charlie.brown, so bob must come first.
	matches := matchMembers(sampleMembers(), "b", mentionLimit)
	if len(matches) < 2 {
		t.Fatalf("expected at least two matches, got %d", len(matches))
	}
	if matches[0].Username != "bob" {
		t.Errorf("first match = %q, want bob", matches[0].Username)
	}

	// A name-only match still counts: "patel" is not in any username.
	matches = matchMembers(sampleMembers(), "patel", mentionLimit)
	if len(matches) != 1 || matches[0].Username != "sanjay" {
		t.Errorf("name match = %+v, want sanjay", matches)
	}

	// Group mentions are offered, but never ahead of a person.
	matches = matchMembers(sampleMembers(), "a", mentionLimit)
	if matches[0].Username != "alice" {
		t.Errorf("first match = %q, want alice ahead of @all", matches[0].Username)
	}
	if !containsUsername(matches, "all") {
		t.Error("@all missing from the candidates for a")
	}
}

func TestMentionCompleterOpensOnAt(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "morning @")

	if !m.mentions.active() {
		t.Fatal("completer not open after typing @")
	}
	if len(m.mentions.matches) == 0 {
		t.Fatal("no candidates offered")
	}

	view := m.View()
	if !strings.Contains(view, "@alice") {
		t.Errorf("candidate list missing alice:\n%s", view)
	}
}

func TestMentionCompleterFiltersAsYouType(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "@ch")

	if m.mentions.query != "ch" {
		t.Errorf("query = %q, want ch", m.mentions.query)
	}
	selected, ok := m.mentions.selected()
	if !ok || selected.Username != "charlie.brown" {
		t.Errorf("selection = %+v, want charlie.brown", selected)
	}
}

func TestMentionCompleterStaysClosedInsideAnAddress(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "mail me at aaron@fide")

	if m.mentions.active() {
		t.Errorf("completer opened inside an address (query=%q)", m.mentions.query)
	}
}

func TestMentionCompleterClosesWhenNothingMatches(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "@al")
	if !m.mentions.active() {
		t.Fatal("expected the completer to be open")
	}

	m = typeInto(m, "zzzqqq")
	if m.mentions.active() {
		t.Error("completer stayed open with no matches")
	}
}

func TestMentionCompletionInsertsUsername(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "ping @sa")
	if !m.mentions.active() {
		t.Fatal("completer not open")
	}

	m, _ = m.Update(press("tab"))

	if got := m.composer.Value(); got != "ping @sanjay " {
		t.Errorf("composer = %q, want %q", got, "ping @sanjay ")
	}
	if m.mentions.active() {
		t.Error("completer should close after accepting")
	}
}

func TestMentionCompleterNavigationAndDismiss(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "@")

	first, _ := m.mentions.selected()
	m, _ = m.Update(press("down"))
	second, _ := m.mentions.selected()
	if first.Username == second.Username {
		t.Error("down did not move the selection")
	}

	before := m.composer.Value()
	m, _ = m.Update(press("esc"))
	if m.mentions.active() {
		t.Error("esc did not dismiss the completer")
	}
	if m.composer.Value() != before {
		t.Errorf("esc altered the composer: %q", m.composer.Value())
	}
}

func TestMentionCompleterEnterAcceptsRatherThanSends(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "@bo")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.composer.Value(); got != "@bob " {
		t.Errorf("enter should have completed the mention, composer = %q", got)
	}
}

// The two completers must not both claim the same keystrokes.
func TestMentionAndEmojiCompletersDoNotOverlap(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "@al")
	if m.picker.active() {
		t.Error("emoji completer opened on a mention")
	}

	m, _ = m.Update(press("esc"))
	m.composer.Reset()
	m = typeInto(m, ":jo")
	if m.mentions.active() {
		t.Error("mention completer opened on a shortcode")
	}
}

// Candidates belong to the room they came from.
func TestMentionCandidatesResetWhenTheRoomChanges(t *testing.T) {
	m := mentionChat(t)
	m, _ = m.Update(press("tab")) // leave the composer so room keys apply
	m.focus = focusRooms
	m, _ = m.Update(press("down"))
	m, _ = m.Update(press("enter"))

	if len(m.members) != 0 {
		t.Errorf("stale members carried into %q: %+v", m.activeRoom, m.members)
	}

	// The group mentions are always available, but nobody from the old room is.
	m = typeInto(m, "@")
	for _, match := range m.mentions.matches {
		if containsUsername(sampleMembers(), match.Username) {
			t.Errorf("completer offered %q from the previous room", match.Username)
		}
	}
}

func containsUsername(members []model.Member, username string) bool {
	for _, member := range members {
		if member.Username == username {
			return true
		}
	}
	return false
}
