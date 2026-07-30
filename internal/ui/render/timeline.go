package render

import (
	"strconv"
	"strings"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/emoji"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// groupWindow is how close in time two messages from the same author must be to
// share one header line.
const groupWindow = 5 * time.Minute

// TimelineState is the message pane's input.
type TimelineState struct {
	Messages []model.Message
	// UnreadFrom is the frozen last-seen marker. Messages after it sit below the
	// "new messages" rule. Zero means no rule.
	UnreadFrom time.Time
	Width      int
	// Cursor is the selected message index, or -1 when nothing is selected.
	Cursor      int
	Focused     bool
	HasMore     bool
	LoadingMore bool
	Empty       string // placeholder when there are no messages
}

// TimelineView is a rendered, scrollable block of lines plus the anchors the
// scroll logic needs. Keeping the anchors here means the model can position the
// viewport without re-deriving layout.
type TimelineView struct {
	Lines []string
	// MessageLine[i] is the first line index of Messages[i].
	MessageLine []int
	// UnreadLine is the line index of the unread rule, or -1.
	UnreadLine int
	// HintLine maps a line index to the message index whose thread affordance is
	// drawn there, so a click on "↳ 3 replies" can open that thread directly.
	HintLine map[int]int
	// ReactionLine maps a line index to the message index whose reactions are
	// drawn there, so a click can toggle one.
	ReactionLine map[int]int
	// ReactionSpans gives, for each reaction line, the cell range each reaction
	// occupies, so a click can tell which one was hit.
	ReactionSpans map[int][]ReactionSpan
}

// ReactionSpan locates one reaction chip within its line.
type ReactionSpan struct {
	// Emoji is the shortcode to toggle.
	Emoji string
	// Start and End are printable cell offsets, End exclusive.
	Start, End int
}

// Timeline renders messages oldest-first into wrapped lines.
func Timeline(theme Theme, state TimelineState) TimelineView {
	view := TimelineView{
		MessageLine:   make([]int, len(state.Messages)),
		UnreadLine:    -1,
		HintLine:      map[int]int{},
		ReactionLine:  map[int]int{},
		ReactionSpans: map[int][]ReactionSpan{},
	}
	if state.Width <= 0 {
		return view
	}

	// One column of gutter marks the selected message.
	contentWidth := max(8, state.Width-2)

	switch {
	case state.LoadingMore:
		view.Lines = append(view.Lines, theme.Faint.Render("  loading older messages…"))
	case state.HasMore && len(state.Messages) > 0:
		view.Lines = append(view.Lines, theme.Faint.Render(
			Rule("press g to load older messages", state.Width, "─")))
	}

	if len(state.Messages) == 0 {
		placeholder := state.Empty
		if placeholder == "" {
			placeholder = "No messages yet."
		}
		view.Lines = append(view.Lines, "", theme.Faint.Render("  "+placeholder))
		return view
	}

	var (
		previous     model.Message
		havePrevious bool
		unreadDrawn  bool
	)

	for index, msg := range state.Messages {
		selected := state.Cursor == index
		gutter := "  "
		if selected {
			gutter = theme.SelectedBar.Render("▌") + " "
		}
		emit := func(content string) {
			view.Lines = append(view.Lines, gutter+content)
		}

		// Date separator whenever the calendar day changes.
		if !havePrevious || !sameDay(previous.At, msg.At) {
			if havePrevious {
				view.Lines = append(view.Lines, "")
			}
			view.Lines = append(view.Lines, theme.DateRule.Render(
				Rule(DayLabel(msg.At), state.Width, "─")))
			havePrevious = false // force a fresh author header after a separator
		}

		// The unread rule goes immediately before the first message the user has
		// not seen. Own messages never trigger it.
		if !unreadDrawn && !state.UnreadFrom.IsZero() &&
			msg.At.After(state.UnreadFrom) && !msg.Own {
			view.UnreadLine = len(view.Lines)
			view.Lines = append(view.Lines, theme.UnreadRule.Render(
				Rule("new messages", state.Width, "─")))
			unreadDrawn = true
			havePrevious = false
		}

		view.MessageLine[index] = len(view.Lines)

		if msg.IsSystem() {
			emit(theme.SystemMsg.Render(Truncate("⋯ "+emoji.Replace(msg.SystemText()), contentWidth)) +
				" " + theme.Time.Render(Clock(msg.At)))
			previous, havePrevious = msg, true
			continue
		}

		// Group consecutive messages from one author into a single header.
		grouped := havePrevious &&
			previous.Username == msg.Username &&
			!previous.IsSystem() &&
			msg.At.Sub(previous.At) < groupWindow
		if !grouped {
			if havePrevious {
				view.Lines = append(view.Lines, "")
				view.MessageLine[index] = len(view.Lines)
			}
			emit(authorLine(theme, msg, contentWidth))
		}

		for _, line := range Wrap(emoji.Replace(msg.Text), contentWidth) {
			emit(theme.Body.Render(line))
		}
		for _, attachment := range msg.Attachments {
			emit(theme.Attachment.Render(Truncate(attachmentSigil(attachment)+" "+attachmentLabel(attachment), contentWidth)))
		}
		if len(msg.Reactions) > 0 {
			rendered, spans := reactionChips(theme, msg.Reactions, contentWidth)
			view.ReactionLine[len(view.Lines)] = index
			view.ReactionSpans[len(view.Lines)] = spans
			emit(rendered)
		}
		if msg.IsThreadParent() {
			view.HintLine[len(view.Lines)] = index
			emit(theme.ThreadHint.Render(Truncate(threadHint(msg), contentWidth)))
		}

		previous, havePrevious = msg, true
	}

	return view
}

func authorLine(theme Theme, msg model.Message, width int) string {
	author := msg.Author
	if author == "" {
		author = msg.Username
	}
	style := theme.Author
	if msg.Own {
		style = theme.OwnAuthor
	}

	stamp := Clock(msg.At)
	if !msg.EditedAt.IsZero() {
		stamp += " (edited)"
	}

	nameWidth := max(1, width-Width(stamp)-1)
	return style.Render(Truncate(author, nameWidth)) + " " + theme.Time.Render(stamp)
}

func threadHint(msg model.Message) string {
	replies := "1 reply"
	if msg.ThreadCount != 1 {
		replies = strconv.Itoa(msg.ThreadCount) + " replies"
	}
	hint := "↳ " + replies
	if when := RelativeTime(model.LatestTime(msg.ThreadLastAt, msg.At)); when != "" {
		hint += " · " + when
	}
	return hint
}

// reactionChips renders reactions as glyph+count chips and reports where each
// one sits, so the UI can map a click back to the reaction it hit.
//
// The gutter is two cells wide, and spans are measured from the start of the
// content, so callers must offset by the gutter when hit-testing.
func reactionChips(theme Theme, reactions []model.Reaction, width int) (string, []ReactionSpan) {
	var (
		b      strings.Builder
		spans  []ReactionSpan
		cursor int
	)
	for i, reaction := range reactions {
		glyph := reaction.Emoji
		if resolved, ok := emoji.Lookup(reaction.Emoji); ok {
			glyph = resolved
		}
		chip := glyph + " " + strconv.Itoa(reaction.Count())

		separator := ""
		if i > 0 {
			separator = "  "
		}
		if cursor+Width(separator)+Width(chip) > width {
			break
		}

		b.WriteString(separator)
		cursor += Width(separator)

		style := theme.Reaction
		if reaction.Mine {
			// Own reactions are highlighted the way the web client does, so it is
			// obvious which ones toggling will remove.
			style = theme.ReactionMine
		}
		b.WriteString(style.Render(chip))

		spans = append(spans, ReactionSpan{
			Emoji: reaction.Emoji,
			Start: cursor,
			End:   cursor + Width(chip),
		})
		cursor += Width(chip)
	}
	return b.String(), spans
}

// attachmentSigil marks an image differently from any other attachment, so it
// is obvious at a glance which lines "v" will actually show you.
func attachmentSigil(attachment model.Attachment) string {
	if attachment.IsImage() {
		return "🖼"
	}
	return "📎"
}

func attachmentLabel(attachment model.Attachment) string {
	switch {
	case attachment.Title != "" && attachment.Text != "":
		return attachment.Title + " — " + strings.ReplaceAll(attachment.Text, "\n", " ")
	case attachment.Title != "":
		return attachment.Title
	case attachment.Text != "":
		return strings.ReplaceAll(attachment.Text, "\n", " ")
	default:
		return attachment.Link
	}
}

// ThreadState is the thread pane's input.
type ThreadState struct {
	Parent  model.Message
	Replies []model.Message
	Width   int
	Cursor  int
	Loading bool
}

// Thread renders a thread as its parent message followed by replies.
func Thread(theme Theme, state ThreadState) TimelineView {
	if state.Width <= 0 {
		return TimelineView{UnreadLine: -1}
	}

	head := Timeline(theme, TimelineState{
		Messages: []model.Message{state.Parent},
		Width:    state.Width,
		Cursor:   -1,
	})

	view := TimelineView{
		Lines: head.Lines, UnreadLine: -1,
		HintLine:      map[int]int{},
		ReactionLine:  map[int]int{},
		ReactionSpans: map[int][]ReactionSpan{},
	}
	label := "thread"
	if count := len(state.Replies); count > 0 {
		label = strconv.Itoa(count) + " replies"
	} else if state.Loading {
		label = "loading replies…"
	}
	view.Lines = append(view.Lines, theme.Divider.Render(Rule(label, state.Width, "─")))

	body := Timeline(theme, TimelineState{
		Messages: state.Replies,
		Width:    state.Width,
		Cursor:   state.Cursor,
		Empty:    "No replies yet — type to reply in this thread.",
	})
	offset := len(view.Lines)
	view.Lines = append(view.Lines, body.Lines...)
	view.MessageLine = make([]int, len(body.MessageLine))
	for i, line := range body.MessageLine {
		view.MessageLine[i] = line + offset
	}
	return view
}

// ThreadListState is the input for the room's thread index.
type ThreadListState struct {
	Threads []model.Message
	Width   int
	Cursor  int
}

// ThreadList renders one line per thread in a room.
func ThreadList(theme Theme, state ThreadListState) []string {
	if state.Width <= 0 {
		return nil
	}
	if len(state.Threads) == 0 {
		return []string{theme.Faint.Render("  No threads in this room.")}
	}

	lines := make([]string, 0, len(state.Threads)*2)
	for index, thread := range state.Threads {
		gutter := "  "
		if index == state.Cursor {
			gutter = theme.SelectedBar.Render("▌") + " "
		}
		author := thread.Author
		if author == "" {
			author = thread.Username
		}
		meta := threadHint(thread)
		width := max(8, state.Width-2)

		headWidth := max(1, width-Width(meta)-1)
		head := theme.Author.Render(Truncate(author, headWidth))
		lines = append(lines, gutter+head+" "+theme.Faint.Render(meta))

		preview := emoji.Replace(strings.ReplaceAll(thread.Text, "\n", " "))
		if preview == "" && thread.IsSystem() {
			preview = emoji.Replace(thread.SystemText())
		}
		lines = append(lines, gutter+theme.Muted.Render(Truncate(preview, width)))
	}
	return lines
}
