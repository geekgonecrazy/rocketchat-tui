// Package model holds the view-facing domain types. The UI depends on this
// package only; it never sees SDK wire types or SQL rows.
package model

import (
	"net/url"
	pathpkg "path"
	"path/filepath"
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
	ID   string
	Kind Kind
	// Type is the raw Rocket.Chat type letter. Kind is what the UI groups and
	// labels by, but it cannot produce the room's web route: a private team and a
	// private discussion are both "p" on the wire while being KindTeam and
	// KindDiscussion here, and a permalink to the wrong route opens the wrong
	// room. So the letter is kept alongside the kind rather than derived from it.
	Type         string
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

// Mention converts a room into a "#" completion candidate.
//
// The inserted text is the slug rather than the display name, because that is
// what the server resolves into a link — the two often differ, which is the
// main reason completing a channel is worth doing at all. Rooms without a slug
// cannot be mentioned: DMs are addressed by person, and an omnichannel room is
// a conversation with a visitor rather than a place anyone can be pointed at.
func (r Room) Mention() (Mention, bool) {
	if r.Name == "" || r.Kind == KindDirect || r.Kind == KindOmnichannel {
		return Mention{}, false
	}
	return Mention{Sigil: "#", Value: r.Name, Name: r.DisplayName}, true
}

// Mentions is the count that drives the red badge.
func (r Room) Mentions() int { return r.UserMentions + r.GroupMentions }

// HasUnread reports whether the room should render as bold/unread.
func (r Room) HasUnread() bool { return r.Unread > 0 || r.Alert }

// Reaction is one emoji and who used it.
type Reaction struct {
	// Emoji is the shortcode as the server stores it, e.g. ":joy:". Rendering
	// converts it to a glyph; the API needs it in this form.
	Emoji     string
	Usernames []string
	// Mine reports whether the logged-in user is among them, which drives both
	// the highlight and whether toggling adds or removes.
	Mine bool
}

// Count is how many people used this reaction.
func (r Reaction) Count() int { return len(r.Usernames) }

// Attachment is the terminal-friendly subset of a Rocket.Chat attachment.
//
// Title/Text/Link are what the timeline one-liner shows. The rest describes the
// file behind it, so an image can be fetched and drawn: Source is the reference
// to download (usually server-relative, so it needs resolving against the
// server URL), and Upload separates a real uploaded file from a link preview
// the server unfurled — only the former is ours to save or open.
// A quote is the odd one out: it describes no file at all, so Source is empty
// and MessageLink points back at the message being quoted. Title carries who
// said it and Text what they said, which is why the timeline renders it as a
// line of speech rather than as something to open.
type Attachment struct {
	Title string
	Text  string
	Link  string

	Source string
	MIME   string
	Size   int64
	Upload bool

	MessageLink string
}

// IsQuote reports whether this attachment is another message being quoted.
func (a Attachment) IsQuote() bool { return a.MessageLink != "" }

// imageExtensions backs IsImage when the server did not declare a MIME type,
// which happens on older uploads and on unfurled remote images.
var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true,
}

// IsImage reports whether the attachment is something the viewer can draw.
func (a Attachment) IsImage() bool {
	if a.Source == "" {
		return false
	}
	if a.MIME != "" {
		return strings.HasPrefix(a.MIME, "image/")
	}
	path := a.Source
	if cut := strings.IndexAny(path, "?#"); cut >= 0 {
		path = path[:cut]
	}
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

// Filename is the name to save the attachment under. It prefers the title,
// which is what Rocket.Chat sets for an upload, and falls back to the last
// path segment of the source.
func (a Attachment) Filename() string {
	if name := sanitizeFilename(a.Title); name != "" {
		return name
	}
	path := a.Source
	if cut := strings.IndexAny(path, "?#"); cut >= 0 {
		path = path[:cut]
	}
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	if name := sanitizeFilename(pathpkg.Base(path)); name != "" {
		return name
	}
	return "attachment"
}

// sanitizeFilename keeps a server-supplied name from escaping the directory we
// mean to write it into. Titles are arbitrary user input.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	name = strings.Trim(name, ". ")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	if len(name) > 120 {
		name = name[:120]
	}
	return name
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

// Member is someone who can be mentioned in a room.
type Member struct {
	Username string
	Name     string // display name, may be empty
}

// Mention converts a member into a completion candidate.
func (m Member) Mention() Mention {
	return Mention{Sigil: "@", Value: m.Username, Name: m.Name}
}

// Mention is a candidate for one of the composer's inline completers: a person
// for "@", a room for "#".
//
// The two are one type because the completer treats them identically — Value is
// what gets spliced in after the sigil, Name is only context. What differs is
// where the candidates come from, which is the caller's business.
type Mention struct {
	Sigil string
	Value string
	Name  string // display name, may be empty
}

// Label renders a candidate for the list: the mention as it will be inserted
// first, since that is what gets typed, with the display name as context.
func (m Mention) Label() string {
	if m.Name == "" || strings.EqualFold(m.Name, m.Value) {
		return m.Sigil + m.Value
	}
	return m.Sigil + m.Value + "  " + m.Name
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
