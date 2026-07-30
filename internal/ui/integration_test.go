package ui

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/config"
	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
	"github.com/geekgonecrazy/rocketchat-tui/internal/store"
)

// safeBuffer collects program output from the render goroutine.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runner drives a real Bubbletea program against a fake server.
type runner struct {
	t       *testing.T
	program *tea.Program
	out     *safeBuffer
	cfg     *config.Config
	done    chan struct{}
}

func newRunner(t *testing.T, server *fakerc.Server, dir string, withServerOverride bool) *runner {
	t.Helper()

	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cache, err := store.Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	override := ""
	if withServerOverride {
		override = server.URL
	}

	out := &safeBuffer{}
	program := tea.NewProgram(
		NewRoot(ctx, cfg, cache, nil, override),
		// A pipe that never yields stands in for a terminal: input is injected
		// with Send instead, so the test does not depend on key parsing.
		tea.WithInput(io.LimitReader(strings.NewReader(""), 0)),
		tea.WithOutput(out),
		tea.WithoutSignals(),
		tea.WithoutCatchPanics(),
	)

	r := &runner{t: t, program: program, out: out, cfg: cfg, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		if _, err := program.Run(); err != nil {
			t.Errorf("program.Run: %v", err)
		}
	}()

	program.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return r
}

func (r *runner) sendRunes(text string) {
	for _, rn := range text {
		r.program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rn}})
	}
}

func (r *runner) send(msg tea.Msg) { r.program.Send(msg) }

// waitForOutput polls the rendered output until it contains fragment.
func (r *runner) waitForOutput(fragment string) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(r.out.String(), fragment) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %q in output; last frame:\n%s",
		fragment, tailOutput(r.out.String()))
}

func (r *runner) quit() {
	r.program.Quit()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		r.t.Error("program did not exit")
	}
}

// tailOutput trims ANSI noise down to something readable in a failure message.
func tailOutput(s string) string {
	if len(s) > 4000 {
		s = s[len(s)-4000:]
	}
	return s
}

func TestEndToEndTokenLoginBrowseAndSend(t *testing.T) {
	server := fakerc.New(t)
	lastSeen := time.Now().Add(-time.Hour)
	server.AddRoom("room-1", "c", "general", map[string]any{"topic": "company wide"})
	server.AddSubscription("room-1", "c", "general", 2, 0, lastSeen, nil)
	server.AddRoom("room-2", "d", "alice", nil)
	server.AddSubscription("room-2", "d", "alice", 0, 0, lastSeen, nil)
	server.AddMessage("m1", "room-1", "alice", "seen before you left", lastSeen.Add(-time.Minute), nil)
	server.AddMessage("m2", "room-1", "bob", "arrived while away", lastSeen.Add(time.Minute), nil)

	r := newRunner(t, server, t.TempDir(), true)

	// The login form is up with the server prefilled from the override.
	r.waitForOutput("Rocket.Chat")

	// Switch to token auth and sign in.
	r.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	r.sendRunes(fakerc.AuthToken)
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	// Chat screen: sidebar, history, and the unread divider.
	r.waitForOutput("general")
	r.waitForOutput("seen before you left")
	r.waitForOutput("arrived while away")
	r.waitForOutput("new messages")

	// A live push lands in the open room.
	server.PushMessage("m3", "room-1", "carol", "pushed over websocket", time.Now(), nil)
	r.waitForOutput("pushed over websocket")

	// Someone else typing shows an indicator.
	server.PushTyping("room-1", "alice", true)
	r.waitForOutput("alice is typing")

	// Compose and send.
	r.sendRunes("hello from rctui")
	r.waitForOutput("hello from rctui")
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	waitUntil(t, "message posted to the server", func() bool {
		for _, sent := range server.SentMessages() {
			if sent.Text == "hello from rctui" && sent.RoomID == "room-1" {
				return true
			}
		}
		return false
	})

	r.quit()

	// Credentials are persisted so the next launch skips the login form.
	if !r.cfg.HasSession() {
		t.Error("expected the session to be saved after a successful login")
	}
}

