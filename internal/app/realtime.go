package app

import (
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// handleRealtime folds one server push into local state. Everything here runs on
// the run loop, so it can mutate state directly.
func (c *Core) handleRealtime(event rocket.Event) {
	switch e := event.(type) {
	case rocket.ConnStateEvent:
		c.handleConnState(e)
	case rocket.MessageEvent:
		c.handleIncomingMessage(e.Message)
	case rocket.MessageDeletedEvent:
		c.handleDeletedMessage(e)
	case rocket.SubscriptionEvent:
		c.handleSubscriptionChange(e)
	case rocket.RoomChangedEvent:
		c.handleRoomChange(e)
	case rocket.TypingEvent:
		c.handleTyping(e.RoomID, e.Username, e.Typing)
	}
}

func (c *Core) handleConnState(event rocket.ConnStateEvent) {
	wasConnected := c.conn == rocket.Connected
	c.conn = event.State
	c.emitStatus(event.Err)

	if event.State != rocket.Connected {
		if wasConnected {
			// Peers' typing indicators cannot be trusted across a gap.
			clear(c.typists)
			c.typingActive = map[string]bool{}
			c.typingRefresh = map[string]time.Time{}
			c.typingExpiry = map[string]time.Time{}
		}
		return
	}

	// (Re)establish the streams we always want.
	c.realtime.SubscribeAllMessages()
	if c.selfID != "" {
		c.realtime.SubscribeUserEvents(c.selfID)
	}
	if c.currentRoom != "" {
		c.realtime.SubscribeRoomMessages(c.currentRoom)
		c.realtime.SubscribeRoomActivity(c.currentRoom)
	}

	// Anything pushed while we were offline has to be pulled.
	c.background(c.syncRooms)
	if c.currentRoom != "" {
		c.catchUpRoom(c.currentRoom)
	}
}

func (c *Core) handleIncomingMessage(msg rocket.Message) {
	if msg.ID == "" || msg.RoomID == "" {
		return
	}
	if err := c.store.SaveMessages([]rocket.Message{msg}); err != nil {
		c.reportError(err)
		return
	}

	// The author is no longer typing once their message lands.
	c.handleTyping(msg.RoomID, msg.User.Username, false)

	if msg.RoomID != c.currentRoom {
		// Unread counts arrive via the subscription stream; refresh ordering so
		// the room floats up the sidebar immediately.
		c.refreshRooms()
		return
	}

	c.emitTimeline(msg.RoomID)
	if msg.ThreadParentID != "" {
		if c.currentThread == msg.ThreadParentID {
			c.emitThread(msg.RoomID, msg.ThreadParentID)
		}
		// A reply may have just turned an ordinary message into a thread parent,
		// which is only visible server-side.
		c.markThreadListDirty(msg.RoomID)
	} else if msg.ThreadCount > 0 {
		c.emitThreadList(msg.RoomID)
	}

	// The user is looking at this room, so keep it read — but only call the
	// server when there is actually something to clear.
	c.markReadIfUnread(msg.RoomID)
}

// markReadIfUnread avoids a REST round trip per incoming message in an already
// read room.
func (c *Core) markReadIfUnread(roomID string) {
	room, found, err := c.store.Room(roomID)
	if err != nil {
		c.logger.Debug("unread check failed", "room", roomID, "err", err)
		return
	}
	if found && room.HasUnread() {
		c.markRead(roomID)
	}
}

func (c *Core) handleDeletedMessage(event rocket.MessageDeletedEvent) {
	if err := c.store.DeleteMessage(event.MessageID); err != nil {
		c.reportError(err)
		return
	}
	if c.currentRoom != "" && (event.RoomID == c.currentRoom || event.RoomID == "") {
		c.emitTimeline(c.currentRoom)
		if c.currentThread != "" {
			c.emitThread(c.currentRoom, c.currentThread)
		}
	}
}

func (c *Core) handleSubscriptionChange(event rocket.SubscriptionEvent) {
	if event.Action == "removed" {
		if err := c.store.DeleteSubscription(event.Subscription.RoomID); err != nil {
			c.reportError(err)
			return
		}
		c.refreshRooms()
		return
	}
	if err := c.store.SaveSubscriptions([]rocket.Subscription{event.Subscription}); err != nil {
		c.reportError(err)
		return
	}
	c.refreshRooms()

	// The header and divider both read from the open room's row. If the divider
	// was still undecided — the room was opened before its subscription synced —
	// this is the moment it can be settled.
	if event.Subscription.RoomID == c.currentRoom {
		c.freezeUnreadMarker(c.currentRoom)
		c.emitTimeline(c.currentRoom)
	}
}

func (c *Core) handleRoomChange(event rocket.RoomChangedEvent) {
	if err := c.store.SaveRooms([]rocket.Room{event.Room}); err != nil {
		c.reportError(err)
		return
	}
	c.refreshRooms()
	if event.Room.ID == c.currentRoom {
		c.emitTimeline(c.currentRoom)
	}
}
