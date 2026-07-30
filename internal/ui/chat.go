package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
	"github.com/geekgonecrazy/rocketchat-tui/internal/termimg"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

// focusArea is which pane receives key presses.
type focusArea int

const (
	focusRooms focusArea = iota
	focusMessages
	focusComposer
)

// bodyMode is what the main pane shows.
type bodyMode int

const (
	bodyTimeline bodyMode = iota
	bodyThread
	bodyThreadList
)

// clearNoticeMsg expires a transient status message.
type clearNoticeMsg struct{}

// chatModel is the main screen. It holds view state and input widgets; all
// layout lives in the render package, and all data comes from app events.
type chatModel struct {
	core  *app.Core
	theme render.Theme

	width  int
	height int

	// sidebar
	rooms      []model.Room
	visible    []model.Room
	roomCursor int
	activeRoom string
	filter     string
	filtering  bool

	// timeline
	room        model.Room
	messages    []model.Message
	unreadFrom  time.Time
	unreadCount int
	hasMore     bool
	loadingMore bool
	msgCursor   int
	// cursorMsgID re-anchors the selection by identity, because paging older
	// history shifts every index in the list.
	cursorMsgID string

	// thread
	threadID      string
	threadParent  model.Message
	threadReplies []model.Message
	threadCursor  int

	// thread index
	threads      []model.Message
	threadsIndex int

	mode bodyMode

	// Layout is recomputed when state changes, not on every redraw, so View is a
	// cheap slice of pre-rendered lines.
	body           render.TimelineView
	scroll         int
	pinnedToBottom bool
	jumpToUnread   bool

	typing map[string]model.TypingUsers
	totals app.Totals

	conn      rocket.ConnState
	syncing   bool
	notice    string
	noticeErr bool
	showHelp  bool

	username    string
	serverLabel string

	composer textarea.Model
	focus    focusArea

	// editID is the message the composer is rewriting, empty when composing a
	// new one. editDraft holds what the composer had before, so cancelling an
	// edit gives it back, and editPrevCursorID the selection to put back with it.
	editID           string
	editDraft        string
	editPrevCursorID string

	// picker backs both the composer's inline emoji completer and the modal
	// reaction picker; only one is ever open.
	picker emojiPicker

	// mentions is the composer's inline "@" completer, fed by members.
	mentions mentionPicker
	// members are the mention candidates for the open room, best first.
	members []model.Member

	// attachments
	pending       pendingAttachment
	downloadDir   string
	imageProtocol termimg.Protocol
}

func newChatModel(core *app.Core, theme render.Theme, username, serverLabel, downloadDir string) chatModel {
	composer := textarea.New()
	composer.Placeholder = "Write a message…"
	composer.Prompt = ""
	composer.ShowLineNumbers = false
	composer.CharLimit = 0
	composer.SetHeight(1)
	// Enter sends; a newline needs an explicit modifier.
	composer.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))

	return chatModel{
		core:           core,
		theme:          theme,
		username:       username,
		serverLabel:    serverLabel,
		composer:       composer,
		focus:          focusRooms,
		msgCursor:      -1,
		threadCursor:   -1,
		typing:         make(map[string]model.TypingUsers),
		pinnedToBottom: true,
		conn:           rocket.Connecting,
		downloadDir:    downloadDir,
		// Detected once at startup: the terminal cannot change out from under a
		// running process, and probing on every keypress would be wasted work.
		imageProtocol: termimg.DetectEnv(),
	}
}

func (m chatModel) Init() tea.Cmd { return textarea.Blink }

// ---- layout metrics ---------------------------------------------------------

func (m chatModel) sidebarWidth() int {
	if m.width <= 0 {
		return 0
	}
	return clamp(m.width/4, 18, 34)
}

func (m chatModel) bodyWidth() int {
	return max(8, m.width-m.sidebarWidth()-1)
}

func (m chatModel) composerHeight() int {
	if m.room.ReadOnly {
		return 2 // divider + notice
	}
	lines := 1 + m.composer.Height() // divider + input
	if m.editing() || (m.threadID != "" && m.mode == bodyThread) {
		lines++ // edit banner, or thread context line
	}
	return lines
}

// pickerHeight is how many lines the open completion list occupies, zero when
// none is open. Only one can be open at a time: an "@" token and a ":" token
// cannot both be what is being typed.
func (m chatModel) pickerHeight() int {
	if m.mentions.active() {
		return min(mentionRows, max(1, len(m.mentions.matches)))
	}
	if !m.picker.active() {
		return 0
	}
	rows := min(pickerRows, max(1, len(m.picker.matches)))
	if m.picker.mode == pickerReact {
		rows++ // title line
	}
	return rows
}