func TestEndToEndResumesSavedSessionWithoutLoginForm(t *testing.T) {
	server := fakerc.New(t)
	server.AddRoom("room-1", "c", "general", nil)
	server.AddSubscription("room-1", "c", "general", 0, 0, time.Now().Add(-time.Hour), nil)
	server.AddMessage("m1", "room-1", "alice", "cached greeting", time.Now().Add(-time.Minute), nil)

	dir := t.TempDir()

	// First launch: log in with a token and let it save.
	first := newRunner(t, server, dir, true)
	first.waitForOutput("Rocket.Chat")
	first.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	first.sendRunes(fakerc.AuthToken)
	first.send(tea.KeyMsg{Type: tea.KeyEnter})
	first.waitForOutput("cached greeting")
	first.quit()

	// Second launch from the same directory: straight into the chat screen.
	second := newRunner(t, server, dir, false)
	second.waitForOutput("general")
	second.waitForOutput("cached greeting")

	if strings.Contains(second.out.String(), "terminal client") {
		t.Error("a saved session should not show the login form")
	}
	second.quit()
}

func TestEndToEndPasswordLoginWithTOTP(t *testing.T) {
	server := fakerc.New(t)
	server.RequireTOTP = true
	server.AddRoom("room-1", "c", "general", nil)
	server.AddSubscription("room-1", "c", "general", 0, 0, time.Now().Add(-time.Hour), nil)
	server.AddMessage("m1", "room-1", "alice", "two factor worked", time.Now().Add(-time.Minute), nil)

	r := newRunner(t, server, t.TempDir(), true)
	r.waitForOutput("Rocket.Chat")

	// Server is prefilled, so focus starts on the username field.
	r.sendRunes(fakerc.Username)
	r.send(tea.KeyMsg{Type: tea.KeyEnter}) // advance to password
	r.sendRunes(fakerc.Password)
	r.send(tea.KeyMsg{Type: tea.KeyEnter}) // submit; server demands a code

	r.waitForOutput("two-factor")

	r.sendRunes(fakerc.TOTPCode)
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	r.waitForOutput("two factor worked")
	r.quit()
}

func TestEndToEndRejectsBadPassword(t *testing.T) {
	server := fakerc.New(t)
	r := newRunner(t, server, t.TempDir(), true)
	r.waitForOutput("Rocket.Chat")

	r.sendRunes(fakerc.Username)
	r.send(tea.KeyMsg{Type: tea.KeyEnter})
	r.sendRunes("definitely-wrong")
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	r.waitForOutput("Incorrect username or password")
	if r.cfg.HasSession() {
		t.Error("a failed login must not save credentials")
	}
	r.quit()
}

func TestEndToEndSwitchRoomsLoadsHistory(t *testing.T) {
	server := fakerc.New(t)
	lastSeen := time.Now().Add(-time.Hour)
	server.AddRoom("room-1", "c", "general", nil)
	server.AddSubscription("room-1", "c", "general", 0, 0, lastSeen, nil)
	server.AddMessage("m1", "room-1", "alice", "first room message", lastSeen.Add(-time.Minute), nil)

	server.AddRoom("room-2", "p", "secret-plans", nil)
	server.AddSubscription("room-2", "p", "secret-plans", 0, 0, lastSeen, nil)
	server.AddMessage("m2", "room-2", "bob", "second room message", lastSeen.Add(-time.Minute), nil)

	r := newRunner(t, server, t.TempDir(), true)
	r.waitForOutput("Rocket.Chat")
	r.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	r.sendRunes(fakerc.AuthToken)
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	r.waitForOutput("first room message")

	// Focus the sidebar, move down, and open the other room.
	r.send(tea.KeyMsg{Type: tea.KeyTab}) // composer -> rooms
	r.send(tea.KeyMsg{Type: tea.KeyDown})
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	r.waitForOutput("second room message")
	r.quit()
}

