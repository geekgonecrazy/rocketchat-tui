package ui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Composing in $EDITOR. ctrl+g writes the draft to a file, hands the terminal
// over, and replaces the composer with whatever was saved.
//
// The whole design rests on the file being pre-seeded and always at the same
// path: quitting the editor without saving leaves the draft sitting in it, so
// the composer gets its own content back and no code implements "cancel". The
// same holds for a non-zero exit, which is why an empty file can be honoured
// literally as "clear the composer" — anyone wanting to bail had two better
// exits than saving nothing.

// openEditor seeds the draft file and hands the terminal to the user's editor.
func (m chatModel) openEditor() (chatModel, tea.Cmd) {
	if m.composePath == "" {
		return m.notify("no data directory to write the draft to", true)
	}
	editor := m.cfg.EditorCommand(os.Getenv)
	if editor == "" {
		return m.notify("set $EDITOR to compose in an external editor", false)
	}

	if err := os.MkdirAll(filepath.Dir(m.composePath), 0o700); err != nil {
		return m.notify("could not create "+shortenPath(filepath.Dir(m.composePath))+": "+err.Error(), true)
	}
	// Written before the terminal is handed over, and a failure keeps it: an
	// editor opening on a file we could not seed would show the previous draft
	// and then paste it back over this one.
	if err := os.WriteFile(m.composePath, []byte(m.composer.Value()), 0o600); err != nil {
		return m.notify("could not write "+shortenPath(m.composePath)+": "+err.Error(), true)
	}

	m.notice, m.noticeErr = "", false
	return m, tea.ExecProcess(editorCmd(editor, m.composePath), func(err error) tea.Msg {
		return editorFinishedMsg{err: err}
	})
}

// editorCmd builds the command that opens path in editor.
//
// The path is passed as an argument to sh rather than pasted into the script, so
// it arrives as "$1" and is never parsed: spaces and shell metacharacters in
// $XDG_DATA_HOME are safe, while a multi-word EDITOR with flags and quoting of
// its own — code --wait, or an emacsclient invocation — still works as written.
func editorCmd(editor, path string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		// No shell to lean on, so the command is split on spaces. An editor path
		// that is quoted because it contains a space is knowingly unsupported.
		fields := strings.Fields(editor)
		if len(fields) == 0 {
			return exec.Command(editor, path)
		}
		return exec.Command(fields[0], append(fields[1:], path)...)
	}
	return exec.Command("sh", "-c", editor+` "$@"`, "sh", path)
}

// editorClosed reacts to the editor handing the terminal back.
func (m chatModel) editorClosed(msg editorFinishedMsg) (chatModel, tea.Cmd) {
	if msg.err != nil {
		var exit *exec.ExitError
		if errors.As(msg.err, &exit) {
			// :cq is a deliberate abort, not a fault, and the composer still holds
			// what it held — so this is news rather than an error.
			return m.notify("editor exited without saving; draft unchanged", false)
		}
		return m.notify("could not launch editor: "+msg.err.Error(), true)
	}

	saved, err := os.ReadFile(m.composePath)
	if err != nil {
		// The composer is left alone and the file is still there, which is the
		// whole reason it is never deleted.
		return m.notify("could not read "+shortenPath(m.composePath)+": "+err.Error(), true)
	}

	text := normalizeDraft(string(saved))
	m.composer.SetValue(text)
	m.composer.SetHeight(clamp(strings.Count(text, "\n")+1, 1, 4))

	// Closed, not synced. Text arriving wholesale from an editor is finished, not
	// mid-typing, and a message ending in "@dou" would otherwise leave the
	// mention completer open to swallow the enter meant to send it.
	m.cmdPicker.close()
	m.mentions.close()
	m.picker.close()
	m.rebuildBody()

	// Same rule as the typed path: rewriting a message you already sent is not
	// composing, so it stays quiet.
	if m.activeRoom != "" && !m.editing() {
		if strings.TrimSpace(text) == "" {
			m.core.StopTyping(m.activeRoom)
		} else {
			m.core.UserTyping(m.activeRoom)
		}
	}
	return m, nil
}

// normalizeDraft turns what an editor saved into what the composer should hold.
//
// Deliberately only two things: CRLF, which Windows editors write, and the
// trailing newline every well-behaved editor adds. Leading and interior
// whitespace is content in a chat client — indented code, fenced blocks,
// deliberate blank lines — and the trim is on "\n" alone, so a trailing space
// inside a code fence survives.
func normalizeDraft(s string) string {
	return strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
