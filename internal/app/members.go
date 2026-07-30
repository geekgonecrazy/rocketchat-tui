package app

import (
	"context"
	"time"
)

const (
	// memberPageSize bounds how many members are fetched per room. A busy channel
	// has more people in it than anyone will ever mention from a candidate list,
	// and the server caps the page anyway.
	memberPageSize = 100
	// memberTTL is how long a fetched member list is trusted before another open
	// of the same room refetches it. Membership changes slowly; the completer
	// also draws on message authors, so a slightly stale roster is harmless.
	memberTTL = 10 * time.Minute
)

// emitMembers publishes the mention candidates a room currently has cached.
//
// It is published whenever either half of the list can have changed — the
// fetched roster, and the people who have spoken. Those two arrive over
// separate requests that race, and only re-emitting on both keeps the completer
// from settling on whichever landed first: a room whose history arrived after
// its roster would otherwise offer nobody who had only spoken.
func (c *Core) emitMembers(roomID string) {
	members, err := c.store.MentionCandidates(roomID)
	if err != nil {
		c.reportError(err)
		return
	}
	c.emit(MembersUpdated{RoomID: roomID, Members: members})
}

// syncMembers refreshes a room's roster from the server unless it was fetched
// recently.
//
// Failures here are logged rather than surfaced: the composer still has the
// people who have spoken in the room to offer, and a server that will not list
// members of a channel (a permission a normal user may lack) should not put an
// error in the status bar every time a room is opened.
func (c *Core) syncMembers(roomID string) {
	if at, ok := c.membersSynced[roomID]; ok && time.Since(at) < memberTTL {
		return
	}
	c.membersSynced[roomID] = time.Now()

	c.background(func(ctx context.Context) error {
		roomType, err := c.resolveRoomType(ctx, roomID)
		if err != nil {
			return err
		}
		members, err := c.client.RoomMembers(ctx, roomID, roomType, memberPageSize)
		if err != nil {
			c.logger.Debug("member list unavailable", "room", roomID, "err", err)
			c.enqueue(func(c *Core) { delete(c.membersSynced, roomID) })
			return nil
		}
		if err := c.store.SaveRoomMembers(roomID, members); err != nil {
			return err
		}
		c.enqueue(func(c *Core) {
			if c.currentRoom == roomID {
				c.emitMembers(roomID)
			}
		})
		return nil
	})
}
