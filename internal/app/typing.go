package app

import (
	"context"
	"sort"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

const (
	// typingIdleStop is how long after the last keystroke we tell the server the
	// user stopped typing.
	typingIdleStop = 5 * time.Second
	// typingKeepalive re-announces continuous typing so peers whose entry is
	// about to expire keep seeing the indicator.
	typingKeepalive = 8 * time.Second
	// typistTTL expires a remote typist whose stop notification never arrived.
	typistTTL = 15 * time.Second
)

// UserTyping is called on every keystroke in the composer. It throttles the
// outgoing notifications the way the web client does: announce once on the
// first keystroke, re-announce periodically while typing continues, and
// announce a stop once the user goes idle.
func (c *Core) UserTyping(roomID string) {
	c.enqueue(func(c *Core) {
		if roomID == "" {
			return
		}
		now := time.Now()
		c.typingExpiry[roomID] = now.Add(typingIdleStop)

		needsAnnounce := !c.typingActive[roomID] ||
			now.Sub(c.typingRefresh[roomID]) >= typingKeepalive
		if !needsAnnounce {
			return
		}
		c.typingActive[roomID] = true
		c.typingRefresh[roomID] = now
		c.notifyTyping(roomID, true)
	})
}

// StopTyping announces that the user is no longer typing, e.g. after sending or
// clearing the composer.
func (c *Core) StopTyping(roomID string) {
	c.enqueue(func(c *Core) { c.stopTyping(roomID) })
}

func (c *Core) stopTyping(roomID string) {
	if roomID == "" || !c.typingActive[roomID] {
		return
	}
	delete(c.typingActive, roomID)
	delete(c.typingRefresh, roomID)
	delete(c.typingExpiry, roomID)
	c.notifyTyping(roomID, false)
}

// expireOutgoingTyping stops announcing for rooms the user has gone idle in.
func (c *Core) expireOutgoingTyping(now time.Time) {
	for roomID, expiry := range c.typingExpiry {
		if now.After(expiry) {
			c.stopTyping(roomID)
		}
	}
}

// notifyTyping sends the notification off-loop: a websocket write must never
// stall keystroke handling.
func (c *Core) notifyTyping(roomID string, typing bool) {
	if c.realtime == nil || c.selfUsername == "" {
		return
	}
	realtime, username, ctx := c.realtime, c.selfUsername, c.ctx
	go func() {
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := realtime.NotifyTyping(sendCtx, roomID, username, typing); err != nil {
			c.logger.Debug("typing notify failed", "room", roomID, "typing", typing, "err", err)
		}
	}()
}

// ---- incoming typing --------------------------------------------------------

// handleTyping records a remote user's typing state and republishes the room's
// indicator when it changed.
func (c *Core) handleTyping(roomID, username string, typing bool) {
	if roomID == "" || username == "" || username == c.selfUsername {
		return
	}
	room, exists := c.typists[roomID]
	if !exists {
		if !typing {
			return
		}
		room = make(map[string]time.Time)
		c.typists[roomID] = room
	}

	if typing {
		_, wasTyping := room[username]
		room[username] = time.Now().Add(typistTTL)
		if wasTyping {
			return // just a keepalive; nothing visible changed
		}
	} else {
		if _, wasTyping := room[username]; !wasTyping {
			return
		}
		delete(room, username)
		if len(room) == 0 {
			delete(c.typists, roomID)
		}
	}
	c.emitTyping(roomID)
}

// expireTypists drops stale typists and republishes affected rooms.
func (c *Core) expireTypists(now time.Time) {
	for roomID, room := range c.typists {
		changed := false
		for username, expiry := range room {
			if now.After(expiry) {
				delete(room, username)
				changed = true
			}
		}
		if len(room) == 0 {
			delete(c.typists, roomID)
		}
		if changed {
			c.emitTyping(roomID)
		}
	}
}

func (c *Core) emitTyping(roomID string) {
	users := make(model.TypingUsers, 0, len(c.typists[roomID]))
	for username := range c.typists[roomID] {
		users = append(users, username)
	}
	sort.Strings(users)
	c.emit(TypingUpdated{RoomID: roomID, Users: users})
}