// bodyHeight mirrors render.Chat's arithmetic: header (2), typing (1),
// composer, status (1). The picker eats into the body so the composer stays put
// rather than the whole layout shifting when the list appears.
func (m chatModel) bodyHeight() int {
	return max(1, m.height-2-1-1-m.composerHeight()-m.pickerHeight())
}

// ---- update -----------------------------------------------------------------

func (m chatModel) Update(msg tea.Msg) (chatModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.composer.SetWidth(max(8, m.bodyWidth()-2))
		m.rebuildBody()
		return m, nil

	case clearNoticeMsg:
		m.notice, m.noticeErr = "", false
		return m, nil

	case coreEventMsg:
		return m.handleCoreEvent(msg.event)

	case viewerFinishedMsg:
		return m.viewerClosed(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	// Anything else (cursor blink, paste) belongs to the focused widget.
	if m.focus == focusComposer {
		var cmd tea.Cmd
		m.composer, cmd = m.composer.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m chatModel) handleCoreEvent(event app.Event) (chatModel, tea.Cmd) {
	switch e := event.(type) {
	case app.RoomsUpdated:
		m.rooms = e.Rooms
		m.totals = e.Totals
		m.applyFilter()
		// A resync while "#" is open should update the list under the cursor
		// rather than leave it showing rooms that have since gone.
		if m.mentions.active() {
			m.mentions.refresh(m.mentionCandidates)
			m.rebuildBody()
		}
		// Open the first room automatically on a cold start so the user lands in
		// something useful instead of an empty pane.
		if m.activeRoom == "" && len(m.visible) > 0 {
			cmd := m.openRoom(m.visible[0].ID)
			return m, cmd
		}

	case app.TimelineUpdated:
		if e.RoomID != m.activeRoom {
			return m, nil
		}
		m.room = e.Room
		m.messages = e.Messages
		m.unreadFrom = e.UnreadFrom
		m.unreadCount = e.UnreadCount
		m.hasMore = e.HasMore
		m.loadingMore = e.LoadingMore
		m.restoreMessageCursor()
		m.composer.SetWidth(max(8, m.bodyWidth()-2))
		if m.mode == bodyTimeline {
			m.rebuildBody()
			if m.jumpToUnread {
				m.positionAtUnread()
			}
			if m.editTargetGone() {
				m = m.abandonEdit()
				return m.notify("that message is no longer here", false)
			}
		}

	case app.ThreadListUpdated:
		if e.RoomID != m.activeRoom {
			return m, nil
		}
		m.threads = e.Threads
		if m.threadsIndex >= len(m.threads) {
			m.threadsIndex = max(0, len(m.threads)-1)
		}
		if m.mode == bodyThreadList {
			m.rebuildBody()
		}

	case app.ThreadUpdated:
		if e.ThreadID != m.threadID {
			return m, nil
		}
		m.threadParent = e.Parent
		m.threadReplies = e.Replies
		if m.mode == bodyThread {
			m.rebuildBody()
			if m.editTargetGone() {
				m = m.abandonEdit()
				return m.notify("that message is no longer here", false)
			}
		}

	case app.MembersUpdated:
		if e.RoomID != m.activeRoom {
			return m, nil
		}
		m.members = e.Members
		// A roster arriving while the completer is open should widen the list
		// under the cursor, not wait for the next keystroke.
		if m.mentions.active() {
			m.mentions.refresh(m.mentionCandidates)
			m.rebuildBody()
		}

	case app.TypingUpdated:
		if len(e.Users) == 0 {
			delete(m.typing, e.RoomID)
		} else {
			m.typing[e.RoomID] = e.Users
		}

	case app.StatusChanged:
		m.conn = e.Connection
		m.syncing = e.Syncing

	case app.Notice:
		m.notice, m.noticeErr = e.Text, e.IsErr
		return m, tea.Tick(6*time.Second, func(time.Time) tea.Msg { return clearNoticeMsg{} })

	case app.AttachmentFetched:
		return m.attachmentFetched(e)

	case app.SessionReady:
		m.username = e.Username
		m.serverLabel = shortServer(e.ServerURL)
	}
	return m, nil
}

// ---- view -------------------------------------------------------------------

func (m chatModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	sidebarWidth := m.sidebarWidth()
	bodyHeight := m.bodyHeight()

	header := render.Header(m.theme, render.HeaderState{
		Room:        m.room,
		Width:       m.width,
		UnreadRooms: m.totals.UnreadRooms,
		Mentions:    m.totals.Mentions,
		ServerLabel: m.serverLabel,
		Username:    m.username,
		ThreadOpen:  m.mode == bodyThread,
	})

	sidebar := render.Sidebar(m.theme, render.SidebarState{
		Rooms:        m.visible,
		Cursor:       m.roomCursor,
		ActiveRoomID: m.activeRoom,
		Width:        sidebarWidth,
		Height:       bodyHeight,
		Focused:      m.focus == focusRooms,
		Filter:       m.filter,
		Filtering:    m.filtering,
	})

	body := render.Window(m.body.Lines, m.scroll, bodyHeight)
	if m.showHelp {
		body = render.Window(render.HelpOverlay(m.theme, m.bodyWidth()), 0, bodyHeight)
	}

	var picker []string
	if m.mentions.active() {
		picker = render.MentionPicker(m.theme, render.MentionPickerState{
			Query:   m.mentions.query,
			Matches: m.mentions.matches,
			Cursor:  m.mentions.cursor,
			Width:   m.width,
			MaxRows: mentionRows,
		})
	} else if m.picker.active() {
		title := ""
		if m.picker.mode == pickerReact {
			title = "React"
		}
		picker = render.EmojiPicker(m.theme, render.EmojiPickerState{
			Title:   title,
			Query:   m.picker.query,
			Matches: m.picker.matches,
			Cursor:  m.picker.cursor,
			Width:   m.width,
			MaxRows: pickerRows,
		})
	}

	composer := render.Composer(m.theme, render.ComposerState{
		Width:      m.width,
		View:       m.composer.View(),
		Prompt:     m.composerPrompt(),
		ReplyingTo: m.threadContext(),
		Editing:    m.editing(),
		ReadOnly:   m.room.ReadOnly,
	})

	return render.Chat(m.theme, render.Frame{
		Width:        m.width,
		Height:       m.height,
		Header:       header,
		Sidebar:      sidebar,
		Body:         body,
		Picker:       picker,
		Typing:       render.TypingLine(m.theme, m.typing[m.activeRoom], m.width),
		Composer:     composer,
		Status:       m.statusBar(),
		SidebarWidth: sidebarWidth,
	})
}

func (m chatModel) composerPrompt() string {
	if m.editing() {
		// A different prompt, not just a banner: the box now holds text that is
		// already posted, and losing track of that is how you edit by accident.
		return "✎ "
	}
	if m.focus == focusComposer {
		return "› "
	}
	return "  "
}

func (m chatModel) threadContext() string {
	if m.mode != bodyThread || m.threadParent.ID == "" {
		return ""
	}
	preview := strings.ReplaceAll(m.threadParent.Text, "\n", " ")
	if preview == "" {
		preview = m.threadParent.SystemText()
	}
	author := m.threadParent.Author
	if author == "" {
		author = m.threadParent.Username
	}
	return author + ": " + preview
}

// unreadHint tells the user how many new messages there are and how to reach
// them, but only while the divider is scrolled out of view.
func (m chatModel) unreadHint() string {
	if m.mode != bodyTimeline || m.body.UnreadLine < 0 {
		return ""
	}
	if m.body.UnreadLine >= m.scroll && m.body.UnreadLine < m.scroll+m.bodyHeight() {
		return "" // already on screen
	}
	if m.unreadCount <= 0 {
		// The server flagged the room unread without a count, which is its normal
		// behaviour when unread counters are off. Claiming a number would be
		// inventing one.
		return "new messages · u to jump"
	}
	plural := "s"
	if m.unreadCount == 1 {
		plural = ""
	}
	return strconv.Itoa(m.unreadCount) + " new message" + plural + " · u to jump"
}

func (m chatModel) statusBar() string {
	connection := m.conn.String()
	if m.conn == rocket.Connected {
		connection = "connected"
	}

	hints := "enter thread · r react · ctrl+t threads · ? help"
	switch {
	case m.editing():
		hints = "enter save · esc cancel · ↑↓ another message"
	case m.focus == focusComposer:
		hints = "enter send · ↑ edit last · @mention · :emoji · ctrl+t threads · ? help"
	case m.focus == focusRooms:
		hints = "enter open · / filter · ctrl+t threads · ? help"
	}

	// A pending unread jump is more useful than the key hints while it applies.
	notice, noticeErr := m.notice, m.noticeErr
	if notice == "" {
		if hint := m.unreadHint(); hint != "" {
			notice = hint
		}
	}

	return render.StatusBar(m.theme, render.StatusState{
		Width:      m.width,
		Connection: connection,
		Online:     m.conn == rocket.Connected,
		Syncing:    m.syncing,
		Notice:     notice,
		NoticeErr:  noticeErr,
		Hints:      hints,
	})
}

// ---- helpers ----------------------------------------------------------------

func clamp(value, low, high int) int {
	if high < low {
		return low
	}
	return min(max(value, low), high)
}

func shortServer(serverURL string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(serverURL, "https://"), "http://")
	return strings.TrimSuffix(trimmed, "/")
}
