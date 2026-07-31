package app

import (
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// This file decides which incoming messages are worth interrupting the user
// for. It decides nothing about how — whether a sound plays, whether a desktop
// notification is raised, whether the user has turned either off — because the
// core does not know the user's preferences and should not. It publishes the
// judgement; the UI, which owns config, acts on it.

// notifiedRing bounds how many message ids are remembered for de-duplication.
// The server re-pushes a message whenever it changes — an edit, a reaction, a
// reply arriving on a parent — and each of those would otherwise notify again
// for text the user has already been told about. A few hundred is far more than
// a burst can contain and costs nothing to hold.
const notifiedRing = 512

// NotifyReason is why a message was judged worth a notification. It is carried
// through to the UI so a notification can say what it is, which is most of what
// makes one worth reading.
type NotifyReason int

const (
	// NotifyDirect is a direct message. The whole room is addressed to you.
	NotifyDirect NotifyReason = iota
	// NotifyMention is @you, or @all / @here, in a room.
	NotifyMention
	// NotifyThread is a reply in a thread you follow.
	NotifyThread
)

// Notification is an incoming message the user should hear about even if they
// are not looking at the terminal.
//
// It is emitted for the open room as well as the others. The room being on
// screen is not evidence anyone is watching it, and for the three things that
// get this far — a DM, a mention, a followed thread — being told twice is a far
// smaller cost than being told never.
type Notification struct {
	RoomID string
	// RoomLabel is the room as the sidebar names it, already carrying its sigil.
	RoomLabel string
	// Author is the sender's display name, falling back to their username.
	Author string
	Text   string
	Reason NotifyReason
}

func (Notification) isAppEvent() {}

// maybeNotify judges one incoming message and publishes a Notification if it
// deserves one.
func (c *Core) maybeNotify(msg rocket.Message) {
	reason, worth := c.notifyReason(msg)
	if !worth {
		return
	}
	if c.alreadyNotified(msg.ID) {
		return
	}

	room, _ := c.roomView(msg.RoomID)
	c.emit(Notification{
		RoomID:    msg.RoomID,
		RoomLabel: room.Label(),
		Author:    firstNonEmpty(msg.User.Name, msg.User.Username),
		Text:      notificationText(msg),
		Reason:    reason,
	})
}

// notifyReason reports whether a message is addressed to this user, and how.
func (c *Core) notifyReason(msg rocket.Message) (NotifyReason, bool) {
	if msg.ID == "" || msg.RoomID == "" {
		return 0, false
	}
	// Your own message, arriving back from the server as confirmation that it
	// was accepted, is not news.
	if c.isSelf(msg.User.ID, msg.User.Username) {
		return 0, false
	}
	// Join/leave/topic-change traffic is room bookkeeping, not something anyone
	// said to anyone.
	if msg.IsSystem() {
		return 0, false
	}
	// An edit re-pushes the whole message. The user has already been told about
	// the text; being told again because someone fixed a typo in it is noise.
	// (A message edited to *add* a mention will not notify — the alternative is
	// notifying afresh on every edit, which is worse far more often.)
	if msg.EditedAt != nil {
		return 0, false
	}

	if room, known := c.roomView(msg.RoomID); known && room.Kind == model.KindDirect {
		return NotifyDirect, true
	}
	if c.mentionsSelf(msg) {
		return NotifyMention, true
	}
	if msg.ThreadParentID != "" && c.followsThread(msg.ThreadParentID) {
		return NotifyThread, true
	}
	return 0, false
}

// mentionsSelf reports whether the message names this user, either directly or
// through @all / @here — which the server delivers as mentions of pseudo-users
// by those names, and which every other client treats as addressed to you.
func (c *Core) mentionsSelf(msg rocket.Message) bool {
	for _, mention := range msg.Mentions {
		if c.isSelf(mention.ID, mention.Username) {
			return true
		}
		switch strings.ToLower(mention.Username) {
		case "all", "here":
			return true
		}
	}
	return false
}

// followsThread reports whether this user follows the thread hanging off
// parentID, according to the last version of the parent we cached.
//
// A reply carries no follower list of its own, and the updated parent that does
// may not have arrived yet — the server pushes the two separately and in no
// guaranteed order. So this can be wrong in one direction only: a thread just
// followed may not notify for its next reply. It cannot invent a follow that
// does not exist, which is the direction that would matter.
func (c *Core) followsThread(parentID string) bool {
	if c.selfID == "" {
		return false
	}
	follows, err := c.store.FollowsThread(parentID, c.selfID)
	if err != nil {
		c.logger.Debug("thread follow check failed", "parent", parentID, "err", err)
		return false
	}
	return follows
}

// isSelf matches a user by id, falling back to username for the payloads that
// carry one but not the other — @all-style mentions and some older servers.
func (c *Core) isSelf(userID, username string) bool {
	if userID != "" && c.selfID != "" && userID == c.selfID {
		return true
	}
	return username != "" && c.selfUsername != "" && strings.EqualFold(username, c.selfUsername)
}

// alreadyNotified reports whether this message has been notified for, recording
// it if not. See notifiedRing for why this is needed at all.
func (c *Core) alreadyNotified(messageID string) bool {
	if c.notified[messageID] {
		return true
	}
	if len(c.notifiedOrder) >= notifiedRing {
		delete(c.notified, c.notifiedOrder[0])
		c.notifiedOrder = c.notifiedOrder[1:]
	}
	c.notified[messageID] = true
	c.notifiedOrder = append(c.notifiedOrder, messageID)
	return false
}

// notificationText is what to show as the body: the message, or a description of
// what came instead when there is no text to show.
func notificationText(msg rocket.Message) string {
	if text := strings.TrimSpace(msg.Msg); text != "" {
		return text
	}
	switch len(msg.Attachments) {
	case 0:
		return ""
	case 1:
		if title := strings.TrimSpace(msg.Attachments[0].Title); title != "" {
			return "sent " + title
		}
		return "sent a file"
	default:
		return "sent files"
	}
}
