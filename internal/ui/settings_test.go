package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/config"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

func TestSettingsPaneOpensAndCloses(t *testing.T) {
	m := newTestChat(t)
	m.focus = focusRooms

	m, _ = m.Update(press(","))
	if !m.settings.open {
		t.Fatal("expected , to open the settings pane")
	}
	view := m.View()
	for _, want := range []string{"Settings", "Desktop notifications", "Notification sound"} {
		if !strings.Contains(view, want) {
			t.Errorf("settings view missing %q:\n%s", want, view)
		}
	}

	m, _ = m.Update(press("esc"))
	if m.settings.open {
		t.Error("expected esc to close the settings pane")
	}
}

// The pane is a list of switches, not a notice. A stray key while reading it
// must not silently put the user back where they started.
func TestSettingsPaneIsNotDismissedByAnyKey(t *testing.T) {
	m := newTestChat(t)
	m.focus = focusRooms
	m, _ = m.Update(press(","))

	for _, key := range []string{"z", "?", "tab", "/"} {
		m, _ = m.Update(press(key))
		if !m.settings.open {
			t.Fatalf("%q closed the settings pane", key)
		}
	}
	// Quitting still has to work from anywhere.
	m, cmd := m.Update(press("ctrl+c"))
	if cmd == nil {
		t.Error("ctrl+c should still quit with the settings pane open")
	}
	_ = m
}

func TestSettingsToggleFlipsAndPersists(t *testing.T) {
	m := newTestChat(t)
	m.focus = focusRooms
	m, _ = m.Update(press(","))

	if !m.cfg.Notifications.DesktopEnabled() {
		t.Fatal("desktop notifications should start on")
	}
	m, _ = m.Update(press("enter"))
	if m.cfg.Notifications.DesktopEnabled() {
		t.Error("enter should have turned desktop notifications off")
	}
	if m.notice == "" || m.noticeErr {
		t.Errorf("expected a plain confirmation, got %q (err=%v)", m.notice, m.noticeErr)
	}

	// Down to the sound row and toggle that too, so the cursor is shown to
	// select rather than the toggle always hitting the first row.
	m, _ = m.Update(press("down"))
	m, _ = m.Update(press("enter"))
	if m.cfg.Notifications.SoundEnabled() {
		t.Error("enter on the second row should have turned the sound off")
	}
	if m.cfg.Notifications.DesktopEnabled() {
		t.Error("toggling sound must not have re-enabled desktop")
	}

	// Saved as you go: the pane has no confirm step, and a TUI is usually killed
	// rather than quit.
	reloaded, err := config.Load(configPath(t, m))
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Notifications.DesktopEnabled() || reloaded.Notifications.SoundEnabled() {
		t.Error("toggles were not written to disk as they were made")
	}
}

func TestSettingsCursorStaysInRange(t *testing.T) {
	m := newTestChat(t)
	m.focus = focusRooms
	m, _ = m.Update(press(","))

	for i := 0; i < 5; i++ {
		m, _ = m.Update(press("j"))
	}
	if m.settings.cursor != len(settingsOrder)-1 {
		t.Errorf("cursor = %d, want %d", m.settings.cursor, len(settingsOrder)-1)
	}
	for i := 0; i < 5; i++ {
		m, _ = m.Update(press("k"))
	}
	if m.settings.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.settings.cursor)
	}
}

// A comma is text in a message. Stealing it for settings the way "?" is stolen
// for help would make commas unwritable.
func TestCommaInTheComposerIsTyped(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	m, _ = m.Update(press(","))
	if m.settings.open {
		t.Error("typing a comma in the composer should not open settings")
	}
	if m.composer.Value() != "," {
		t.Errorf("composer = %q, want the comma to have been typed", m.composer.Value())
	}
}

// /settings is how the pane is reached from the composer, where the key is not
// available. Focus has to move with it, or the arrows go to a hidden text box.
func TestSettingsSlashCommandOpensThePaneAndTakesFocus(t *testing.T) {
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.focus = focusComposer

	m, _ = m.runComposedCommand("settings", "")
	if !m.settings.open {
		t.Fatal("/settings should open the settings pane")
	}
	if m.focus == focusComposer {
		t.Error("focus should leave the composer when the pane takes the keyboard")
	}
	if m.composer.Value() != "" {
		t.Errorf("composer = %q, want it cleared", m.composer.Value())
	}
	// And the pane must then actually respond to keys.
	m, _ = m.Update(press("down"))
	if m.settings.cursor != 1 {
		t.Errorf("cursor = %d, want the pane to have taken the arrow keys", m.settings.cursor)
	}
}

// ---- delivery ---------------------------------------------------------------

// notifyEvent is a mention arriving from the core.
func notifyEvent() app.Notification {
	return app.Notification{
		RoomID: "r1", RoomLabel: "# general", Author: "Alice",
		Text: "@tester can you look?", Reason: app.NotifyMention,
	}
}