// The point of completing a channel is that its mentionable slug and the name
// on screen are not the same string, so the end-to-end path has to carry both.
func TestEndToEndCompletesChannelMention(t *testing.T) {
	server := fakerc.New(t)
	lastSeen := time.Now().Add(-time.Hour)
	// room-1 has the most recent message, so it is the one that opens.
	server.AddRoom("room-1", "c", "general", map[string]any{
		"lm": lastSeen.UTC().Format(time.RFC3339Nano),
	})
	server.AddSubscription("room-1", "c", "general", 0, 0, lastSeen, nil)
	server.AddMessage("m1", "room-1", "alice", "first room message", lastSeen.Add(-time.Minute), nil)

	display := map[string]any{"fname": "Engineering Platform"}
	server.AddRoom("room-2", "c", "eng-platform", display)
	server.AddSubscription("room-2", "c", "eng-platform", 0, 0, lastSeen, display)

	r := newRunner(t, server, t.TempDir(), true)
	r.waitForOutput("Rocket.Chat")
	r.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	r.sendRunes(fakerc.AuthToken)
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	// Landing in a room leaves the composer focused, so this types into it.
	r.waitForOutput("first room message")
	r.sendRunes("see #eng")

	// The list offers the slug with the display name beside it — the name in the
	// sidebar is not what the server would resolve.
	r.waitForOutput("#eng-platform  Engineering Platform")

	r.send(tea.KeyMsg{Type: tea.KeyTab})
	r.waitForOutput("see #eng-platform")

	r.send(tea.KeyMsg{Type: tea.KeyEnter})
	waitUntil(t, "the completed mention was sent", func() bool {
		for _, sent := range server.SentMessages() {
			if sent.Text == "see #eng-platform" && sent.RoomID == "room-1" {
				return true
			}
		}
		return false
	})

	r.quit()
}

func TestEndToEndOpensThreadFromTimeline(t *testing.T) {
	server := fakerc.New(t)
	base := time.Now().Add(-time.Hour)
	server.AddRoom("room-1", "c", "general", nil)
	server.AddSubscription("room-1", "c", "general", 0, 0, base, nil)
	server.AddMessage("parent-1", "room-1", "alice", "thread starter", base,
		map[string]any{"tcount": 1, "tlm": base.Add(time.Minute).UTC().Format(time.RFC3339Nano)})
	server.AddMessage("reply-1", "room-1", "bob", "the threaded reply", base.Add(time.Minute),
		map[string]any{"tmid": "parent-1"})

	r := newRunner(t, server, t.TempDir(), true)
	r.waitForOutput("Rocket.Chat")
	r.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	r.sendRunes(fakerc.AuthToken)
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	r.waitForOutput("thread starter")
	// The reply is hidden from the main timeline.
	if strings.Contains(r.out.String(), "the threaded reply") {
		t.Error("a thread reply must not appear in the main timeline")
	}

	// Focus messages, select the newest, and open its thread.
	r.send(tea.KeyMsg{Type: tea.KeyTab}) // composer -> rooms
	r.send(tea.KeyMsg{Type: tea.KeyTab}) // rooms -> messages
	r.send(tea.KeyMsg{Type: tea.KeyUp})  // select newest
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	r.waitForOutput("the threaded reply")

	// Replying now targets the thread.
	r.sendRunes("my threaded answer")
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	waitUntil(t, "threaded reply posted", func() bool {
		for _, sent := range server.SentMessages() {
			if sent.Text == "my threaded answer" && sent.ThreadID == "parent-1" {
				return true
			}
		}
		return false
	})
	r.quit()
}

