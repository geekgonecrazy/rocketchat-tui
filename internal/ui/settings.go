package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/notify"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

// The settings pane opens over the body like the help overlay, but unlike help
// it is not dismissed by "any key": it is something you operate, so it keeps the
// arrows, enter and space for itself and closes on esc.

// setting identifies one row in the pane.
type setting int

const (
	settingDesktop setting = iota
	settingSound
)

// settingsOrder is the order rows appear in, and the order the cursor walks.
var settingsOrder = []setting{settingDesktop, settingSound}

// settingsPane is the pane's own state. Everything it displays is read from
// config, so there is no copy here to keep in sync.
type settingsPane struct {
	open   bool
	cursor int
}

func (p *settingsPane) toggleOpen() {
	p.open = !p.open
	p.cursor = 0
}

// settingsRows renders the current preferences into display rows.
func (m chatModel) settingsRows() []render.SettingRow {
	notifications := m.cfg.Notifications

	sound := "terminal bell"
	if notifications.SoundCommand != "" {
		sound = notifications.SoundCommand
	}

	rows := make([]render.SettingRow, 0, len(settingsOrder))
	for i, id := range settingsOrder {
		row := render.SettingRow{Selected: i == m.settings.cursor}
		switch id {
		case settingDesktop:
			row.Label = "Desktop notifications"
			row.On = notifications.DesktopEnabled()
			row.Detail = "when someone DMs you, mentions you, or replies in a thread you follow"
		case settingSound:
			row.Label = "Notification sound"
			row.On = notifications.SoundEnabled()
			row.Detail = sound
		}
		rows = append(rows, row)
	}
	return rows
}

// handleSettingsKey drives the pane while it is open.
func (m chatModel) handleSettingsKey(pressed string) (chatModel, tea.Cmd) {
	switch pressed {
	case "esc", ",", "q":
		m.settings.open = false
		return m, nil

	case "up", "k":
		if m.settings.cursor > 0 {
			m.settings.cursor--
		}
		return m, nil

	case "down", "j":
		if m.settings.cursor < len(settingsOrder)-1 {
			m.settings.cursor++
		}
		return m, nil

	case "enter", " ", "space", "x":
		return m.toggleSetting(settingsOrder[m.settings.cursor])
	}
	// Anything else is ignored rather than treated as "close". A stray keystroke
	// while reading the pane should not put the user back where they started
	// with no idea what they changed.
	return m, nil
}

// toggleSetting flips one preference and writes it out.
//
// Saving here rather than on close is deliberate: there is no confirm step and
// no cancel, so the file is the only record of the decision, and a client that
// is killed rather than quit is the normal way a TUI ends.
func (m chatModel) toggleSetting(id setting) (chatModel, tea.Cmd) {
	var label string
	on := false
	switch id {
	case settingDesktop:
		on = !m.cfg.Notifications.DesktopEnabled()
		m.cfg.Notifications.SetDesktop(on)
		label = "desktop notifications"
	case settingSound:
		on = !m.cfg.Notifications.SoundEnabled()
		m.cfg.Notifications.SetSound(on)
		label = "notification sound"
	}

	if err := m.cfg.Save(); err != nil {
		return m.notify("could not save settings: "+err.Error(), true)
	}
	state := "off"
	if on {
		state = "on"
	}
	return m.notify(label+" "+state, false)
}

// ---- delivery ---------------------------------------------------------------

// deliverNotification hands a core notification to the notifier, off the render
// loop: a desktop helper is a subprocess, and waiting on one inside Update would
// freeze the interface for as long as it took.
func (m chatModel) deliverNotification(event app.Notification) tea.Cmd {
	prefs := notify.Prefs{
		Desktop:      m.cfg.Notifications.DesktopEnabled(),
		Sound:        m.cfg.Notifications.SoundEnabled(),
		SoundCommand: m.cfg.Notifications.SoundCommand,
	}
	if !prefs.Desktop && !prefs.Sound {
		return nil
	}

	note := notify.Notification{
		Title: notificationTitle(event),
		// A notification is a summons, not a reader. Long text is truncated so the
		// thing that matters — who, and where — is not pushed off the popup.
		Body: notify.Truncate(event.Text, 200),
	}
	notifier := m.notifier
	return func() tea.Msg {
		notifier.Deliver(prefs, note)
		return nil
	}
}

// notificationTitle names the sender and, where it adds anything, the room.
//
// A DM's room *is* the sender, so repeating it would read "jane — @jane"; a
// mention or thread reply needs the room, because who said it is not enough to
// tell you where to go.
func notificationTitle(event app.Notification) string {
	if event.Reason == app.NotifyDirect || event.RoomLabel == "" {
		return event.Author
	}
	return event.Author + " in " + event.RoomLabel
}