// runCmd executes a tea.Cmd the way the runtime would, discarding its message.
func runCmd(m chatModel, e app.Event) chatModel {
	m, cmd := m.Update(coreEventMsg{event: e})
	if cmd != nil {
		cmd()
	}
	return m
}

// Desktop delivery is switched off in these tests on purpose: whether it
// reaches the terminal at all depends on whether the machine running the test
// has notify-send, and its own fallback is covered in internal/notify. What is
// the UI's business — that the toggles are read, and that a notification
// reaches the notifier at all — is deterministic once it is the only path left.
func TestNotificationRingsTheBell(t *testing.T) {
	m, terminal := newTestChatWithTerminal(t)
	m.cfg.Notifications.SetDesktop(false)

	m = runCmd(m, notifyEvent())
	if terminal.String() != "\a" {
		t.Errorf("terminal got %q, want a single bell", terminal.String())
	}
	_ = m
}

func TestNotificationWithSoundOffIsSilent(t *testing.T) {
	m, terminal := newTestChatWithTerminal(t)
	m.cfg.Notifications.SetDesktop(false)
	m.cfg.Notifications.SetSound(false)

	m = runCmd(m, notifyEvent())
	if terminal.Len() != 0 {
		t.Errorf("everything is off but %q was written", terminal.String())
	}
	_ = m
}

// Turning the toggle off in the pane has to reach delivery, not just the file.
func TestTogglingSoundOffInThePaneSilencesTheNextNotification(t *testing.T) {
	m, terminal := newTestChatWithTerminal(t)
	m.cfg.Notifications.SetDesktop(false)
	m.focus = focusRooms

	m, _ = m.Update(press(","))
	m, _ = m.Update(press("down")) // the sound row
	m, _ = m.Update(press("enter"))
	m, _ = m.Update(press("esc"))

	m = runCmd(m, notifyEvent())
	if terminal.Len() != 0 {
		t.Errorf("sound was turned off in the pane but %q was written", terminal.String())
	}
	_ = m
}

// A DM's room is the sender, so naming both reads "alice — @alice".
func TestNotificationTitle(t *testing.T) {
	cases := []struct {
		name  string
		event app.Notification
		want  string
	}{
		{"a mention names the room", app.Notification{
			Author: "Alice", RoomLabel: "# general", Reason: app.NotifyMention,
		}, "Alice in # general"},
		{"a thread reply names the room", app.Notification{
			Author: "Bob", RoomLabel: "# general", Reason: app.NotifyThread,
		}, "Bob in # general"},
		{"a DM is just the sender", app.Notification{
			Author: "Alice", RoomLabel: "@alice", Reason: app.NotifyDirect,
		}, "Alice"},
		{"an unnamed room falls back to the sender", app.Notification{
			Author: "Alice", Reason: app.NotifyMention,
		}, "Alice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notificationTitle(tc.event); got != tc.want {
				t.Errorf("notificationTitle = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSettingsPaneSnapshot renders the pane and logs it so the layout can be
// eyeballed with -v. The width assertion is the part that matters unattended:
// the detail lines are indented past the widest label, so a longer label than
// anyone expected is how this quietly starts wrapping.
func TestSettingsPaneSnapshot(t *testing.T) {
	m := newTestChat(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 96, Height: 28})
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m.cfg.Notifications.SetSound(false)
	m.cfg.Notifications.SoundCommand = "paplay ~/sounds/ping.wav"
	m.focus = focusRooms
	m, _ = m.Update(press(","))

	view := m.View()
	want := []string{
		"Settings",
		"Desktop notifications", "[✓] on", // on by default
		"Notification sound", "[ ] off", // switched off above
		"paplay ~/sounds/ping.wav",           // the configured command, in place of "terminal bell"
		"↑↓ move · enter toggle · esc close", // the status bar follows the pane
	}
	for _, fragment := range want {
		if !strings.Contains(view, fragment) {
			t.Errorf("settings snapshot missing %q", fragment)
		}
	}
	for i, line := range strings.Split(view, "\n") {
		if render.Width(line) > 96 {
			t.Errorf("line %d is %d cells wide, want <= 96", i, render.Width(line))
		}
	}

	t.Logf("\n%s\n", view)
}

// A narrow terminal is the case the truncation arithmetic gets wrong: the
// detail lines are indented, so their budget is the width minus an indent that
// can exceed it.
func TestSettingsPaneFitsANarrowTerminal(t *testing.T) {
	m := newTestChat(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m.focus = focusRooms
	m, _ = m.Update(press(","))

	for i, line := range strings.Split(m.View(), "\n") {
		if render.Width(line) > 40 {
			t.Errorf("line %d is %d cells wide, want <= 40:\n%s", i, render.Width(line), m.View())
			break
		}
	}
}

// configPath digs out where the model's config lives, which is the only way a
// test can check that a toggle was actually written.
func configPath(t *testing.T, m chatModel) string {
	t.Helper()
	path := m.cfg.Path()
	if path == "" {
		t.Fatal("the test config has no path, so persistence cannot be checked")
	}
	return path
}
