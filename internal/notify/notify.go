// Package notify raises a message out of the terminal and onto the desktop.
//
// A terminal client spends most of its life behind another window, so the
// sidebar going bold is not a notification — it is a thing you find later. This
// package is the part that reaches the user who is not looking.
//
// Two channels, chosen independently because they fail independently: a desktop
// notification, and a sound. Each has a fallback that works where the preferred
// mechanism does not, which in practice means "over SSH", where there is no
// desktop session to talk to and the terminal at the far end is the only thing
// that can make noise.
package notify

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// deliverTimeout bounds a helper that hangs. notify-send blocks until the
// notification daemon answers, and a wedged daemon must not leave a goroutine —
// or a sound process — around for the rest of the session.
const deliverTimeout = 5 * time.Second

// Notification is one thing worth interrupting the user for.
type Notification struct {
	Title string
	Body  string
}

// Prefs is what the user has left switched on. The zero value is silent, which
// makes "no preferences" fail safe here; config.go decides what the actual
// default is, and it is not this.
type Prefs struct {
	Desktop bool
	Sound   bool

	// SoundCommand is run with `sh -c` when set, instead of ringing the terminal
	// bell. It is the user's own config file, so it gets the same trust as their
	// shell rc — the point of the field is to let them write "paplay ~/ping.wav"
	// with the tilde and the quoting they would use anywhere else.
	SoundCommand string
}

// Notifier delivers notifications. The zero value is not usable; call New.
type Notifier struct {
	// out is where terminal escape sequences go — the same descriptor the TUI
	// renders into.
	out io.Writer
	// terminal reports whether out is one, since writing an escape sequence into
	// a pipe just puts rubbish in it.
	terminal bool

	// writeMu serialises our own writes to out. It does not, and cannot,
	// synchronise with the renderer's writes; see writeTerminal.
	writeMu sync.Mutex

	// helperOnce resolves the desktop helper at most once. Which one exists is a
	// property of the machine, and probing the PATH per notification would be
	// work done repeatedly to reach the same answer.
	helperOnce sync.Once
	helper     []string
}

// New builds a Notifier writing terminal escapes to out. Pass os.Stdout in the
// running program; tests pass a buffer, which is also how they stay silent.
func New(out io.Writer, isTerminal bool) *Notifier {
	return &Notifier{out: out, terminal: isTerminal}
}

// NewStdout builds the Notifier the program actually uses.
func NewStdout() *Notifier {
	return New(os.Stdout, isTerminal(os.Stdout))
}

// Deliver raises one notification, honouring prefs. It blocks for as long as the
// helpers take, so callers on a render loop should run it off one — see
// ui.deliverNotification.
func (n *Notifier) Deliver(prefs Prefs, note Notification) {
	if prefs.Desktop {
		n.desktop(note)
	}
	if prefs.Sound {
		n.sound(prefs.SoundCommand)
	}
}

// ---- desktop ----------------------------------------------------------------

// desktop shows a notification through whatever the machine provides, falling
// back to asking the terminal to do it.
func (n *Notifier) desktop(note Notification) {
	title := sanitize(note.Title)
	body := sanitize(note.Body)
	if title == "" && body == "" {
		return
	}

	if args := n.desktopHelper(); args != nil {
		if err := runHelper(args, title, body); err == nil {
			return
		}
		// A helper that is installed but failed — no D-Bus session, a locked
		// screen, a daemon that went away — is exactly the case the terminal
		// fallback exists for, so fall through rather than give up.
	}
	n.osc777(title, body)
}

// desktopHelper resolves the argv for a desktop notification, or nil when this
// machine has none. The two placeholders are filled by runHelper.
func (n *Notifier) desktopHelper() []string {
	n.helperOnce.Do(func() {
		if runtime.GOOS == "darwin" {
			// terminal-notifier is nicer when it is there: osascript notifications
			// are attributed to Script Editor, which is confusing, and it cannot
			// show one at all when the machine has no GUI session.
			if path, err := exec.LookPath("terminal-notifier"); err == nil {
				n.helper = []string{path, "-title", titlePlaceholder, "-message", bodyPlaceholder}
				return
			}
			if path, err := exec.LookPath("osascript"); err == nil {
				// argv rather than an interpolated script: message text is arbitrary
				// user input, and building AppleScript source out of it means every
				// quote and backslash in a message is a way to break — or rewrite —
				// the script.
				n.helper = []string{path,
					"-e", "on run argv",
					"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
					"-e", "end run",
					"--", bodyPlaceholder, titlePlaceholder,
				}
				return
			}
			return
		}
		if path, err := exec.LookPath("notify-send"); err == nil {
			// "--" so a message beginning with a dash is not read as a flag.
			n.helper = []string{path, "--app-name=rctui", "--", titlePlaceholder, bodyPlaceholder}
		}
	})
	return n.helper
}

