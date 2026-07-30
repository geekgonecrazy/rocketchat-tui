package app

import (
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// Event is a state change the UI should render. The core pushes these; the UI
// never reads the store or the SDK directly, so there is no shared mutable
// state between the two and no locking in the render path.
type Event interface{ isAppEvent() }

// Totals drives the global unread/mention counters in the header.
type Totals struct {
	UnreadRooms int
	Unread      int
	Mentions    int
}

// RoomsUpdated is a fresh sidebar snapshot.
type RoomsUpdated struct {
	Rooms  []model.Room
	Totals Totals
}

// TimelineUpdated is a fresh message list for one room.
//
// UnreadFrom is frozen when the room is opened, so the "new messages" divider
// stays put while the user reads instead of sliding down as they scroll.
type TimelineUpdated struct {
	RoomID      string
	Room        model.Room
	Messages    []model.Message
	UnreadFrom  time.Time
	UnreadCount int
	HasMore     bool
	LoadingMore bool
}

// ThreadListUpdated is the set of threads in a room.
type ThreadListUpdated struct {
	RoomID  string
	Threads []model.Message
}

// ThreadUpdated is one open thread: its parent plus replies.
type ThreadUpdated struct {
	RoomID   string
	ThreadID string
	Parent   model.Message
	Replies  []model.Message
}

// MembersUpdated is who the composer may offer for an @ mention in a room:
// the room's fetched roster merged with everyone who has spoken there.
type MembersUpdated struct {
	RoomID  string
	Members []model.Member
}

// TypingUpdated is the current set of typists in a room, excluding the local
// user.
type TypingUpdated struct {
	RoomID string
	Users  model.TypingUsers
}

// StatusChanged reports connectivity and background sync activity.
type StatusChanged struct {
	Connection rocket.ConnState
	Syncing    bool
	Err        error
}

// SessionReady means credentials are good and the cache is being served.
type SessionReady struct {
	Username  string
	ServerURL string
}

// SessionInvalid means the stored token was rejected and the user must log in.
type SessionInvalid struct {
	Reason string
}

// Notice is a transient, non-fatal message for the status bar.
type Notice struct {
	Text  string
	IsErr bool
}

// AttachmentFetched is an attachment now sitting in the local cache, ready to
// be drawn, saved, or handed to the desktop. Path is empty when Err is set.
//
// MessageID and Index say which attachment this is: a fetch is asynchronous, so
// the user may have moved the cursor or opened another room before it lands,
// and the UI needs to know whether the result is still the one it asked for.
type AttachmentFetched struct {
	MessageID  string
	Index      int
	Attachment model.Attachment
	Path       string
	MIME       string
	Err        error
}

func (RoomsUpdated) isAppEvent()      {}
func (AttachmentFetched) isAppEvent() {}
func (TimelineUpdated) isAppEvent()   {}
func (ThreadListUpdated) isAppEvent() {}
func (ThreadUpdated) isAppEvent()     {}
func (MembersUpdated) isAppEvent()    {}
func (TypingUpdated) isAppEvent()     {}
func (StatusChanged) isAppEvent()     {}
func (SessionReady) isAppEvent()      {}
func (SessionInvalid) isAppEvent()    {}
func (Notice) isAppEvent()            {}

// computeTotals folds the sidebar into header counters.
func computeTotals(rooms []model.Room) Totals {
	var totals Totals
	for _, room := range rooms {
		if room.HasUnread() {
			totals.UnreadRooms++
		}
		totals.Unread += room.Unread
		totals.Mentions += room.Mentions()
	}
	return totals
}
