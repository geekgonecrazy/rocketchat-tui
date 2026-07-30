package app

import (
	"context"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// OpenRoom switches the active room: it renders from cache at once, then
// catches up over the network.
func (c *Core) OpenRoom(roomID string) {
	c.enqueue(func(c *Core) {
		if roomID == "" || roomID == c.currentRoom {
			return
		}
		previous := c.currentRoom

		// Stop typing in the room we are leaving, or the old room shows us as
		// typing indefinitely.
		if previous != "" {
			c.stopTyping(previous)
			if c.realtime != nil {
				c.realtime.UnsubscribeRoomActivity(previous)
				c.realtime.UnsubscribeRoomMessages(previous)
			}
		}

		c.currentRoom = roomID
		c.currentThread = ""
		c.timelineLimit[roomID] = timelinePageSize
		delete(c.unreadMarker, roomID)
		delete(c.unreadAtOpen, roomID)

		// Freeze the unread divider at the position the room had on open. A room
		// with nothing unread gets no divider at all.
		c.freezeUnreadMarker(roomID)

		if c.realtime != nil {
			c.realtime.SubscribeRoomMessages(roomID)
			c.realtime.SubscribeRoomActivity(roomID)
		}

		c.emitTimeline(roomID)
		c.emitTyping(roomID)
		c.emitMembers(roomID)
		c.catchUpRoom(roomID)
		c.syncMembers(roomID)
		c.markRead(roomID)
		c.loadThreadList(roomID)
	})
}

// freezeUnreadMarker decides, once per visit, where this room's unread divider
// sits. It is called on open and again if the room's subscription arrives later,
// because opening a room whose subscription has not synced yet must not lock in
// "nothing unread" — that would lose the divider for the whole visit.
//
// Three server states have to be handled: an alert with a last-seen marker gives
// a divider, an alert without one gives none, and the count is whatever the
// server chose to tell us (often nothing at all).
func (c *Core) freezeUnreadMarker(roomID string) {
	if _, decided := c.unreadMarker[roomID]; decided {
		return
	}
	room, known := c.roomView(roomID)
	if !known {
		return // undecided; try again when the subscription lands
	}
	if room.HasUnread() && !room.LastSeenAt.IsZero() {
		c.unreadMarker[roomID] = room.LastSeenAt
		c.unreadAtOpen[roomID] = room.Unread
		return
	}
	c.unreadMarker[roomID] = time.Time{}
	c.unreadAtOpen[roomID] = 0
}

// timelineWindow is how many messages the room currently displays.
func (c *Core) timelineWindow(roomID string) int {
	if limit := c.timelineLimit[roomID]; limit > 0 {
		return limit
	}
	return timelinePageSize
}

// emitTimeline publishes the room's cached messages plus divider position.
func (c *Core) emitTimeline(roomID string) {
	limit := c.timelineWindow(roomID)
	messages, err := c.store.RoomTimeline(roomID, limit)
	if err != nil {
		c.reportError(err)
		return
	}
	marker := c.unreadMarker[roomID]

	// The count comes from the server's subscription, captured when the room was
	// opened. Counting messages after the marker locally looks equivalent but is
	// not: it only ever counts what the current page happens to hold, so it grows
	// as the user pages backwards and reports a page size dressed up as a fact.
	// A last-seen marker years in the past made that obvious on a real server —
	// "60 new messages" was simply the page size.
	unreadCount := c.unreadAtOpen[roomID]

	// Older history may exist unless a fetch told us we hit the beginning.
	hasMore, known := c.hasMore[roomID]
	if !known {
		hasMore = len(messages) >= limit
	}

	c.emit(TimelineUpdated{
		RoomID:      roomID,
		Room:        c.currentRoomView(roomID),
		Messages:    messages,
		UnreadFrom:  marker,
		UnreadCount: unreadCount,
		HasMore:     hasMore,
		LoadingMore: c.loadingMore[roomID],
	})
}

// catchUpRoom fetches whatever we are missing for a room: a first page when the
// cache is empty, or a delta sync when it is warm.
func (c *Core) catchUpRoom(roomID string) {
	c.background(func(ctx context.Context) error {
		roomType, err := c.resolveRoomType(ctx, roomID)
		if err != nil {
			return err
		}
		newest, err := c.store.NewestTimestamp(roomID)
		if err != nil {
			return err
		}

		if newest.IsZero() {
			// Cold room: pull the most recent page.
			messages, err := c.client.History(ctx, rocket.HistoryQuery{
				RoomID:   roomID,
				RoomType: roomType,
				Count:    timelinePageSize,
			})
			if err != nil {
				return err
			}
			if err := c.store.SaveMessages(messages); err != nil {
				return err
			}
			reachedStart := len(messages) < timelinePageSize
			if err := c.saveHistoryBounds(roomID, messages, reachedStart); err != nil {
				return err
			}
			c.enqueue(func(c *Core) {
				c.hasMore[roomID] = !reachedStart
				if c.currentRoom == roomID {
					c.emitTimeline(roomID)
				}
			})
			return nil
		}

		// Warm room: ask only for what changed since our newest message.
		updated, deleted, err := c.client.SyncMessages(ctx, roomID, newest)
		if err != nil {
			return err
		}
		if err := c.store.SaveMessages(updated); err != nil {
			return err
		}
		for _, msg := range deleted {
			if err := c.store.DeleteMessage(msg.ID); err != nil {
				return err
			}
		}
		if len(updated) == 0 && len(deleted) == 0 {
			return nil
		}
		c.enqueue(func(c *Core) {
			if c.currentRoom == roomID {
				c.emitTimeline(roomID)
			}
			c.refreshRooms()
		})
		return nil
	})
}

// LoadOlder pages history backwards for the open room.
func (c *Core) LoadOlder(roomID string) {
	c.enqueue(func(c *Core) {
		if roomID == "" || c.loadingMore[roomID] {
			return
		}
		if hasMore, known := c.hasMore[roomID]; known && !hasMore {
			return
		}
		c.loadingMore[roomID] = true
		c.emitTimeline(roomID)

		c.background(func(ctx context.Context) error {
			defer c.enqueue(func(c *Core) {
				c.loadingMore[roomID] = false
				if c.currentRoom == roomID {
					c.emitTimeline(roomID)
				}
			})

			roomType, err := c.resolveRoomType(ctx, roomID)
			if err != nil {
				return err
			}
			oldest, err := c.store.OldestTimestamp(roomID)
			if err != nil {
				return err
			}
			messages, err := c.client.History(ctx, rocket.HistoryQuery{
				RoomID:   roomID,
				RoomType: roomType,
				Count:    timelinePageSize,
				Before:   oldest,
			})
			if err != nil {
				return err
			}
			if err := c.store.SaveMessages(messages); err != nil {
				return err
			}
			reachedStart := len(messages) < timelinePageSize
			if err := c.saveHistoryBounds(roomID, messages, reachedStart); err != nil {
				return err
			}
			c.enqueue(func(c *Core) {
				c.hasMore[roomID] = !reachedStart
				// Widen the window so the page we just fetched is actually shown.
				c.timelineLimit[roomID] = c.timelineWindow(roomID) + len(messages)
			})
			return nil
		})
	})
}

// saveHistoryBounds records how far back the cache now reaches.
func (c *Core) saveHistoryBounds(roomID string, messages []rocket.Message, reachedEnd bool) error {
	state, err := c.store.HistoryState(roomID)
	if err != nil {
		return err
	}
	for _, msg := range messages {
		at := msg.Timestamp.Time
		if at.IsZero() {
			continue
		}
		if state.OldestTS.IsZero() || at.Before(state.OldestTS) {
			state.OldestTS = at
		}
		if at.After(state.NewestTS) {
			state.NewestTS = at
		}
	}
	state.ReachedEnd = state.ReachedEnd || reachedEnd
	state.SyncedAt = time.Now()
	return c.store.SaveHistoryState(roomID, state)
}

// MarkRead clears a room's unread state.
func (c *Core) MarkRead(roomID string) {
	c.enqueue(func(c *Core) { c.markRead(roomID) })
}

func (c *Core) markRead(roomID string) {
	if roomID == "" {
		return
	}
	// Reading the room, however it happened, retires any deliberate unread.
	delete(c.heldUnread, roomID)

	// Anchor the local last-seen marker to the newest server timestamp we hold,
	// never to the local clock. The two routinely disagree — 94 seconds of skew
	// was measured against a real server during development — and a marker set
	// into the future would classify genuinely new messages as already seen,
	// silently suppressing the unread divider.
	//
	// When nothing is cached yet (a cold room being opened for the first time)
	// there is no trustworthy basis, so the marker is left alone: a zero anchor
	// clears the counters without moving it, and the authoritative `ls` arrives
	// with the next subscription sync. The store keeps the later of the two
	// values, so an older anchor can never regress the marker either.
	readAt, err := c.store.NewestTimestamp(roomID)
	if err != nil {
		c.reportError(err)
		return
	}

	// Clear locally first so the sidebar responds immediately; the server call
	// is the slow confirmation of what we already showed.
	if err := c.store.ClearUnread(roomID, readAt); err != nil {
		c.reportError(err)
		return
	}
	c.refreshRooms()
	c.background(func(ctx context.Context) error {
		return c.client.MarkRead(ctx, roomID)
	})
}

// MarkUnread flags a whole room unread again: its last message becomes new, the
// same thing the server does for the room-level form.
func (c *Core) MarkUnread(roomID string) {
	c.enqueue(func(c *Core) { c.markUnread(roomID, "") })
}

// MarkUnreadFrom flags a room unread starting at one message, so that message
// and everything after it counts as new. An empty messageID falls back to the
// room as a whole.
func (c *Core) MarkUnreadFrom(roomID, messageID string) {
	c.enqueue(func(c *Core) { c.markUnread(roomID, messageID) })
}

// unreadSnapshot is a room's local unread state before an optimistic write, kept
// so a rejected mark-unread can put back exactly what was on screen.
type unreadSnapshot struct {
	unread   int
	alert    bool
	lastSeen time.Time
	// marker and count are the frozen divider; frozen records whether it had
	// been decided at all, which is not the same as being decided as "none".
	marker time.Time
	count  int
	frozen bool
}

// markUnread is the counterpart to markRead. messageID names the first message
// that should count as new; empty means the room's last message.
//
// Like markRead it writes locally first and confirms over the network, but
// unlike markRead it restores the previous state if the server refuses: an
// unread badge the server does not agree with would survive until the next full
// sync, quietly claiming there is something to read.
func (c *Core) markUnread(roomID, messageID string) {
	if roomID == "" {
		return
	}

	target, cached, ok := c.unreadTarget(roomID, messageID)
	if !ok {
		return
	}

	previous := c.captureUnread(roomID)

	// A room whose history has never been loaded has nothing to anchor to. The
	// server knows its own last message, so the call still goes out and the room
	// simply alerts without a divider until the next sync — the "alert, no ls"
	// shape the sidebar already handles (deviation §13).
	anchor, unread := previous.lastSeen, 1
	if cached {
		var err error
		// The anchor sits between the target and the message before it, so the
		// divider lands immediately above the target rather than below it.
		anchor, err = c.store.UnreadAnchor(roomID, target.At)
		if err != nil {
			c.reportError(err)
			return
		}
		unread, err = c.store.CountAfter(roomID, anchor)
		if err != nil {
			c.reportError(err)
			return
		}
	}

	if err := c.store.SetUnread(roomID, unread, true, anchor); err != nil {
		c.reportError(err)
		return
	}

	// Hold the room unread against the realtime path: a message arriving in the
	// room the user is looking at normally marks it read again, which would undo
	// this within seconds of the next reply.
	c.heldUnread[roomID] = true

	// The divider is frozen per visit, so marking unread has to move it by hand;
	// the user is looking at the room and expects the line to appear now.
	if roomID == c.currentRoom {
		c.unreadMarker[roomID] = anchor
		c.unreadAtOpen[roomID] = unread
		c.emitTimeline(roomID)
	}
	c.refreshRooms()

	c.background(func(ctx context.Context) error {
		var err error
		if messageID != "" {
			err = c.client.MarkUnreadFrom(ctx, messageID)
		} else {
			err = c.client.MarkUnread(ctx, roomID)
		}
		if err != nil {
			c.enqueue(func(c *Core) { c.restoreUnread(roomID, previous) })
			return err
		}
		return nil
	})
}

// unreadTarget resolves the message a mark-unread is anchored to. cached is
// false when the room has no local history to anchor to — the mark still goes
// ahead, without a divider. ok is false when there is nothing to do at all, and
// the caller has already been told why.
func (c *Core) unreadTarget(roomID, messageID string) (target model.Message, cached, ok bool) {
	if messageID == "" {
		// The room's newest timeline message: what the server would pick too.
		latest, err := c.store.RoomTimeline(roomID, 1)
		if err != nil {
			c.reportError(err)
			return model.Message{}, false, false
		}
		if len(latest) == 0 {
			return model.Message{}, false, true
		}
		return latest[0], true, true
	}

	target, found, err := c.store.Message(messageID)
	if err != nil {
		c.reportError(err)
		return model.Message{}, false, false
	}
	if !found {
		c.emit(Notice{Text: "that message is no longer cached"})
		return model.Message{}, false, false
	}
	// The server refuses this outright (deviation §12), and its error says
	// nothing a user could act on.
	if target.Own {
		c.emit(Notice{Text: "Rocket.Chat won't mark your own message unread"})
		return model.Message{}, false, false
	}
	return target, true, true
}

// captureUnread records a room's unread state so it can be put back.
func (c *Core) captureUnread(roomID string) unreadSnapshot {
	room, _ := c.roomView(roomID)
	marker, frozen := c.unreadMarker[roomID]
	return unreadSnapshot{
		unread:   room.Unread,
		alert:    room.Alert,
		lastSeen: room.LastSeenAt,
		marker:   marker,
		count:    c.unreadAtOpen[roomID],
		frozen:   frozen,
	}
}

// restoreUnread undoes an optimistic mark-unread the server rejected.
func (c *Core) restoreUnread(roomID string, snapshot unreadSnapshot) {
	delete(c.heldUnread, roomID)
	if err := c.store.SetUnread(roomID, snapshot.unread, snapshot.alert, snapshot.lastSeen); err != nil {
		c.reportError(err)
		return
	}
	if snapshot.frozen {
		c.unreadMarker[roomID] = snapshot.marker
		c.unreadAtOpen[roomID] = snapshot.count
	} else {
		delete(c.unreadMarker, roomID)
		delete(c.unreadAtOpen, roomID)
	}
	c.refreshRooms()
	if roomID == c.currentRoom {
		c.emitTimeline(roomID)
	}
}

// React toggles a reaction on a message. The emoji is a shortcode such as
// "joy" or ":joy:".
//
// The server echoes the updated message over the websocket, which is what
// refreshes the timeline, so nothing is written optimistically here: a reaction
// that the server rejects should not linger on screen.
func (c *Core) React(messageID, shortcode string, add bool) {
	c.enqueue(func(c *Core) {
		if messageID == "" || shortcode == "" {
			return
		}
		roomID, threadID := c.currentRoom, c.currentThread
		c.background(func(ctx context.Context) error {
			if err := c.client.React(ctx, messageID, shortcode, add); err != nil {
				return err
			}
			// Fetch the message back rather than guessing at the new counts:
			// several people may have reacted since we last saw it.
			updated, err := c.client.GetMessage(ctx, messageID)
			if err != nil {
				return err
			}
			if err := c.store.SaveMessages([]rocket.Message{updated}); err != nil {
				return err
			}
			c.enqueue(func(c *Core) {
				if c.currentRoom == roomID {
					c.emitTimeline(roomID)
				}
				if threadID != "" && c.currentThread == threadID {
					c.emitThread(roomID, threadID)
				}
			})
			return nil
		})
	})
}

// Edit rewrites the text of a message the user already sent.
//
// Like React, nothing is written optimistically: the server owns whether an
// edit is allowed at all (editing can be disabled, or time-limited), so the
// timeline re-renders from the copy it sends back rather than from our guess.
func (c *Core) Edit(roomID, messageID, text string) {
	c.enqueue(func(c *Core) {
		if roomID == "" || messageID == "" || text == "" {
			return
		}
		threadID := c.currentThread

		c.background(func(ctx context.Context) error {
			updated, err := c.client.Update(ctx, roomID, messageID, text)
			if err != nil {
				return err
			}
			if updated.ID == "" {
				// The response shape is not something to depend on: caching a
				// message with no id would write a junk row. Ask for the message
				// instead, the way React does after a reaction.
				updated, err = c.client.GetMessage(ctx, messageID)
				if err != nil {
					return err
				}
			}
			if err := c.store.SaveMessages([]rocket.Message{updated}); err != nil {
				return err
			}
			c.enqueue(func(c *Core) {
				if c.currentRoom == roomID {
					c.emitTimeline(roomID)
				}
				if threadID != "" && c.currentThread == threadID {
					c.emitThread(roomID, threadID)
				}
			})
			return nil
		})
	})
}

// Send posts a message to a room, optionally into a thread.
//
// Uploads, if any, are what the composer had queued. Rocket.Chat has no notion
// of a message with files bolted on: each file is its own message, and the text
// rides on one of them. So a send with files is a run of uploads rather than a
// single call, and text with no files is the plain path below.
func (c *Core) Send(roomID, threadID, text string, uploads ...Upload) {
	c.enqueue(func(c *Core) {
		if roomID == "" || (text == "" && len(uploads) == 0) {
			return
		}
		c.stopTyping(roomID)

		c.background(func(ctx context.Context) error {
			if len(uploads) > 0 {
				return c.sendUploads(ctx, roomID, threadID, text, uploads)
			}

			sent, err := c.client.Send(ctx, rocket.SendOptions{
				RoomID:   roomID,
				Text:     text,
				ThreadID: threadID,
			})
			if err != nil {
				return err
			}
			if err := c.store.SaveMessages([]rocket.Message{sent}); err != nil {
				return err
			}
			c.enqueue(func(c *Core) { c.refreshAfterSend(roomID, threadID) })
			return nil
		})
	})
}

// refreshAfterSend re-publishes whichever panes the new message belongs in.
func (c *Core) refreshAfterSend(roomID, threadID string) {
	if c.currentRoom == roomID {
		c.emitTimeline(roomID)
	}
	if threadID != "" && c.currentThread == threadID {
		c.emitThread(roomID, threadID)
	}
}
