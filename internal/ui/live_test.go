//go:build livetest

// Drives the real TUI against a live Rocket.Chat server and prints the rendered
// frames, so the whole stack can be inspected end to end:
//
//	RC_SERVER=https://chat.example.com RC_USER=me RC_PASS=… \
//	  go test -tags livetest -run TestLiveScreens -v ./internal/ui/
//
// The model is driven directly rather than through tea.Program, because that
// gives exact frames instead of a stream of ANSI redraws.
package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/config"
	"github.com/geekgonecrazy/rocketchat-tui/internal/store"
)

// driver steps a tea.Model by hand, pumping commands and messages.
type driver struct {
	t     *testing.T
	model tea.Model
	msgs  chan tea.Msg
}

func newDriver(t *testing.T, model tea.Model) *driver {
	return &driver{t: t, model: model, msgs: make(chan tea.Msg, 256)}
}

// run executes a command off-loop and queues whatever it produces. Batches are
// expanded, since a batch is just a list of commands.
func (d *driver) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		switch typed := msg.(type) {
		case nil:
		case tea.BatchMsg:
			for _, inner := range typed {
				d.run(inner)
			}
		default:
			select {
			case d.msgs <- msg:
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

// send delivers a message immediately and pumps the resulting command.
func (d *driver) send(msg tea.Msg) {
	model, cmd := d.model.Update(msg)
	d.model = model
	d.run(cmd)
}

// pump drains queued messages for up to window, updating the model.
func (d *driver) pump(window time.Duration) {
	deadline := time.After(window)
	for {
		select {
		case msg := <-d.msgs:
			d.send(msg)
		case <-deadline:
			return
		}
	}
}

// waitForView pumps until the rendered view contains fragment.
func (d *driver) waitForView(fragment string, window time.Duration) bool {
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if strings.Contains(d.model.View(), fragment) {
			return true
		}
		select {
		case msg := <-d.msgs:
			d.send(msg)
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}

func (d *driver) typeText(text string) {
	for _, r := range text {
		d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (d *driver) frame(label string) {
	d.t.Logf("\n===== %s =====\n%s\n", label, d.model.View())
}

func TestLiveScreens(t *testing.T) {
	server, user, pass := os.Getenv("RC_SERVER"), os.Getenv("RC_USER"), os.Getenv("RC_PASS")
	if server == "" || user == "" || pass == "" {
		t.Skip("set RC_SERVER, RC_USER and RC_PASS to drive the live UI")
	}

	dir := t.TempDir()
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cache, err := store.Open(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer cache.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := NewRoot(ctx, cfg, cache, nil, server)
	d := newDriver(t, root)
	d.run(root.Init())
	d.send(tea.WindowSizeMsg{Width: 100, Height: 32})

	if !d.waitForView("Rocket.Chat", 10*time.Second) {
		t.Fatalf("login screen never rendered:\n%s", d.model.View())
	}
	d.frame("login screen")

	// Server is prefilled from the override, so focus starts on the username.
	d.typeText(user)
	d.send(tea.KeyMsg{Type: tea.KeyEnter})
	d.typeText(pass)
	d.send(tea.KeyMsg{Type: tea.KeyEnter})

	if !d.waitForView("connected", 45*time.Second) {
		t.Fatalf("never reached a connected chat screen:\n%s", d.model.View())
	}
	// Let history and the realtime connection settle.
	d.pump(6 * time.Second)
	d.frame("chat screen, first room open")

	// Walk the sidebar so every room kind gets rendered and loaded.
	d.send(tea.KeyMsg{Type: tea.KeyTab}) // composer -> rooms
	for i := 0; i < 5; i++ {
		d.send(tea.KeyMsg{Type: tea.KeyDown})
	}
	d.pump(500 * time.Millisecond)
	d.frame("sidebar walked to the end")

	// Open whatever the cursor landed on.
	d.send(tea.KeyMsg{Type: tea.KeyEnter})
	d.pump(6 * time.Second)
	d.frame("second room open")

	// ctrl+t must reach the thread list from any focus, including the composer,
	// which is where opening a room leaves you.
	d.send(tea.KeyMsg{Type: tea.KeyCtrlT})
	d.pump(5 * time.Second)
	d.frame("thread list via ctrl+t")

	// Clicking a room in the sidebar must open it.
	d.send(tea.MouseMsg{X: 3, Y: headerRows + 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	d.pump(5 * time.Second)
	d.frame("room opened by clicking the sidebar")

	// Both overlays need focus off the composer, which is where opening a room
	// leaves it: "?" and "," are ordinary characters while you are writing.
	d.send(tea.KeyMsg{Type: tea.KeyTab}) // composer -> rooms

	// Help overlay, to confirm the key reference renders.
	d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	d.pump(300 * time.Millisecond)
	d.frame("help overlay")

	// Settings, driven rather than just shown: the pane is a list of switches,
	// so the frame worth looking at is one after the cursor has moved.
	d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{','}})
	d.send(tea.KeyMsg{Type: tea.KeyDown})
	d.pump(300 * time.Millisecond)
	d.frame("settings pane")
	d.send(tea.KeyMsg{Type: tea.KeyEsc})
	d.pump(300 * time.Millisecond)

	if strings.Contains(d.model.View(), "disconnected") {
		t.Error("the client reports itself disconnected from a reachable server")
	}
}
