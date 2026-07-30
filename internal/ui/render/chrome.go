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
		lines = append(lines, theme.ThreadHint.Render(
			Truncate("  ↳ replying in thread: "+state.ReplyingTo, state.Width)))
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

// HelpOverlay renders the key reference as a centred block of lines.
func HelpOverlay(theme Theme, width int) []string {
	entries := [][2]string{
		{"tab", "move focus: rooms → messages → composer"},
		{"↑ ↓ / k j", "move within the focused pane"},
		{"enter", "rooms: open · messages: open or start a thread · composer: send"},
		{"↑ (composer)", "edit your last message; again for the one before"},
		{"ctrl+t", "threads in this room — works while typing"},
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
		{"esc", "close thread / clear filter / leave the composer"},
		{"ctrl+r", "resync now"},
		{"ctrl+l", "mark the current room read"},
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
