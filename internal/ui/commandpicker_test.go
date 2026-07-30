package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// sampleCommands is a registry shaped like a real one: the client's own, a
// fallback, something the server contributed, and one nobody can run.
func sampleCommands() []model.Command {
	return []model.Command{
		{Name: "archive", Description: "Archive the room", Scope: model.ScopeServer},
		{Name: "exit", Description: "leave rctui", Scope: model.ScopeClient},
		{Name: "invite", Params: "@username…", Description: "add people to this room", Scope: model.ScopeLocal},
		{Name: "jitsi", Description: "Start a video call", Scope: model.ScopeUnsupported},
		{Name: "leave", Description: "leave this room", Scope: model.ScopeLocal},
		{Name: "open", Params: "<room>", Description: "jump to a room in the sidebar", Scope: model.ScopeClient},
		{Name: "upload", Params: "[path]", Description: "attach a file", Scope: model.ScopeClient},
	}
}

func chatWithCommands(t *testing.T) chatModel {
	t.Helper()
	m := openChat(t)
	return event(m, app.CommandsUpdated{Commands: sampleCommands()})
}

func TestCommandTokenOnlyMatchesABareName(t *testing.T) {
	cases := []struct {
		text  string
		query string
		ok    bool
	}{
		{"/", "", true},
		{"/le", "le", true},
		{"/LEAVE", "leave", true},
		{"/lenny-face", "lenny-face", true},
		// One space ends the name, and with it the completion: the rest of the
		// line belongs to the argument, and to the "@" completer.
		{"/invite ", "", false},
		{"/invite @jo", "", false},
		{"", "", false},
		{"hello", "", false},
		{"see /leave", "", false},
		{"/usr/bin", "", false},
	}

	for _, tc := range cases {
		query, ok := commandToken(tc.text)
		if ok != tc.ok || query != tc.query {
			t.Errorf("commandToken(%q) = (%q, %v), want (%q, %v)", tc.text, query, ok, tc.query, tc.ok)
		}
	}
}

// A bare slash opens the list, because which commands exist is a property of
// the server and nobody can be expected to know it.
func TestBareSlashOpensTheCommandList(t *testing.T) {
	m := chatWithCommands(t)
	m = typeInto(m, "/")

	if !m.cmdPicker.active() {
		t.Fatal("/ should open the command list")
	}
	if len(m.cmdPicker.matches) != 6 {
		t.Errorf("offered %d commands, want 6 (the unrunnable one is not offered)", len(m.cmdPicker.matches))
	}
	for _, match := range m.cmdPicker.matches {
		if match.Name == "jitsi" {
			t.Error("a command nobody can run should not be offered")
		}
	}
}

func TestCommandListFiltersAsYouType(t *testing.T) {
	m := chatWithCommands(t)
	m = typeInto(m, "/le")

	if len(m.cmdPicker.matches) != 1 || m.cmdPicker.matches[0].Name != "leave" {
		t.Fatalf("matches = %+v, want just leave", m.cmdPicker.matches)
	}

	m, _ = m.Update(press("tab"))
	if got := m.composer.Value(); got != "/leave " {
		t.Errorf("composer = %q, want %q", got, "/leave ")
	}
	if m.cmdPicker.active() {
		t.Error("completing should close the list")
	}
}

// The trailing space a completion leaves behind is what lets the "@" completer
// take over, which is the whole reason "/invite @jane" is typeable.
func TestArgumentsHandOffToTheMentionCompleter(t *testing.T) {
	m := chatWithCommands(t)
	m = event(m, app.MembersUpdated{RoomID: m.activeRoom, Members: []model.Member{
		{Username: "jane", Name: "Jane"},
	}})
	m = typeInto(m, "/invite @ja")

	if m.cmdPicker.active() {
		t.Error("the command list should be closed once an argument is being typed")
	}
	if !m.mentions.active() {
		t.Fatal("the mention completer should have taken over")
	}
	if len(m.mentions.matches) == 0 || m.mentions.matches[0].Value != "jane" {
		t.Errorf("mention matches = %+v, want jane", m.mentions.matches)
	}
}

// Enter completes a prefix, the way it does in the other two completers, but a
// command typed out in full is a command: pressing enter again to run something
// already spelled out would be a tax with nothing on the other side of it.
func TestEnterCompletesAPrefixAndRunsAFullyTypedCommand(t *testing.T) {
	m := chatWithCommands(t)
	m = typeInto(m, "/le")
	m, _ = m.Update(press("enter"))
	if got := m.composer.Value(); got != "/leave " {
		t.Fatalf("composer = %q, want the completion", got)
	}

	m = chatWithCommands(t)
	m = typeInto(m, "/exit")
	_, cmd := m.Update(press("enter"))
	if !quits(cmd) {
		t.Error("a command typed in full should run on enter")
	}
}

