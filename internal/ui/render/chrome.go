package render

import (
	"strconv"
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// HeaderState is the input for the top bar.
type HeaderState struct {
	Room        model.Room
	Width       int
	UnreadRooms int
	Mentions    int
	ServerLabel string
	Username    string
	ThreadOpen  bool
}

// Header renders the title line and the rule beneath it.
func Header(theme Theme, state HeaderState) []string {
	if state.Width <= 0 {
		return nil
	}

	title := "rctui"
	if state.Room.ID != "" {
		title = state.Room.Label()
	}
	if state.ThreadOpen {
		title += "  ›  thread"
	}

	// Right side: attention counters, then who and where we are.
	var right []string
	if state.Mentions > 0 {
		right = append(right, "@"+strconv.Itoa(state.Mentions))
	}
	if state.UnreadRooms > 0 {
		right = append(right, strconv.Itoa(state.UnreadRooms)+" unread")
	}
	if state.Username != "" {
		right = append(right, state.Username)
	}
	if state.ServerLabel != "" {
		right = append(right, state.ServerLabel)
	}
	rightText := strings.Join(right, "  ·  ")

	// The topic fills whatever the title and counters leave behind.
	titleWidth := Width(title)
	rightWidth := Width(rightText)
	topic := ""
	if room := strings.ReplaceAll(state.Room.Topic, "\n", " "); room != "" {
		available := state.Width - titleWidth - rightWidth - 6
		if available > 12 {
			topic = "  " + Truncate(room, available)
		}
	}

	left := theme.HeaderTitle.Render(title) + theme.HeaderMeta.Render(topic)
	gap := max(1, state.Width-titleWidth-Width(topic)-rightWidth)

	mentionStyle := theme.HeaderMeta
	if state.Mentions > 0 {
		mentionStyle = theme.MentionBadge
	}

	return []string{
		left + strings.Repeat(" ", gap) + mentionStyle.Render(rightText),
		theme.Divider.Render(strings.Repeat("─", state.Width)),
	}
}

// TypingLine renders the typing indicator, or a blank line to keep the layout
// from jumping as people start and stop typing.
func TypingLine(theme Theme, users model.TypingUsers, width int) string {
	if width <= 0 {
		return ""
	}
	text := users.Text()
	if text == "" {
		return strings.Repeat(" ", width)
	}
	return theme.Typing.Render(Truncate("  "+text, width))
}

// ComposerState is the input for the message box.
type ComposerState struct {
	Width int
	// View is the pre-rendered input widget; the model owns the widget, this
	// package only frames it.
	View       string
	Prompt     string
	ReplyingTo string
	// AlsoToChannel is the state of the "also send to channel" toggle, shown on
	// the thread banner. It only means anything while ReplyingTo is set.
	AlsoToChannel bool
	// Editing marks the box as rewriting a posted message rather than composing
	// a new one.
	Editing  bool
	ReadOnly bool
	// Attachments are the queued files, already labelled with their sizes. They
	// are shown above the input because they will be sent with whatever is typed
	// there, and a user needs to see what is riding along before pressing enter.
	Attachments []string
	Placeholder string
}

// Composer renders the input box with its prompt and thread context.
func Composer(theme Theme, state ComposerState) []string {
	if state.Width <= 0 {
		return nil
	}
	lines := []string{theme.Divider.Render(strings.Repeat("─", state.Width))}

	if state.ReadOnly {
		lines = append(lines, theme.Faint.Render(Truncate("  This room is read-only.", state.Width)))
		return lines
	}
	// The edit banner replaces the thread hint rather than stacking on it: while
	// editing, which message the box holds matters more than where a new one
	// would go, and the composer must not change height between the two.
	switch {
	case state.Editing:
		lines = append(lines, theme.UnreadRule.Render(
			Truncate("  ✎ editing a sent message — enter saves, esc cancels", state.Width)))
	case state.ReplyingTo != "":
		lines = append(lines, theme.ThreadHint.Render(threadBanner(state)))
	}

	if len(state.Attachments) > 0 {
		lines = append(lines, theme.ThreadHint.Render(attachmentLine(state.Attachments, state.Width)))
	}

	prompt := state.Prompt
	if prompt == "" {
		prompt = "› "
	}
	for i, line := range strings.Split(state.View, "\n") {
		if i == 0 {
			lines = append(lines, theme.Key.Render(prompt)+line)
			continue
		}
		lines = append(lines, strings.Repeat(" ", Width(prompt))+line)
	}
	return lines
}

// threadBanner is the "replying in thread" line, with the state of the mirror
// toggle on its right.
//
// The toggle is what gets the space it needs: a truncated parent preview still
// tells the user which thread they are in, but a truncated checkbox would hide
// that the next message is about to go to the whole room as well.
func threadBanner(state ComposerState) string {
	box := "[ ]"
	if state.AlsoToChannel {
		box = "[✓]"
	}
	toggle := box + " also to channel (alt+c)"

	head := "  ↳ replying in thread: "
	if room := state.Width - Width(head) - Width(" · ") - Width(toggle); room >= 8 {
		return head + Truncate(state.ReplyingTo, room) + " · " + toggle
	}
	// Too narrow for both. A few cells of parent text would say little that the
	// thread pane above does not already show; the checkbox has no such backup.
	return Truncate("  ↳ thread · "+toggle, state.Width)
}

// attachmentLine renders the queued files on one line.
//
// When the names do not fit it collapses to a count rather than truncating the
// list, because a half-shown queue reads as the whole queue: the point of the
// line is to say exactly what enter is about to send.
func attachmentLine(labels []string, width int) string {
	full := "  📎 " + strings.Join(labels, " · ")
	if Width(full) <= width {
		return Pad(full, width)
	}
	summary := "  📎 " + itoa(len(labels)) + " files attached"
	return Pad(Truncate(summary, width), width)
}

// StatusState is the input for the bottom bar.
type StatusState struct {
	Width      int
	Connection string
	Online     bool
	Syncing    bool
	Notice     string
	NoticeErr  bool
	Hints      string
}

// StatusBar renders connection state on the left and key hints on the right.
func StatusBar(theme Theme, state StatusState) string {
	if state.Width <= 0 {
		return ""
	}

	dot, dotStyle := "●", theme.StatusOK
	if !state.Online {
		dotStyle = theme.StatusErr
	}
	left := dotStyle.Render(dot) + theme.Status.Render(" "+state.Connection)
	if state.Syncing {
		left += theme.Status.Render(" · syncing")
	}
	if state.Notice != "" {
		noticeStyle := theme.Status
		if state.NoticeErr {
			noticeStyle = theme.StatusErr
		}
		left += noticeStyle.Render(" · " + state.Notice)
	}

	leftWidth := Width(left)
	right := theme.Faint.Render(state.Hints)
	rightWidth := Width(right)
	if leftWidth+rightWidth+2 > state.Width {
		return Truncate(left, state.Width)
	}
	return left + strings.Repeat(" ", state.Width-leftWidth-rightWidth) + right
}

// SettingRow is one preference in the settings overlay.
type SettingRow struct {
	Label    string
	Detail   string
	On       bool
	Selected bool
}

// SettingsOverlay renders the preferences pane. It occupies the body the same
// way the help overlay does, but every row is something to act on, so the
// selected one is marked and the footer says how.
func SettingsOverlay(theme Theme, width int, rows []SettingRow) []string {
	labelWidth := 0
	for _, row := range rows {
		labelWidth = max(labelWidth, Width(row.Label))
	}

	// Where the detail lines start, under the switches rather than under the
	// labels. On a narrow terminal that column leaves nothing to say anything in,
	// so the detail gives up its alignment before it gives up its content.
	detailIndent := 6 + labelWidth
	if detailIndent > width/2 {
		detailIndent = 4
	}

	lines := []string{theme.Title.Render("  " + Truncate("Settings", max(1, width-4))), ""}
	for _, row := range rows {
		// A checkbox rather than the word "on": the pane is a list of switches,
		// and a column of glyphs is read at a glance where a column of words is
		// read one at a time.
		state, stateStyle := "[ ] off", theme.Muted
		if row.On {
			state, stateStyle = "[✓] on", theme.Key
		}

		// The switch is the row's whole point, so the label yields to it: two
		// spaces of margin, the cursor marker, the gap, and the switch itself all
		// come off the width before the label gets what is left.
		budget := max(1, width-Width(state)-6)
		label := Truncate(Pad(row.Label, min(labelWidth, budget)), budget)

		marker, labelStyle := "  ", theme.Muted
		if row.Selected {
			marker, labelStyle = theme.Key.Render("› "), theme.Key
		}
		lines = append(lines, "  "+marker+labelStyle.Render(label)+"  "+stateStyle.Render(state))

		if row.Detail != "" {
			lines = append(lines, Pad("", detailIndent)+
				theme.Faint.Render(Truncate(row.Detail, max(1, width-detailIndent-2))))
		}
	}

	lines = append(lines,
		"",
		"  "+theme.Muted.Render(Truncate(
			"↑↓ move · enter or space toggles · esc closes. Changes are saved as", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"you make them.", max(1, width-4))),
		"",
		theme.Title.Render("  "+Truncate("When you are notified", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"A direct message, an @mention of you or of @all / @here, and a reply", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"in a thread you follow. Ordinary channel traffic never notifies, and", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"neither does your own message coming back from the server.", max(1, width-4))),
		"",
		theme.Title.Render("  Sound"),
		"  "+theme.Muted.Render(Truncate(
			"The terminal bell by default, so whatever you have told your terminal", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"to do about alerts is what happens. Set \"sound_command\" in the config", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"file to run something else instead — e.g. paplay ~/sounds/ping.wav.", max(1, width-4))),
	)
	return lines
}

// HelpOverlay renders the key reference as a centred block of lines.
func HelpOverlay(theme Theme, width int) []string {
	entries := [][2]string{
		{"tab", "move focus: rooms → messages → composer"},
		{"↑ ↓ / k j", "move within the focused pane"},
		{"enter", "rooms: open · messages: open or start a thread · composer: send"},
		{"↑ (composer)", "edit your last message; again for the one before"},
		{"ctrl+t", "threads in this room — works while typing"},
		{"alt+c", "in a thread: also send your reply to the channel"},
		{"r", "react to the selected message"},
		{"alt+enter", "newline in the composer"},
		{"click", "a room, a message, a ↳ replies line, or a reaction to toggle it"},
		{"wheel", "scroll the pane under the pointer"},
		{"/", "filter the room list"},
		{"g", "load older messages"},
		{"u", "jump to the unread line"},
		{"U", "mark unread: the room, or from the selected message"},
		{"v", "view the selected message's image full-screen"},
		{"s", "save its attachment to your downloads"},
		{"o", "open its attachment in a desktop app"},
		{"ctrl+o", "attach a file to send with your message"},
		{"ctrl+x", "remove the last file you attached"},
		{"ctrl+g", "write your message in $EDITOR"},
		{"esc", "close thread / clear filter / leave the composer"},
		{"ctrl+r", "resync now"},
		{"ctrl+l", "mark the current room read"},
		{",", "settings: desktop notifications and sound"},
		{"?", "toggle this help"},
		{"ctrl+c", "quit"},
	}

	keyWidth := 0
	for _, entry := range entries {
		keyWidth = max(keyWidth, Width(entry[0]))
	}

	lines := []string{theme.Title.Render("  Keys"), ""}
	for _, entry := range entries {
		lines = append(lines, "  "+theme.Key.Render(Pad(entry[0], keyWidth))+
			"  "+theme.Muted.Render(Truncate(entry[1], max(1, width-keyWidth-6))))
	}

	lines = append(lines,
		"",
		theme.Title.Render("  Mentions"),
		"  "+theme.Muted.Render(Truncate(
			"Type @ in the composer and the people in this room appear; keep", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"typing to narrow them. tab or enter inserts the username, esc", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"dismisses. @all and @here notify the room.", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"Type # instead and the rooms you are in appear, so you can link one", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"without remembering its slug.", max(1, width-4))),
		"",
		theme.Title.Render("  Emoji"),
		"  "+theme.Muted.Render(Truncate(
			"Type a colon and a few letters — :jo — and a list appears. tab or", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"enter inserts it, esc dismisses. Shortcodes in messages render as", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"emoji automatically. Press r on a message to react to it.", max(1, width-4))),
	)

	// The commonest question this answers is how to start a thread, which is not
	// obvious from a key list alone.
	lines = append(lines,
		"",
		theme.Title.Render("  Threads"),
		"  "+theme.Muted.Render(Truncate(
			"Any message can start one: select it (tab to the messages pane, or", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"click it) and press enter, then type your reply.", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"ctrl+t lists every thread in the room. esc returns to the timeline.", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"alt+c ticks \"also to channel\" on the composer: what you send then", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"shows in the room as well, marked ↱ with the thread it came from.", max(1, width-4))),
	)

	lines = append(lines,
		"",
		theme.Title.Render("  Attachments"),
		"  "+theme.Muted.Render(Truncate(
			"ctrl+o turns the composer into a file prompt: type a path, tab", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"completes it, ~ means your home directory, enter attaches. Typing", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"/upload <path> does the same without the prompt.", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"Attach as many as you like — they sit above the composer and", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"nothing is uploaded until you send the message. ctrl+x removes the", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"last one; leaving the room drops them all.", max(1, width-4))),
	)

	// Quit-to-cancel and empty-clears cannot be read off the key list, and an
	// undocumented recovery file insures nobody.
	lines = append(lines,
		"",
		theme.Title.Render("  Composing in an editor"),
		"  "+theme.Muted.Render(Truncate(
			"ctrl+g opens your draft in $EDITOR (or $VISUAL, or \"editor\" in the", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"config file). Save and quit, and what you wrote replaces the composer;", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"quit without saving, or exit non-zero with :cq, and the composer is", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"left alone. Saving an empty file clears it.", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"The draft lives at $XDG_DATA_HOME/rctui/compose.md and is never", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"deleted, so a message is recoverable if anything goes wrong.", max(1, width-4))),
	)

	lines = append(lines,
		"",
		theme.Title.Render("  Slash commands"),
		"  "+theme.Muted.Render(Truncate(
			"Type / in an empty composer and every command this server offers", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"appears — its own, plus whatever apps are installed. tab completes;", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"enter runs a command you have typed out in full. After the first", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"space, @ and # complete arguments as usual.", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"/leave, /invite, /topic and the rest work even where the server does", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"not offer them. /exit leaves rctui, /open jumps to a room.", max(1, width-4))),
	)

	lines = append(lines,
		"",
		theme.Title.Render("  Editing"),
		"  "+theme.Muted.Render(Truncate(
			"With the composer empty, ↑ loads your last message into it; press ↑", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"again to go further back and ↓ to come forward. enter saves the edit,", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"esc leaves it alone. Inside a thread you walk that thread's replies,", max(1, width-4))),
		"  "+theme.Muted.Render(Truncate(
			"never the channel's messages.", max(1, width-4))),
	)
	return lines
}
