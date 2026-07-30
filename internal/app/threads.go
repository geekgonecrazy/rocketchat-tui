package app

import (
	"context"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// OpenThread opens a thread by its parent message id.
func (c *Core) OpenThread(roomID, threadID string) {
	c.enqueue(func(c *Core) {
		if threadID == "" {
			return
		}
		c.currentThread = threadID
		c.emitThread(roomID, threadID)

		c.background(func(ctx context.Context) error {
			// The parent may be older than our cached page, so fetch it if needed.
			if _, found, err := c.store.Message(threadID); err != nil {
				return err
			} else if !found {
				parent, err := c.client.GetMessage(ctx, threadID)
				if err != nil {
					return err
				}
				if err := c.store.SaveMessages([]rocket.Message{parent}); err != nil {
					return err
				}
			}
			replies, err := c.client.ThreadMessages(ctx, threadID, 0, 0)
			if err != nil {
				return err
			}
			if err := c.store.SaveMessages(replies); err != nil {
				return err
			}
			c.enqueue(func(c *Core) {
				if c.currentThread == threadID {
					c.emitThread(roomID, threadID)
				}
			})
			return nil
		})
	})
}

// CloseThread leaves thread view.
func (c *Core) CloseThread() {
	c.enqueue(func(c *Core) { c.currentThread = "" })
}

func (c *Core) emitThread(roomID, threadID string) {
	parent, found, err := c.store.Message(threadID)
	if err != nil {
		c.reportError(err)
		return
	}
	if !found {
		parent = model.Message{ID: threadID, RoomID: roomID, Text: "loading…"}
	}
	replies, err := c.store.ThreadReplies(threadID)
	if err != nil {
		c.reportError(err)
		return
	}
	c.emit(ThreadUpdated{
		RoomID:   roomID,
		ThreadID: threadID,
		Parent:   parent,
		Replies:  replies,
	})
}

// RefreshThreadList reloads the room's thread list.
func (c *Core) RefreshThreadList(roomID string) {
	c.enqueue(func(c *Core) { c.loadThreadList(roomID) })
}

// markThreadListDirty schedules a thread-list refresh, coalescing bursts.
//
// A reply carries only its parent's id; the parent row we hold still says
// tcount=0, so no local update can reveal that a thread now exists. Only the
// server knows, which is why this re-fetches rather than patching the cache.
func (c *Core) markThreadListDirty(roomID string) {
	if roomID == "" {
		return
	}
	if time.Since(c.threadListSynced[roomID]) >= threadListDebounce {
		c.loadThreadList(roomID)
		return
	}
	c.threadListDirty[roomID] = true
}

// flushThreadLists services refreshes deferred by the debounce.
func (c *Core) flushThreadLists(now time.Time) {
	for roomID, dirty := range c.threadListDirty {
		if !dirty || now.Sub(c.threadListSynced[roomID]) < threadListDebounce {
			continue
		}
		delete(c.threadListDirty, roomID)
		c.loadThreadList(roomID)
	}
}

func (c *Core) loadThreadList(roomID string) {
	if roomID == "" {
		return
	}
	c.threadListSynced[roomID] = time.Now()
	c.emitThreadList(roomID)
	c.background(func(ctx context.Context) error {
		threads, _, err := c.client.ThreadsList(ctx, roomID, threadPageSize, 0)
		if err != nil {
			// Threads can be disabled server-side; that is not worth a red banner.
			c.logger.Debug("thread list unavailable", "room", roomID, "err", err)
			return nil
		}
		if err := c.store.SaveMessages(threads); err != nil {
			return err
		}
		c.enqueue(func(c *Core) {
			if c.currentRoom == roomID {
				c.emitThreadList(roomID)
			}
		})
		return nil
	})
}

func (c *Core) emitThreadList(roomID string) {
	threads, err := c.store.ThreadParents(roomID, threadPageSize)
	if err != nil {
		c.reportError(err)
		return
	}
	c.emit(ThreadListUpdated{RoomID: roomID, Threads: threads})
}
