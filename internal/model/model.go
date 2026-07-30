// Package model holds the view-facing domain types. The UI depends on this
// package only; it never sees SDK wire types or SQL rows.
package model

import (
	"sort"
	"strings"
	"time"
)

// Kind is the room flavour the UI groups and labels by. Rocket.Chat encodes all
// of these in one `t` field plus a few booleans; we resolve it once, here.
type Kind int

const (
	KindChannel Kind = iota
	KindPrivate
	KindDirect
	KindTeam
	KindDiscussion
	KindOmnichannel
)

func (k Kind) String() string {
	switch k {
	case KindPrivate:
		return "private"
	case KindDirect:
		return "direct"
	case KindTeam:
		return "team"
	case KindDiscussion:
		return "discussion"
	case KindOmnichannel:
		return "omnichannel"
	default:
		return "channel"
	}
}

// Sigil is the single-character prefix shown before a room name.
func (k Kind) Sigil() string {
	switch k {
	case KindPrivate:
		return "\U0001F512" // lock
	case KindDirect:
		return "@"
	case KindTeam:
		return "&"
	case KindDiscussion:
		return "↳" // downwards arrow with tip rightwards
	default:
		return "#"
	}
}

// Room is one row in the sidebar plus the metadata the header needs.
type Room struct {
	ID           string
	Kind         Kind
	Name         string // slug, empty for some DMs
	DisplayName  string // what to render
	Topic        string
	TeamID       string
	TeamMain     bool
	ParentRoomID string // set for discussions

	Unread        int
	UserMentions  int
	GroupMentions int
	Alert         bool
	Open          bool
	Favorite      bool

	LastMessageAt time.Time
	LastSeenAt    time.Time
	ReadOnly      bool
	Archived      bool
	UserCount     int
}

// Label is the room's name with its sigil.
func (r Room) Label() string {
	name := r.DisplayName
	if name == "" {
		name = r.Name
	}
	if name == "" {
		name = r.ID
	}
	if r.Kind == KindDirect {
		return "@" + name
	}
	return r.Kind.Sigil() + " " + name
}

// Mentions is the count that drives the red badge.
func (r Room) Mentions() int { return r.UserMentions + r.GroupMentions }

// HasUnread reports whether the room should render as bold/unread.
func (r Room) HasUnread() bool { return r.Unread > 0 || r.Alert }

// Reaction is one emoji and who used it.
type Reaction struct {
	Emoji     string
	Usernames []string
}

// Attachment is the terminal-friendly subset of a Rocket.Chat attachment.
type Attachment struct {
	Title string
	Text  string
	Link  string
}

// Message is one entry in the timeline.
type Message struct {
	ID       string
	RoomID   string
	ThreadID string // non-empty when this is a reply inside a thread

	Username string
	Author   string // display name, falls back to username
	Text     string

	At       time.Time
	EditedAt time.Time

	SystemType   string // non-empty for join/leave/topic events
	ThreadCount  int    // >0 when this message is a thread parent
	ThreadLastAt time.Time
	ShowInParent bool

	Reactions   []Reaction
	Attachments []Attachment

	Own bool // authored by the logged-in user
}

// IsSystem reports whether this is a server-generated event message.
func (m Message) IsSystem() bool { return m.SystemType != "" }

// IsThreadReply reports whether this message belongs to a thread.
func (m Message) IsThreadReply() bool { return m.ThreadID != "" }

// IsThreadParent reports whether this message has replies hanging off it.
func (m Message) IsThreadParent() bool { return m.ThreadCount > 0 }

// SystemText renders a human-readable line for system messages, mirroring the
// web client's wording for the types a terminal user actually sees.
func (m Message) SystemText() string {
	who := m.Author
	if who == "" {
		who = m.Username
	}
	switch m.SystemType {
	case "uj":
		return who + " joined the channel"
	case "ujt":
		return who + " joined the team"
	case "ul":
		return who + " left the channel"
	case "ult":
		return who + " left the team"
	case "ru":
		return m.Text + " removed by " + who
	case "au":
		return m.Text + " added by " + who
	case "room_changed_topic":
		return who + " changed the topic to: " + m.Text
	case "room_changed_description":
		return who + " changed the description"
	case "room_changed_announcement":
		return who + " changed the announcement"
	case "room_changed_privacy":
		return who + " changed the room privacy to " + m.Text
	case "room_e2e_enabled":
		return who + " enabled end-to-end encryption"
	case "r":
		return who + " renamed the room to " + m.Text
	case "message_pinned":
		return who + " pinned a message"
	case "discussion-created":
		return who + " started a discussion: " + m.Text
	case "user-muted":
		return m.Text + " muted by " + who
	case "user-unmuted":
		return m.Text + " unmuted by " + who
	case "subscription-role-added":
		return m.Text + " was set " + who
	case "rm":
		return "message removed"
	default:
		if m.Text != "" {
			return who + ": " + m.Text
		}
		return who + " " + m.SystemType
	}
}

// TypingUsers is a room's current typists, excluding the local user.
type TypingUsers []string

// Text renders the typing indicator line the way the web client phrases it.
func (t TypingUsers) Text() string {
	switch len(t) {
	case 0:
		return ""
	case 1:
		return t[0] + " is typing…"
	case 2:
		return t[0] + " and " + t[1] + " are typing…"
	default:
		return strings.Join(t[:len(t)-1], ", ") + " and " + t[len(t)-1] + " are typing…"
	}
}

// SortRooms orders the sidebar by message activity, most recent first, falling
// back to name so that rooms with no known activity keep a stable order.
//
// Unread state deliberately plays no part. Sorting unread rooms to the top
// sounds helpful but means the list rearranges itself as a consequence of
// reading: open a room, its unread clears, and it drops away from where it just
// was. Position should be a property of the conversation, not of what the user
// has just looked at, so unread is carried by weight and badges instead — see
// Room.HasUnread and Room.Mentions.
func SortRooms(rooms []Room) {
	sort.SliceStable(rooms, func(i, j int) bool {
		a, b := rooms[i], rooms[j]
		if !a.LastMessageAt.Equal(b.LastMessageAt) {
			return a.LastMessageAt.After(b.LastMessageAt)
		}
		return strings.ToLower(a.Label()) < strings.ToLower(b.Label())
	})
}
