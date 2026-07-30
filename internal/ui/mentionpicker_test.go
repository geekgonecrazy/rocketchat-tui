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
		sigil byte
		query string
		start int
		ok    bool
	}{
		{"@", '@', "", 0, true},
		{"@al", '@', "al", 0, true},
		{"hey @al", '@', "al", 4, true},
		{"hey @Al", '@', "al", 4, true},
		{"(@al", '@', "al", 1, true},
		{"hey @charlie.brown", '@', "charlie.brown", 4, true},
		// Rooms open the same way, on the other sigil.
		{"#", '#', "", 0, true},
		{"see #gen", '#', "gen", 4, true},
		{"see #Gen", '#', "gen", 4, true},
		{"(#gen", '#', "gen", 1, true},
		// Not mentions: an address, a mid-word sigil, and no sigil at all.
		{"aaron@fide", 0, "", 0, false},
		{"x@y", 0, "", 0, false},
		{"issue#42", 0, "", 0, false},
		{"hello", 0, "", 0, false},
		{"", 0, "", 0, false},
		// The mention has to be what is being typed, not something further back.
		{"@alice says", 0, "", 0, false},
		{"#general says", 0, "", 0, false},
	}

	for _, tc := range cases {
		start, sigil, query, ok := mentionToken(tc.text)
		if ok != tc.ok {
			t.Errorf("mentionToken(%q) ok = %v, want %v", tc.text, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if start != tc.start || sigil != tc.sigil || query != tc.query {
			t.Errorf("mentionToken(%q) = (%d, %q, %q), want (%d, %q, %q)",
				tc.text, start, string(sigil), query, tc.start, string(tc.sigil), tc.query)
		}
	}
}

// sampleMemberMentions is the roster as the completer consumes it.
func sampleMemberMentions() []model.Mention {
	mentions := make([]model.Mention, 0, len(sampleMembers()))
	for _, member := range sampleMembers() {
		mentions = append(mentions, member.Mention())
	}
	return mentions
}

func TestMentionMatchRanking(t *testing.T) {
	// "b" prefixes bob and appears inside charlie.brown, so bob must come first.
	matches := matchMentions(sampleMemberMentions(), specialMentions, "b", mentionLimit)
	if len(matches) < 2 {
		t.Fatalf("expected at least two matches, got %d", len(matches))
	}
	if matches[0].Value != "bob" {
		t.Errorf("first match = %q, want bob", matches[0].Value)
	}

	// A name-only match still counts: "patel" is not in any username.
	matches = matchMentions(sampleMemberMentions(), specialMentions, "patel", mentionLimit)
	if len(matches) != 1 || matches[0].Value != "sanjay" {
		t.Errorf("name match = %+v, want sanjay", matches)
	}

	// Group mentions are offered, but never ahead of a person.
	matches = matchMentions(sampleMemberMentions(), specialMentions, "a", mentionLimit)
	if matches[0].Value != "alice" {
		t.Errorf("first match = %q, want alice ahead of @all", matches[0].Value)
	}
	if !containsValue(matches, "all") {
		t.Error("@all missing from the candidates for a")
	}

	// The group mentions are matched on their value only: the descriptive text
	// standing in for their display name must not drag them into unrelated
	// queries.
	matches = matchMentions(sampleMemberMentions(), specialMentions, "everyone", mentionLimit)
	if containsValue(matches, "all") || containsValue(matches, "here") {
		t.Errorf("group mention matched its description: %+v", matches)
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
	if !ok || selected.Value != "charlie.brown" {
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
	if first.Value == second.Value {
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
		if containsValue(sampleMemberMentions(), match.Value) {
			t.Errorf("completer offered %q from the previous room", match.Value)
		}
	}
}

// ---- channels ---------------------------------------------------------------

func TestChannelCompleterOpensOnHash(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "see also #")

	if !m.mentions.active() {
		t.Fatal("completer not open after typing #")
	}
	if !containsValue(m.mentions.matches, "general") || !containsValue(m.mentions.matches, "random") {
		t.Errorf("channels missing from the candidates: %+v", m.mentions.matches)
	}

	view := m.View()
	if !strings.Contains(view, "#general") {
		t.Errorf("candidate list missing #general:\n%s", view)
	}
}

func TestChannelCompleterFiltersAndInserts(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "see also #ran")

	if m.mentions.query != "ran" {
		t.Errorf("query = %q, want ran", m.mentions.query)
	}
	selected, ok := m.mentions.selected()
	if !ok || selected.Value != "random" {
		t.Fatalf("selection = %+v, want random", selected)
	}

	m, _ = m.Update(press("tab"))
	if got := m.composer.Value(); got != "see also #random " {
		t.Errorf("composer = %q, want %q", got, "see also #random ")
	}
	if m.mentions.active() {
		t.Error("completer should close after accepting")
	}
}

// A "#" mention names a room, so the sigil must switch which list is offered
// even when a person shares the name.
func TestChannelCompleterOffersRoomsNotPeople(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "#")

	for _, match := range m.mentions.matches {
		if match.Sigil != "#" {
			t.Errorf("non-room candidate %+v offered for #", match)
		}
	}
	// "@all" and "@here" are typed with an "@" and mean nothing after a "#".
	if containsValue(m.mentions.matches, "all") || containsValue(m.mentions.matches, "here") {
		t.Errorf("group mentions offered for #: %+v", m.mentions.matches)
	}
}

// DMs carry a name on the wire — the other person — but are addressed by person
// rather than pointed at, so they are never offered after a "#".
func TestChannelCompleterSkipsDirectMessages(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "#a")
	if containsValue(m.mentions.matches, "alice") {
		t.Errorf("completer offered the DM with alice: %+v", m.mentions.matches)
	}

	// Nothing else is even close, so the list gets out of the way entirely.
	m = typeInto(m, "lice")
	if m.mentions.active() {
		t.Errorf("completer stayed open on a DM name: %+v", m.mentions.matches)
	}
}

func TestChannelCompleterStaysClosedMidWord(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "fixed issue#gen")

	if m.mentions.active() {
		t.Errorf("completer opened inside a word (query=%q)", m.mentions.query)
	}
}

// Switching sigils mid-token must re-rank rather than leave the cursor pointing
// into the previous list.
func TestCompleterSwitchesBetweenSigils(t *testing.T) {
	m := mentionChat(t)
	m = typeInto(m, "@")
	m, _ = m.Update(press("down"))
	if selected, _ := m.mentions.selected(); selected.Sigil != "@" {
		t.Fatalf("expected a person, got %+v", selected)
	}

	m.composer.Reset()
	m = typeInto(m, "#")
	selected, ok := m.mentions.selected()
	if !ok || selected.Sigil != "#" {
		t.Fatalf("expected a room, got %+v", selected)
	}
	if m.mentions.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after switching lists", m.mentions.cursor)
	}
}

func containsValue(mentions []model.Mention, value string) bool {
	for _, mention := range mentions {
		if mention.Value == value {
			return true
		}
	}
	return false
}
