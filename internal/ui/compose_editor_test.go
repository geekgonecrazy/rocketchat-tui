package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

func TestNormalizeDraft(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"crlf becomes lf", "one\r\ntwo\r\n", "one\ntwo"},
		{"trailing newlines go", "hello\n\n\n", "hello"},
		{"leading whitespace is content", "    indented code\n", "    indented code"},
		{"interior blank lines are content", "para one\n\npara two\n", "para one\n\npara two"},
		// Only "\n" is trimmed, so a trailing space inside a fence survives.
		{"trailing space survives", "```\ncode \n```\n", "```\ncode \n```"},
		{"empty file stays empty", "", ""},
		{"a file of newlines is empty", "\n\n", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeDraft(tc.input); got != tc.want {
				t.Errorf("normalizeDraft(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestEditorCmdArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shell form is what these pin")
	}

	cases := []struct {
		name   string
		editor string
		path   string
	}{
		{"plain editor", "nvim", "/tmp/compose.md"},
		{"editor with flags", "code --wait", "/tmp/compose.md"},
		{"editor path with a space", "/opt/My Editor/bin/edit", "/tmp/compose.md"},
		// The one that matters: the path is an argument, never text the shell
		// parses, so a data directory with a space in it still opens one file.
		{"compose path with a space", "nvim", "/home/some one/.local/share/rctui/compose.md"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := editorCmd(tc.editor, tc.path)
			want := []string{"sh", "-c", tc.editor + ` "$@"`, "sh", tc.path}
			if !reflect.DeepEqual(cmd.Args, want) {
				t.Errorf("args = %q, want %q", cmd.Args, want)
			}
		})
	}
}

// writeDraft stands in for the editor having saved.
func writeDraft(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write draft: %v", err)
	}
}

func readFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	return string(raw), err
}

// newEditorChat is a chat model in a room, with a draft file of its own.
func newEditorChat(t *testing.T) chatModel {
	t.Helper()
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.composePath = filepath.Join(t.TempDir(), "compose.md")
	return m
}

func TestOpenEditorSeedsTheDraftFile(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "true")

	m := newEditorChat(t)
	m.composer.SetValue("half-written thought")

	m, cmd := m.openEditor()
	if cmd == nil {
		t.Fatal("openEditor should hand the terminal over")
	}
	saved, err := readFile(m.composePath)
	if err != nil {
		t.Fatalf("draft file: %v", err)
	}
	if saved != "half-written thought" {
		t.Errorf("draft file holds %q, want the composer's text", saved)
	}
}

func TestOpenEditorRefusesWithNoEditorConfigured(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	m := newEditorChat(t)
	m.composer.SetValue("still mine")

	m, _ = m.openEditor()
	if !strings.Contains(m.notice, "$EDITOR") {
		t.Errorf("notice = %q, want it to say what to set", m.notice)
	}
	if m.noticeErr {
		t.Error("an unset $EDITOR is a thing to fix, not an error")
	}
	// The terminal was never handed over, so nothing was written either.
	if _, err := readFile(m.composePath); err == nil {
		t.Error("the draft file should not exist when the editor never opened")
	}
	if m.composer.Value() != "still mine" {
		t.Errorf("composer = %q, want it untouched", m.composer.Value())
	}
}

// TestComposerCtrlGReachesTheEditor pins the binding itself: the textarea does
// not claim ctrl+g, so the key has to arrive here rather than being typed.
func TestComposerCtrlGReachesTheEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	m := newEditorChat(t)
	if m.focus != focusComposer {
		t.Fatalf("focus = %v, want composer", m.focus)
	}
	m.composer.SetValue("mid-sentence")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if !strings.Contains(m.notice, "$EDITOR") {
		t.Errorf("notice = %q, want ctrl+g to have reached openEditor", m.notice)
	}
	if m.composer.Value() != "mid-sentence" {
		t.Errorf("composer = %q, want ctrl+g not to be typed into it", m.composer.Value())
	}
}

