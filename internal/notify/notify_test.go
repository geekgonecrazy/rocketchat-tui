package notify

import (
	"bytes"
	"strings"
	"testing"
)

// deliverDesktopViaTerminal forces the OSC path by giving the notifier a
// notifier with no desktop helper resolved. helperOnce is pre-tripped with a nil
// helper, which is the state a machine with neither notify-send nor osascript
// leaves it in — and the state every SSH session is in.
func terminalOnly(out *bytes.Buffer) *Notifier {
	n := New(out, true)
	n.helperOnce.Do(func() { n.helper = nil })
	return n
}

func TestOSCFallbackCarriesTitleAndBody(t *testing.T) {
	var out bytes.Buffer
	n := terminalOnly(&out)
	n.Deliver(Prefs{Desktop: true}, Notification{Title: "alice in #dev", Body: "ping"})

	got := out.String()
	want := "\x1b]777;notify;alice in #dev;ping\a"
	if got != want {
		t.Errorf("OSC sequence = %q, want %q", got, want)
	}
}

func TestNotTerminalWritesNothing(t *testing.T) {
	var out bytes.Buffer
	n := New(&out, false)
	n.helperOnce.Do(func() { n.helper = nil })
	n.Deliver(Prefs{Desktop: true, Sound: true}, Notification{Title: "alice", Body: "ping"})

	if out.Len() != 0 {
		t.Errorf("wrote %q to a non-terminal; escape sequences must never reach a pipe", out.String())
	}
}

func TestSoundRingsTheBell(t *testing.T) {
	var out bytes.Buffer
	n := terminalOnly(&out)
	n.Deliver(Prefs{Sound: true}, Notification{Title: "alice", Body: "ping"})

	if out.String() != "\a" {
		t.Errorf("sound wrote %q, want a single BEL", out.String())
	}
}

func TestPrefsOffDeliverNothing(t *testing.T) {
	var out bytes.Buffer
	n := terminalOnly(&out)
	n.Deliver(Prefs{}, Notification{Title: "alice", Body: "ping"})

	if out.Len() != 0 {
		t.Errorf("wrote %q with everything switched off", out.String())
	}
}

// A message is text other people wrote. If it reaches the terminal unfiltered,
// anyone who can message you can move your cursor, repaint your screen, or
// close the OSC sequence early and have the rest run as something else.
func TestMessageTextCannotInjectEscapeSequences(t *testing.T) {
	var out bytes.Buffer
	n := terminalOnly(&out)
	n.Deliver(Prefs{Desktop: true}, Notification{
		Title: "mallory\x1b[2J",
		Body:  "hi\x07\x1b]0;pwned\x07 there\nsecond line",
	})

	got := out.String()
	body := strings.TrimSuffix(strings.TrimPrefix(got, "\x1b]777;notify;"), "\a")
	if strings.ContainsAny(body, "\x1b\x07\n") {
		t.Errorf("control characters survived sanitising: %q", got)
	}
	if !strings.Contains(got, "mallory") || !strings.Contains(got, "there") {
		t.Errorf("sanitising ate the actual message: %q", got)
	}
}

// A semicolon in the title would otherwise end the field and shunt the rest of
// the title into the body.
func TestSemicolonInTitleDoesNotEndTheField(t *testing.T) {
	var out bytes.Buffer
	n := terminalOnly(&out)
	n.Deliver(Prefs{Desktop: true}, Notification{Title: "a;b", Body: "body"})

	got := out.String()
	// Three, and only three: the two in "777;notify;" plus the one separating
	// title from body. A fourth would mean the title's own semicolon survived.
	if strings.Count(got, ";") != 3 {
		t.Errorf("title semicolon was not neutralised: %q", got)
	}
	if !strings.HasSuffix(got, ";body\a") {
		t.Errorf("body should still be the last field: %q", got)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		text string
		n    int
		want string
	}{
		{"under the limit is untouched", "hello", 10, "hello"},
		{"exactly the limit is untouched", "hello", 5, "hello"},
		{"over the limit gets an ellipsis", "hello there", 6, "hello…"},
		{"trailing space is not left before the ellipsis", "ab cdef", 4, "ab…"},
		{"a limit of one is just the ellipsis", "hello", 1, "…"},
		// Runes, not bytes: cutting mid-rune would put a replacement character in
		// the notification, and a language whose characters are all multi-byte
		// would be truncated to a fraction of the requested length.
		{"counts runes rather than bytes", "日本語テキスト", 4, "日本語…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Truncate(tc.text, tc.n); got != tc.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.text, tc.n, got, tc.want)
			}
		})
	}
}

func TestSanitizeKeepsPrintableUnicode(t *testing.T) {
	if got := sanitize("  héllo 👋 world  "); got != "héllo 👋 world" {
		t.Errorf("sanitize mangled printable text: %q", got)
	}
}