// quits reports whether a command is (or batches) tea.Quit.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case []tea.Msg:
		for _, m := range msg {
			if _, ok := m.(tea.QuitMsg); ok {
				return true
			}
		}
	}
	return false
}

func TestExitQuits(t *testing.T) {
	m := chatWithCommands(t)
	m = typeInto(m, "/exit ")
	_, cmd := m.Update(press("enter"))
	if !quits(cmd) {
		t.Error("/exit should quit")
	}
}

// The registry the model is built with already holds the client's commands, so
// /exit works on a screen that has not finished loading — which is the screen
// someone is most likely to want out of.
func TestExitWorksBeforeDiscoveryHasLanded(t *testing.T) {
	m := newTestChat(t)
	m.focus = focusComposer
	m.composer.Focus()
	m = typeInto(m, "/exit ")
	_, cmd := m.Update(press("enter"))
	if !quits(cmd) {
		t.Error("/exit should quit before any registry has arrived")
	}
}

// An unrecognised command is never posted as a message: the line stays in the
// composer to be corrected.
func TestUnknownCommandKeepsTheLineAndSaysSo(t *testing.T) {
	m := chatWithCommands(t)
	m = typeInto(m, "/inivte @jane")
	m, _ = m.Update(press("enter"))

	if got := m.composer.Value(); got != "/inivte @jane" {
		t.Errorf("composer = %q, want the line kept for correction", got)
	}
	if !strings.Contains(m.notice, "no such command") || !m.noticeErr {
		t.Errorf("notice = %q (err %v), want a refusal", m.notice, m.noticeErr)
	}
}

// A room that will not take messages will still take commands: leaving one is
// exactly what a read-only room is for.
func TestCommandsRunInAReadOnlyRoom(t *testing.T) {
	m := chatWithCommands(t)
	m.room = model.Room{ID: m.activeRoom, ReadOnly: true}
	m = typeInto(m, "/leave ")
	m, _ = m.Update(press("enter"))

	if m.composer.Value() != "" {
		t.Errorf("composer = %q, want the command consumed", m.composer.Value())
	}
	if m.notice != "" {
		t.Errorf("notice = %q, want the command to have been dispatched without complaint", m.notice)
	}
}

func TestOpenJumpsToARoomByName(t *testing.T) {
	m := chatWithCommands(t)
	m = typeInto(m, "/open random")
	m, _ = m.Update(press("enter"))

	if m.activeRoom != "r3" {
		t.Errorf("active room = %q, want r3", m.activeRoom)
	}
	if m.composer.Value() != "" {
		t.Errorf("composer = %q, want cleared", m.composer.Value())
	}
}

func TestOpenSaysSoWhenThereIsNoSuchRoom(t *testing.T) {
	m := chatWithCommands(t)
	before := m.activeRoom
	m = typeInto(m, "/open nowhere")
	m, _ = m.Update(press("enter"))

	if m.activeRoom != before {
		t.Errorf("active room moved to %q", m.activeRoom)
	}
	if !strings.Contains(m.notice, "no room here called nowhere") {
		t.Errorf("notice = %q", m.notice)
	}
}

// A room left elsewhere — by a command, or by another client — takes the
// timeline with it rather than leaving a room the user no longer has on screen.
func TestRoomClosedMovesOffTheRoom(t *testing.T) {
	m := chatWithCommands(t)
	if m.activeRoom != "r1" {
		t.Fatalf("expected to start in r1, got %q", m.activeRoom)
	}

	remaining := sampleRooms()[1:]
	m = event(m, app.RoomsUpdated{Rooms: remaining})
	m = event(m, app.RoomClosed{RoomID: "r1"})

	if m.activeRoom != "r2" {
		t.Errorf("active room = %q, want the next room in the sidebar", m.activeRoom)
	}
}

func TestMatchCommandsRanksPrefixesFirstAndFindsDescriptions(t *testing.T) {
	matches := matchCommands(sampleCommands(), "a", commandLimit)
	if len(matches) == 0 || matches[0].Name != "archive" {
		t.Fatalf("matches = %+v, want archive first", matches)
	}
	// "people" appears only in a description, which is how someone finds a
	// command whose name says nothing about what they want.
	matches = matchCommands(sampleCommands(), "people", commandLimit)
	if len(matches) != 1 || matches[0].Name != "invite" {
		t.Errorf("matches = %+v, want invite by description", matches)
	}
	// Two characters are in half the descriptions by accident, so short queries
	// search names only.
	for _, match := range matchCommands(sampleCommands(), "le", commandLimit) {
		if match.Name != "leave" {
			t.Errorf("%q matched a two-character query through its description", match.Name)
		}
	}
}