// The behaviour the whole feature turns on: files queue in the composer and go
// nowhere until the message they belong to is sent, and then they go as one
// message each, in the order they were attached.
func TestEndToEndAttachTwoFilesAndSendThemTogether(t *testing.T) {
	server := fakerc.New(t)
	server.AddRoom("room-1", "c", "general", nil)
	server.AddSubscription("room-1", "c", "general", 0, 0, time.Now().Add(-time.Hour), nil)
	server.AddMessage("m1", "room-1", "alice", "morning", time.Now().Add(-time.Minute), nil)

	dir := t.TempDir()
	png := filepath.Join(dir, "diagram.png")
	if err := os.WriteFile(png, fakerc.FilePNG, 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("some notes"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	r := newRunner(t, server, t.TempDir(), true)
	r.waitForOutput("Rocket.Chat")
	r.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	r.sendRunes(fakerc.AuthToken)
	r.send(tea.KeyMsg{Type: tea.KeyEnter})
	r.waitForOutput("morning")

	for _, path := range []string{png, txt} {
		r.send(tea.KeyMsg{Type: tea.KeyCtrlO})
		r.sendRunes(path)
		r.send(tea.KeyMsg{Type: tea.KeyEnter})
	}
	r.waitForOutput("diagram.png")
	r.waitForOutput("notes.txt")

	// Both are queued and visible. Nothing has been uploaded.
	if got := server.Uploads(); len(got) != 0 {
		t.Fatalf("attaching uploaded %d files; nothing should go out before send", len(got))
	}

	r.sendRunes("two things")
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	waitUntil(t, "both files uploaded", func() bool { return len(server.Uploads()) == 2 })
	uploads := server.Uploads()

	if uploads[0].Filename != "diagram.png" || uploads[1].Filename != "notes.txt" {
		t.Errorf("uploaded %q then %q, want them in the order they were attached",
			uploads[0].Filename, uploads[1].Filename)
	}
	// The image has to arrive declared as an image or no client will draw it.
	if uploads[0].MIME != "image/png" {
		t.Errorf("diagram.png declared as %q, want image/png", uploads[0].MIME)
	}
	if !strings.HasPrefix(uploads[1].MIME, "text/plain") {
		t.Errorf("notes.txt declared as %q, want text/plain", uploads[1].MIME)
	}
	// One message's worth of text, on one of the files rather than on both.
	if uploads[0].Text != "two things" {
		t.Errorf("text = %q, want it carried on the first file", uploads[0].Text)
	}
	if uploads[1].Text != "" {
		t.Errorf("second file carried %q; the text belongs to one message", uploads[1].Text)
	}
	if uploads[0].RoomID != "room-1" {
		t.Errorf("room = %q, want room-1", uploads[0].RoomID)
	}

	r.quit()
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEndToEndExpiredSessionReturnsToLoginWithoutQuitting(t *testing.T) {
	server := fakerc.New(t)
	server.AddRoom("room-1", "c", "general", nil)
	server.AddSubscription("room-1", "c", "general", 0, 0, time.Now().Add(-time.Hour), nil)
	server.AddMessage("m1", "room-1", "alice", "still here", time.Now().Add(-time.Minute), nil)

	dir := t.TempDir()

	// Log in and save a session.
	first := newRunner(t, server, dir, true)
	first.waitForOutput("Rocket.Chat")
	first.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	first.sendRunes(fakerc.AuthToken)
	first.send(tea.KeyMsg{Type: tea.KeyEnter})
	first.waitForOutput("still here")
	first.quit()

	// Corrupt the saved token so the next launch's first API call is rejected.
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.AuthToken = "no-longer-valid"
	if err := cfg.Save(); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	second := newRunner(t, server, dir, false)

	// The stale session must drop us back to the login form, not exit.
	second.waitForOutput("session expired")
	second.waitForOutput("Password")

	// The program is still alive and accepting input.
	second.sendRunes("someone")
	second.waitForOutput("someone")

	select {
	case <-second.done:
		t.Fatal("program exited instead of returning to the login form")
	case <-time.After(300 * time.Millisecond):
	}

	// The dead credentials are gone from disk.
	reloaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if reloaded.HasSession() {
		t.Error("an expired session should have been cleared from the config")
	}

	second.quit()
}

// Editing goes the whole way round: the composer recalls a message the user
// sent, the correction reaches chat.update, and the timeline redraws from the
// server's copy — "(edited)" included.
func TestEndToEndEditsTheLastMessageWithUpArrow(t *testing.T) {
	server := fakerc.New(t)
	lastSeen := time.Now().Add(-time.Hour)
	server.AddRoom("room-1", "c", "general", nil)
	server.AddSubscription("room-1", "c", "general", 0, 0, lastSeen, nil)
	server.AddMessage("m1", "room-1", "alice", "someone else's message", lastSeen, nil)

	r := newRunner(t, server, t.TempDir(), true)
	r.waitForOutput("Rocket.Chat")
	r.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	r.sendRunes(fakerc.AuthToken)
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	r.waitForOutput("someone else's message")

	// Post something of our own to correct afterwards.
	r.sendRunes("teh first draft")
	r.send(tea.KeyMsg{Type: tea.KeyEnter})
	waitUntil(t, "the message to be posted", func() bool {
		return len(server.SentMessages()) == 1
	})

	// ↑ in the empty composer pulls our own message back, not alice's.
	r.send(tea.KeyMsg{Type: tea.KeyUp})
	r.waitForOutput("editing a sent message")

	// Rewrite it and save.
	for i := 0; i < len("teh first draft"); i++ {
		r.send(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r.sendRunes("the first draft")
	r.send(tea.KeyMsg{Type: tea.KeyEnter})

	waitUntil(t, "the edit to reach the server", func() bool {
		for _, edit := range server.EditedMessages() {
			if edit.RoomID == "room-1" && edit.Text == "the first draft" {
				return true
			}
		}
		return false
	})

	// The server's copy is what redraws, so the edit marker appears with it.
	r.waitForOutput("(edited)")
	r.quit()
}