func TestEditorClosedReplacesTheComposer(t *testing.T) {
	m := newEditorChat(t)
	m.composer.SetValue("draft")
	writeDraft(t, m.composePath, "first line\r\nsecond line\r\nthird line\n\n")

	// Something the completers would have latched onto if they were synced.
	m.members = []model.Member{{Username: "douglas"}}
	m.mentions.sync("hi @dou", m.mentionCandidates)
	if !m.mentions.active() {
		t.Fatal("test setup: the mention completer should be open")
	}

	m, _ = m.Update(editorFinishedMsg{})

	if want := "first line\nsecond line\nthird line"; m.composer.Value() != want {
		t.Errorf("composer = %q, want %q", m.composer.Value(), want)
	}
	if m.composer.Height() != 3 {
		t.Errorf("composer height = %d, want 3", m.composer.Height())
	}
	// Closed rather than resynced: an open mention list would eat the enter that
	// is meant to send this.
	if m.mentions.active() || m.cmdPicker.active() || m.picker.active() {
		t.Error("all three completers should be closed after paste-back")
	}
	if m.notice != "" {
		t.Errorf("notice = %q, want nothing to report on success", m.notice)
	}
}

func TestEditorClosedClampsHeightToFourLines(t *testing.T) {
	m := newEditorChat(t)
	writeDraft(t, m.composePath, strings.Repeat("a line\n", 20))

	m, _ = m.Update(editorFinishedMsg{})
	if m.composer.Height() != 4 {
		t.Errorf("composer height = %d, want the four-line clamp", m.composer.Height())
	}
}

func TestEditorClosedOnAnEmptyFileClearsTheComposer(t *testing.T) {
	m := newEditorChat(t)
	m.composer.SetValue("line one\nline two")
	m.composer.SetHeight(2)
	writeDraft(t, m.composePath, "")

	m, _ = m.Update(editorFinishedMsg{})
	if m.composer.Value() != "" {
		t.Errorf("composer = %q, want it cleared", m.composer.Value())
	}
	if m.composer.Height() != 1 {
		t.Errorf("composer height = %d, want 1", m.composer.Height())
	}
}

func TestEditorClosedKeepsTheEditItIsInsideOf(t *testing.T) {
	m := newEditorChat(t)
	m = event(m, app.TimelineUpdated{
		RoomID:   "r1",
		Messages: []model.Message{{ID: "mine", Username: "tester", Text: "typo here", Own: true}},
	})
	m.editID = "mine"
	writeDraft(t, m.composePath, "typo fixed\n")

	m, _ = m.Update(editorFinishedMsg{})
	if m.editID != "mine" {
		t.Errorf("editID = %q, want the edit to survive composing in the editor", m.editID)
	}
	if m.composer.Value() != "typo fixed" {
		t.Errorf("composer = %q", m.composer.Value())
	}
}

func TestEditorClosedNonZeroExitLeavesTheDraftAlone(t *testing.T) {
	m := newEditorChat(t)
	m.composer.SetValue("what I was writing")
	writeDraft(t, m.composePath, "saved then aborted")

	// What :cq produces, and what a crashing editor produces too.
	failed := exec.Command("sh", "-c", "exit 1").Run()
	if failed == nil {
		t.Fatal("test setup: expected a non-zero exit")
	}

	m, _ = m.Update(editorFinishedMsg{err: failed})
	if m.composer.Value() != "what I was writing" {
		t.Errorf("composer = %q, want it unchanged", m.composer.Value())
	}
	if m.noticeErr {
		t.Errorf("notice %q is flagged as an error, but :cq is a legitimate exit", m.notice)
	}
	if !strings.Contains(m.notice, "without saving") {
		t.Errorf("notice = %q, want it to say the draft is unchanged", m.notice)
	}
}

func TestEditorClosedLaunchFailureIsAnError(t *testing.T) {
	m := newEditorChat(t)
	m.composer.SetValue("what I was writing")

	_, err := exec.LookPath("rctui-no-such-editor")
	if err == nil {
		t.Skip("that editor apparently exists")
	}

	m, _ = m.Update(editorFinishedMsg{err: err})
	if m.composer.Value() != "what I was writing" {
		t.Errorf("composer = %q, want it unchanged", m.composer.Value())
	}
	if !m.noticeErr {
		t.Error("an editor that could not be launched is an error")
	}
	if !strings.Contains(m.notice, "could not launch editor") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestEditorClosedUnreadableDraftLeavesTheComposerAlone(t *testing.T) {
	m := newEditorChat(t)
	m.composer.SetValue("what I was writing")
	// No file at all: whatever went wrong, the composer is the surviving copy.

	m, _ = m.Update(editorFinishedMsg{})
	if m.composer.Value() != "what I was writing" {
		t.Errorf("composer = %q, want it unchanged", m.composer.Value())
	}
	if !m.noticeErr {
		t.Error("a draft that cannot be read back is an error")
	}
}
