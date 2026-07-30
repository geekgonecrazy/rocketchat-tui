// Package app is the headless core: it owns the SDK, the cache, and all
// application state, and publishes state changes as events.
//
// The UI is a pure consumer. It never calls the SDK, never touches SQLite, and
// never shares mutable state with this package — which is what keeps rendering
// independent of the network and the redraw loop free of locks.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
	"github.com/geekgonecrazy/rocketchat-tui/internal/store"
)

const (
	// timelinePageSize is how many messages we fetch and render per page.
	timelinePageSize = 60
	// threadPageSize bounds the thread list.
	threadPageSize = 50
	// resyncInterval is the safety net for missed realtime pushes.
	resyncInterval = 2 * time.Minute
	// threadListDebounce bounds how often a room's thread list is re-fetched
	// while replies are arriving.
	threadListDebounce = 2 * time.Second
	// tickInterval drives typing expiry and deferred typing stops.
	tickInterval = time.Second
)

// action is a unit of work run on the core's single goroutine. Every state
// mutation goes through one, so no field in Core needs a mutex.
type action func(*Core)

// Core owns application state and is the only writer of it.
type Core struct {
	client   *rocket.Client
	store    *store.Store
	realtime *rocket.Realtime
	logger   *slog.Logger

	events   chan Event
	actions  chan action
	done     chan struct{}
	rtEvents <-chan rocket.Event
	ctx      context.Context

	// --- state below is owned by the run loop ---

	selfID       string
	selfUsername string

	currentRoom   string
	currentThread string

	// unreadMarker is frozen per room when it is opened, so the divider does not
	// move while the user reads.
	unreadMarker map[string]time.Time
	// unreadAtOpen is the server's unread count when the room was opened. The
	// server often reports alert=true with no count at all, so this is zero more
	// often than not, and callers must treat zero as "unknown", not "none".
	unreadAtOpen map[string]int
	// heldUnread marks rooms the user deliberately marked unread. Without it the
	// next message to arrive in the open room would mark it read again, undoing
	// the very thing they asked for.
	heldUnread map[string]bool
	// hasMore tracks whether older history may exist server-side.
	hasMore     map[string]bool
	loadingMore map[string]bool
	// timelineLimit is how many messages a room's timeline currently shows. It
	// grows as the user pages backwards, otherwise loading older history would
	// fetch rows the timeline query then trims straight back off.
	timelineLimit map[string]int

	// typists maps room -> username -> expiry, so a missed "stopped typing"
	// cannot pin the indicator on forever.
	typists map[string]map[string]time.Time

	// membersSynced is when each room's roster was last fetched, so revisiting a
	// room does not re-list its members on every open.
	membersSynced map[string]time.Time

	// Thread membership lives on the parent message, so a reply arriving does
	// not tell us the parent is now a thread. The list has to be re-fetched, and
	// these two fields keep a busy thread from causing a request per reply.
	threadListDirty  map[string]bool
	threadListSynced map[string]time.Time

	// outgoing typing state, mirroring the web client's throttling
	typingActive  map[string]bool
	typingRefresh map[string]time.Time
	typingExpiry  map[string]time.Time

	conn    rocket.ConnState
	syncing int
}

// New builds a core. Run must be called before any other method.
func New(client *rocket.Client, cache *store.Store, logger *slog.Logger) *Core {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Core{
		client:           client,
		store:            cache,
		logger:           logger,
		events:           make(chan Event, 512),
		actions:          make(chan action, 256),
		done:             make(chan struct{}),
		ctx:              context.Background(),
		unreadMarker:     make(map[string]time.Time),
		unreadAtOpen:     make(map[string]int),
		heldUnread:       make(map[string]bool),
		hasMore:          make(map[string]bool),
		loadingMore:      make(map[string]bool),
		timelineLimit:    make(map[string]int),
		typists:          make(map[string]map[string]time.Time),
		membersSynced:    make(map[string]time.Time),
		threadListDirty:  make(map[string]bool),
		threadListSynced: make(map[string]time.Time),
		typingActive:     make(map[string]bool),
		typingRefresh:    make(map[string]time.Time),
		typingExpiry:     make(map[string]time.Time),
	}
}

// Events is the stream the UI renders from. It closes when Run returns.
func (c *Core) Events() <-chan Event { return c.events }

// Run drives the core until ctx is cancelled.
func (c *Core) Run(ctx context.Context) {
	c.ctx = ctx
	defer close(c.events)
	defer close(c.done)

	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	resync := time.NewTicker(resyncInterval)
	defer resync.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case act := <-c.actions:
			act(c)

		case event, ok := <-c.rtEvents:
			if !ok {
				c.rtEvents = nil
				continue
			}
			c.handleRealtime(event)

		case now := <-tick.C:
			c.expireTypists(now)
			c.expireOutgoingTyping(now)
			c.flushThreadLists(now)

		case <-resync.C:
			c.refreshRooms()
			if c.currentRoom != "" {
				c.catchUpRoom(c.currentRoom)
			}
		}
	}
}

// enqueue schedules work on the run loop, abandoning it if the core has stopped.
func (c *Core) enqueue(act action) {
	select {
	case c.actions <- act:
	case <-c.done:
	}
}

// emit publishes an event to the UI.
//
// emit is only ever called from the run loop, so it can never race with the
// deferred close of c.events. It watches the context as well as c.done because a
// UI that has stopped draining would otherwise wedge the loop, and the loop is
// what would notice the cancellation.
func (c *Core) emit(event Event) {
	select {
	case c.events <- event:
	case <-c.ctx.Done():
	case <-c.done:
	}
}

