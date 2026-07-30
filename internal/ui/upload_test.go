package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// openChat is a chat model sitting in a room with the composer focused.
func openChat(t *testing.T) chatModel {
	t.Helper()
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer
	m.composer.Focus()
	return m
}

// touch creates a file under dir and returns its path.
func touch(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestAttachPromptQueuesAFileWithoutSendingIt(t *testing.T) {
	dir := t.TempDir()
	path := touch(t, dir, "diagram.png", 4096)

	m := openChat(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.attach.open {
		t.Fatal("ctrl+o should open the attach prompt")
	}

	m = typeInto(m, path)
	m, _ = m.Update(press("enter"))

	if m.attach.open {
		t.Error("the prompt should close once the file is queued")
	}
	if len(m.uploads) != 1 || m.uploads[0].Name != "diagram.png" {
		t.Fatalf("uploads = %+v, want one diagram.png", m.uploads)
	}
	if m.uploads[0].MIME != "image/png" {
		t.Errorf("MIME = %q, want image/png", m.uploads[0].MIME)
	}

	view := m.View()
	if !strings.Contains(view, "diagram.png") || !strings.Contains(view, "4.0 KB") {
		t.Errorf("queued file should show above the composer:\n%s", view)
	}
}

func TestAttachPromptRestoresTheDraftItBorrowed(t *testing.T) {
	m := openChat(t)
	m = typeInto(m, "look at this")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.composer.Value() != "" {
		t.Errorf("the prompt should start empty, got %q", m.composer.Value())
	}
	m = typeInto(m, "/some/path")
	m, _ = m.Update(press("esc"))

	if m.attach.open {
		t.Error("esc should close the prompt")
	}
	if m.composer.Value() != "look at this" {
		t.Errorf("draft = %q, want the message back", m.composer.Value())
	}
	if len(m.uploads) != 0 {
		t.Error("a cancelled prompt must not queue anything")
	}
}

// A rejected path is nearly always a nearly-right path, so the prompt has to
// stay open with the text intact rather than making the user start again.
func TestAttachPromptKeepsABadPathForCorrection(t *testing.T) {
	m := openChat(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = typeInto(m, filepath.Join(t.TempDir(), "nope.png"))
	m, _ = m.Update(press("enter"))

	if !m.attach.open {
		t.Error("the prompt should stay open after a bad path")
	}
	if !strings.Contains(m.composer.Value(), "nope.png") {
		t.Errorf("the typed path should survive, got %q", m.composer.Value())
	}
	if len(m.uploads) != 0 {
		t.Error("nothing should have been queued")
	}
	if !m.noticeErr || !strings.Contains(m.notice, "no such file") {
		t.Errorf("notice = %q (err=%v), want a missing-file error", m.notice, m.noticeErr)
	}
}

func TestAttachPromptTabCompletesAPath(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "screenshot-one.png", 1)
	touch(t, dir, "screenshot-two.png", 1)
	touch(t, dir, "unrelated.txt", 1)

	m := openChat(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = typeInto(m, filepath.Join(dir, "screen"))
	m, _ = m.Update(press("tab"))

	// Two candidates share everything up to where they diverge, so completion
	// stops there rather than guessing which one was meant.
	want := filepath.Join(dir, "screenshot-")
	if m.composer.Value() != want {
		t.Errorf("completed to %q, want %q", m.composer.Value(), want)
	}
	if len(m.attach.matches) != 2 {
		t.Errorf("matches = %v, want the two screenshots", m.attach.matches)
	}
	view := m.View()
	if !strings.Contains(view, "screenshot-one.png") || strings.Contains(view, "unrelated.txt") {
		t.Errorf("candidates should be listed, and only the matching ones:\n%s", view)
	}

	// Unique from here, so the next tab finishes the name.
	m = typeInto(m, "o")
	m, _ = m.Update(press("tab"))
	if m.composer.Value() != filepath.Join(dir, "screenshot-one.png") {
		t.Errorf("a unique match should complete fully, got %q", m.composer.Value())
	}
}

// tab is focus-cycling everywhere else in the app; while a path is being typed
// it must not be.
func TestAttachPromptClaimsTabAndEnterFromTheGlobalBindings(t *testing.T) {
	m := openChat(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = typeInto(m, "/no-such-directory-here/x")

	before := m.focus
	m, _ = m.Update(press("tab"))
	if m.focus != before {
		t.Error("tab inside the prompt should complete, not change focus")
	}
	if !m.attach.open {
		t.Error("the prompt should still be open")
	}
}

func TestUploadCommandQueuesFromTheComposer(t *testing.T) {
	path := touch(t, t.TempDir(), "notes.txt", 100)

	m := openChat(t)
	m = typeInto(m, "/upload "+path)
	m, _ = m.Update(press("enter"))

	if len(m.uploads) != 1 || m.uploads[0].Name != "notes.txt" {
		t.Fatalf("uploads = %+v, want one notes.txt", m.uploads)
	}
	if m.composer.Value() != "" {
		t.Errorf("the command should be consumed, got %q", m.composer.Value())
	}
	if m.attach.open {
		t.Error("a command with a path should not open the prompt")
	}
}

func TestBareUploadCommandOpensThePrompt(t *testing.T) {
	m := openChat(t)
	m = typeInto(m, "/upload")
	m, _ = m.Update(press("enter"))

	if !m.attach.open {
		t.Error("bare /upload should open the prompt")
	}
}

// The upload command is parsed by the same rule as every other slash command
// now, so what matters here is that the rule still recognises the shapes a path
// arrives in — a path is the one argument likely to contain slashes and spaces.
func TestUploadCommandRecognition(t *testing.T) {
	cases := []struct {
		value string
		path  string
		ok    bool
	}{
		{"/upload", "", true},
		{"/upload ", "", true},
		{"/upload ~/a.png", "~/a.png", true},
		// Taken verbatim, so a name with spaces needs no quoting.
		{"/upload /tmp/my holiday.png", "/tmp/my holiday.png", true},
		{"/uploads/foo", "", false},
		{"upload /tmp/a.png", "", false},
		{"see /upload", "", false},
		{"/upload a.png\nand more", "", false},
	}

	for _, tc := range cases {
		name, path, ok := model.ParseCommand(tc.value)
		if ok != tc.ok || (ok && name != "upload") || path != tc.path {
			t.Errorf("ParseCommand(%q) = (%q, %q, %v), want (upload, %q, %v)",
				tc.value, name, path, ok, tc.path, tc.ok)
		}
	}
}

func TestSendPostsTheQueueAndClearsIt(t *testing.T) {
	dir := t.TempDir()
	m := openChat(t)
	for _, name := range []string{"one.png", "two.png"} {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
		m = typeInto(m, touch(t, dir, name, 10))
		m, _ = m.Update(press("enter"))
	}
	m = typeInto(m, "two shots")

	m, _ = m.Update(press("enter"))

	if len(m.uploads) != 0 {
		t.Errorf("the queue should be consumed by the send, got %+v", m.uploads)
	}
	if m.composer.Value() != "" {
		t.Errorf("composer = %q, want it cleared", m.composer.Value())
	}
	if strings.Contains(m.View(), "one.png") {
		t.Error("the chip line should be gone once sent")
	}
}

// Files alone are a message; the composer being empty must not block the send.
func TestSendWithFilesAndNoTextIsAllowed(t *testing.T) {
	m := openChat(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = typeInto(m, touch(t, t.TempDir(), "only.png", 10))
	m, _ = m.Update(press("enter"))

	m, _ = m.Update(press("enter"))
	if len(m.uploads) != 0 {
		t.Error("an empty composer with files queued should still send")
	}
}

func TestDropLastUploadRemovesOneAtATime(t *testing.T) {
	dir := t.TempDir()
	m := openChat(t)
	for _, name := range []string{"one.png", "two.png"} {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
		m = typeInto(m, touch(t, dir, name, 10))
		m, _ = m.Update(press("enter"))
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(m.uploads) != 1 || m.uploads[0].Name != "one.png" {
		t.Fatalf("uploads = %+v, want one.png left", m.uploads)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(m.uploads) != 0 {
		t.Errorf("uploads = %+v, want empty", m.uploads)
	}
	// Nothing left to remove is not an error, just nothing.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(m.uploads) != 0 {
		t.Error("removing from an empty queue should do nothing")
	}
}

func TestQueueIsDiscardedWhenTheRoomChanges(t *testing.T) {
	m := openChat(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = typeInto(m, touch(t, t.TempDir(), "wrong-room.png", 10))
	m, _ = m.Update(press("enter"))
	if len(m.uploads) != 1 {
		t.Fatalf("setup: uploads = %+v", m.uploads)
	}

	cmd := (&m).openRoom("r3")
	_ = cmd

	if len(m.uploads) != 0 {
		t.Errorf("uploads = %+v, want the queue left behind with the room", m.uploads)
	}
}

func TestAttachingIsRefusedInAReadOnlyRoom(t *testing.T) {
	m := openChat(t)
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Room:   model.Room{ID: "r1", DisplayName: "announcements", ReadOnly: true},
	})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.attach.open {
		t.Error("a read-only room has nothing to attach to")
	}
	if !strings.Contains(m.notice, "read-only") {
		t.Errorf("notice = %q, want it to say why", m.notice)
	}
}

// A sent message cannot gain a file: the server has no operation for it. Saying
// so beats opening a prompt whose result would be discarded.
func TestAttachingIsRefusedWhileEditing(t *testing.T) {
	m := openChat(t)
	m.editID = "msg-1"

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.attach.open {
		t.Error("editing and attaching are different things and cannot overlap")
	}
	if !strings.Contains(m.notice, "edit") {
		t.Errorf("notice = %q, want it to mention the edit", m.notice)
	}
}

func TestPathCompletionHidesDotfilesUntilAsked(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, ".hidden.png", 1)
	touch(t, dir, "visible.png", 1)

	if got := pathMatches(dir + "/"); len(got) != 1 || got[0] != "visible.png" {
		t.Errorf("matches = %v, want only the visible file", got)
	}
	if got := pathMatches(dir + "/."); len(got) != 1 || got[0] != ".hidden.png" {
		t.Errorf("matches = %v, want the dotfile once named", got)
	}
}

func TestPathCompletionMarksDirectoriesAndKeepsTilde(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "shots"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	completed, matches := completePath(filepath.Join(dir, "sh"))
	if completed != filepath.Join(dir, "shots")+"/" {
		t.Errorf("completed = %q, want a trailing slash so the next tab descends", completed)
	}
	if len(matches) != 1 || matches[0] != "shots/" {
		t.Errorf("matches = %v, want shots/", matches)
	}

	// "~" must survive completion rather than being rewritten into the expanded
	// home path under the user as they type.
	if completed, _ := completePath("~"); completed != "~/" {
		t.Errorf("completePath(\"~\") = %q, want \"~/\"", completed)
	}
}