// Placeholders stand in for the title and body inside a helper's argv, so the
// argument order can differ per helper without the caller knowing about it.
// They are substituted, never concatenated, which is what keeps message text
// from being read as anything but one argument.
const (
	titlePlaceholder = "\x00title\x00"
	bodyPlaceholder  = "\x00body\x00"
)

func runHelper(args []string, title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), deliverTimeout)
	defer cancel()

	filled := make([]string, len(args))
	for i, arg := range args {
		switch arg {
		case titlePlaceholder:
			filled[i] = title
		case bodyPlaceholder:
			filled[i] = body
		default:
			filled[i] = arg
		}
	}

	cmd := exec.CommandContext(ctx, filled[0], filled[1:]...)
	// A helper must never inherit the terminal: anything it prints would land in
	// the middle of the TUI, and anything it read would steal keystrokes.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	return cmd.Run()
}

// osc777 asks the terminal emulator to raise the notification itself. This is
// the path that works over SSH, where the desktop is at the other end of the
// connection and no helper on this machine can reach it. kitty, wezterm, foot
// and iTerm2 all understand it; terminals that do not simply ignore it, which is
// why nothing here can report whether it worked.
func (n *Notifier) osc777(title, body string) {
	// The title is a field inside the sequence, so a semicolon in it would end
	// the field early and push the rest into the body. The body is last and can
	// keep its own.
	title = strings.ReplaceAll(title, ";", ",")
	n.writeTerminal("\x1b]777;notify;" + title + ";" + body + "\a")
}

// ---- sound ------------------------------------------------------------------

func (n *Notifier) sound(command string) {
	if strings.TrimSpace(command) == "" {
		// The bell, deliberately, rather than a bundled sound file: it is the one
		// mechanism that survives SSH, costs no subprocess, and is already wired to
		// whatever the user told their terminal to do about alerts — including
		// turning them into something visual, or into nothing.
		n.writeTerminal("\a")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), deliverTimeout)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		cancel()
		// A misconfigured player should still make *some* noise rather than
		// silently stop notifying.
		n.writeTerminal("\a")
		return
	}
	// Reaped off the caller's goroutine: a sound is something to start, not
	// something to wait for, and the caller may be a render loop.
	go func() {
		defer cancel()
		_ = cmd.Wait()
	}()
}

// ---- terminal ---------------------------------------------------------------

// writeTerminal emits a control sequence to the terminal.
//
// This shares a descriptor with Bubbletea's renderer, which we cannot lock
// against. It is safe in practice because of what is written rather than by
// arrangement: both sequences here are short enough for the tty layer to write
// atomically, and neither moves the cursor or changes any attribute, so even
// landing between two frames leaves nothing to repair.
func (n *Notifier) writeTerminal(sequence string) {
	if !n.terminal {
		return
	}
	n.writeMu.Lock()
	defer n.writeMu.Unlock()
	_, _ = io.WriteString(n.out, sequence)
}

// sanitize strips what must not reach a terminal or a helper: C0 controls, ESC,
// and DEL. Message text is arbitrary input from other people, and a notification
// body is one of the few places it would otherwise be handed to the terminal
// without passing through the renderer's escaping.
func sanitize(text string) string {
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
	return strings.TrimSpace(text)
}

// Truncate shortens a notification body to n runes, since a desktop
// notification shows a line or two and a long paste should not become a wall.
func Truncate(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	if n <= 1 {
		return "…"
	}
	return strings.TrimRight(string(runes[:n-1]), " ") + "…"
}

// isTerminal reports whether f is a terminal. It is a syscall rather than a
// guess so that piping rctui's output somewhere does not fill the pipe with
// escape sequences.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