// background runs a network call off the loop and reports completion back onto
// it, so the UI keeps redrawing while requests are in flight.
func (c *Core) background(fn func(context.Context) error) {
	c.syncing++
	c.emitStatus(nil)
	ctx := c.ctx
	go func() {
		err := fn(ctx)
		c.enqueue(func(c *Core) {
			c.syncing--
			if err != nil && !errors.Is(err, context.Canceled) {
				c.reportError(err)
			}
			c.emitStatus(nil)
		})
	}()
}

func (c *Core) emitStatus(err error) {
	c.emit(StatusChanged{Connection: c.conn, Syncing: c.syncing > 0, Err: err})
}

// reportError turns an error into either a re-login prompt or a status notice.
func (c *Core) reportError(err error) {
	var apiErr *rocket.APIError
	if errors.As(err, &apiErr) && apiErr.Unauthorized() {
		c.emit(SessionInvalid{Reason: "session expired, please log in again"})
		return
	}
	c.logger.Warn("core error", "err", err)
	c.emit(Notice{Text: err.Error(), IsErr: true})
}

// ---- session ----------------------------------------------------------------

// Start brings the session up: it serves the cache immediately, then validates
// the token, syncs, and connects realtime.
func (c *Core) Start(userID, username string) {
	c.enqueue(func(c *Core) {
		c.selfID = userID
		c.selfUsername = username
		c.store.SetIdentity(userID, username)

		// Offline-first: render whatever the cache holds before any network call.
		c.refreshRooms()
		c.emit(SessionReady{Username: username, ServerURL: c.client.ServerURL()})

		c.startRealtime()
		c.background(func(ctx context.Context) error {
			me, err := c.client.Me(ctx)
			if err != nil {
				return err
			}
			c.enqueue(func(c *Core) {
				if me.ID != "" {
					c.selfID = me.ID
				}
				if me.Username != "" {
					c.selfUsername = me.Username
				}
				c.store.SetIdentity(c.selfID, c.selfUsername)
			})
			return c.syncRooms(ctx)
		})
	})
}

func (c *Core) startRealtime() {
	if c.realtime != nil {
		return
	}
	c.realtime = rocket.NewRealtime(c.client.WebSocketURL(), c.client.Credentials, c.logger)
	c.rtEvents = c.realtime.Events()
	go c.realtime.Run(c.ctx)
}

// ---- rooms ------------------------------------------------------------------

// Refresh forces a full room + current-room resync.
func (c *Core) Refresh() {
	c.enqueue(func(c *Core) {
		c.background(c.syncRooms)
		if c.currentRoom != "" {
			c.catchUpRoom(c.currentRoom)
			// An explicit resync means "now", so the roster's TTL does not apply.
			delete(c.membersSynced, c.currentRoom)
			c.syncMembers(c.currentRoom)
		}
	})
}

// syncRooms pulls subscriptions and room metadata, using delta cursors when we
// have them so a warm start is cheap.
func (c *Core) syncRooms(ctx context.Context) error {
	subsSince, err := c.store.MetaTime(metaSubscriptionsSince)
	if err != nil {
		return err
	}
	roomsSince, err := c.store.MetaTime(metaRoomsSince)
	if err != nil {
		return err
	}
	syncStart := time.Now()

	subs, err := c.client.Subscriptions(ctx, subsSince)
	if err != nil {
		return err
	}
	rooms, err := c.client.Rooms(ctx, roomsSince)
	if err != nil {
		return err
	}
	if err := c.store.SaveSubscriptions(subs); err != nil {
		return err
	}
	if err := c.store.SaveRooms(rooms); err != nil {
		return err
	}
	if err := c.store.SetMetaTime(metaSubscriptionsSince, syncStart); err != nil {
		return err
	}
	if err := c.store.SetMetaTime(metaRoomsSince, syncStart); err != nil {
		return err
	}

	c.enqueue(func(c *Core) {
		if c.currentRoom != "" {
			c.freezeUnreadMarker(c.currentRoom)
		}
		c.refreshRooms()
		if c.currentRoom != "" {
			c.emitTimeline(c.currentRoom)
		}
	})
	return nil
}

// refreshRooms re-reads the sidebar from cache and publishes it.
func (c *Core) refreshRooms() {
	rooms, err := c.store.Rooms()
	if err != nil {
		c.reportError(err)
		return
	}
	c.emit(RoomsUpdated{Rooms: rooms, Totals: computeTotals(rooms)})
}

// currentRoomView loads the open room's metadata for the header.
func (c *Core) currentRoomView(roomID string) model.Room {
	room, _ := c.roomView(roomID)
	return room
}

// roomView loads a room's metadata, reporting whether we actually know it. The
// distinction matters for the unread divider: an unknown room is "not yet
// synced", which is not the same as "nothing unread".
func (c *Core) roomView(roomID string) (model.Room, bool) {
	room, found, err := c.store.Room(roomID)
	if err != nil {
		c.logger.Debug("room lookup failed", "room", roomID, "err", err)
	}
	if !found {
		room.ID = roomID
	}
	return room, found
}

const (
	metaSubscriptionsSince = "subscriptions_since"
	metaRoomsSince         = "rooms_since"
)

// resolveRoomType returns the wire room type, fetching it if the room is not
// cached yet (a brand new DM or discussion).
func (c *Core) resolveRoomType(ctx context.Context, roomID string) (string, error) {
	roomType, err := c.store.RoomType(roomID)
	if err != nil {
		return "", err
	}
	if roomType != "" {
		return roomType, nil
	}
	room, err := c.client.RoomInfo(ctx, roomID)
	if err != nil {
		return "", fmt.Errorf("resolve room %s: %w", roomID, err)
	}
	if err := c.store.SaveRooms([]rocket.Room{room}); err != nil {
		return "", err
	}
	return room.Type, nil
}
