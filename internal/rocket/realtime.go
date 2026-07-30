package rocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ConnState is the realtime connection's lifecycle state.
type ConnState int

const (
	Disconnected ConnState = iota
	Connecting
	Connected
)

func (s ConnState) String() string {
	switch s {
	case Connecting:
		return "connecting"
	case Connected:
		return "connected"
	default:
		return "disconnected"
	}
}

// Event is a realtime push from the server.
type Event interface{ isRealtimeEvent() }

// ConnStateEvent reports connection transitions so the UI can show status.
type ConnStateEvent struct {
	State ConnState
	Err   error
}

// MessageEvent is a new or edited message.
type MessageEvent struct{ Message Message }

// MessageDeletedEvent is a message removal.
type MessageDeletedEvent struct {
	MessageID string
	RoomID    string
}

// SubscriptionEvent carries changed unread counts / mentions / last-seen.
type SubscriptionEvent struct {
	Action       string // inserted | updated | removed
	Subscription Subscription
}

// RoomChangedEvent carries changed room metadata.
type RoomChangedEvent struct {
	Action string
	Room   Room
}

// TypingEvent reports one user starting or stopping typing in a room.
type TypingEvent struct {
	RoomID   string
	Username string
	Typing   bool
}

func (ConnStateEvent) isRealtimeEvent()      {}
func (MessageEvent) isRealtimeEvent()        {}
func (MessageDeletedEvent) isRealtimeEvent() {}
func (SubscriptionEvent) isRealtimeEvent()   {}
func (RoomChangedEvent) isRealtimeEvent()    {}
func (TypingEvent) isRealtimeEvent()         {}

// subSpec is a subscription we want to hold. Specs are stored by a stable key
// so reconnects can replay them and callers can unsubscribe by name.
type subSpec struct {
	name   string
	params []any
}

// Realtime maintains a DDP connection and republishes server pushes on a single
// channel. It reconnects with backoff and replays subscriptions, so callers can
// treat it as always-on.
type Realtime struct {
	wsURL  string
	creds  func() Credentials
	logger *slog.Logger
	events chan Event

	mu      sync.Mutex
	state   ConnState
	session *ddpSession
	nextID  int64
	desired map[string]subSpec
}

// ddpSession is the per-connection state. A fresh one is built on every dial so
// in-flight calls from a dead connection can never resolve against a new one.
type ddpSession struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  map[string]chan ddpResult
	active   map[string]string // sub key -> ddp sub id
	closed   bool
	connOnce chan struct{} // closed when the server says "connected"
}

// NewRealtime builds a realtime client. credsFn is called on every connect so
// a refreshed token is picked up automatically.
func NewRealtime(wsURL string, credsFn func() Credentials, logger *slog.Logger) *Realtime {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Realtime{
		wsURL:   wsURL,
		creds:   credsFn,
		logger:  logger,
		events:  make(chan Event, 512),
		desired: make(map[string]subSpec),
	}
}

// Events returns the stream of server pushes.
//
// The channel is deliberately never closed: the read loop may still be
// publishing when Run returns, and closing it out from under that goroutine
// would panic. Consumers stop by cancelling the context they passed to Run.
func (r *Realtime) Events() <-chan Event { return r.events }

// State returns the current connection state.
func (r *Realtime) State() ConnState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// Run owns the connection until ctx is cancelled. It never returns an error for
// transient failures: those are reported as ConnStateEvent and retried.
func (r *Realtime) Run(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for ctx.Err() == nil {
		r.setState(Connecting, nil)
		err := r.connectAndServe(ctx)
		if ctx.Err() != nil {
			break
		}
		r.setState(Disconnected, err)
		if err != nil {
			r.logger.Warn("realtime disconnected", "err", err, "retry_in", backoff)
		}

		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
		if err == nil {
			backoff = time.Second
		}
	}
	r.setState(Disconnected, nil)
}

func (r *Realtime) connectAndServe(ctx context.Context) error {
	creds := r.creds()
	if !creds.Valid() {
		return errors.New("realtime: no credentials")
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout: 20 * time.Second,
	}
	dialCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	conn, _, err := dialer.DialContext(dialCtx, r.wsURL, nil)
	if err != nil {
		return fmt.Errorf("realtime: dial %s: %w", r.wsURL, err)
	}

	session := &ddpSession{
		conn:     conn,
		pending:  make(map[string]chan ddpResult),
		active:   make(map[string]string),
		connOnce: make(chan struct{}),
	}
	r.mu.Lock()
	r.session = session
	r.mu.Unlock()

	defer func() {
		session.close(errors.New("realtime: connection closed"))
		r.mu.Lock()
		if r.session == session {
			r.session = nil
		}
		r.mu.Unlock()
	}()

	// Read loop pushes frames; it exits when the socket dies.
	readErr := make(chan error, 1)
	go func() { readErr <- r.readLoop(ctx, session) }()

	if err := session.send(ddpFrame{Msg: "connect", Version: "1"}, []string{"1"}); err != nil {
		return err
	}

	// Wait for the DDP handshake.
	select {
	case <-session.connOnce:
	case err := <-readErr:
		return err
	case <-time.After(20 * time.Second):
		return drainReadLoop(session, readErr, errors.New("realtime: handshake timed out"))
	case <-ctx.Done():
		return drainReadLoop(session, readErr, ctx.Err())
	}

	// Authenticate the socket with the same token REST uses.
	loginCtx, loginCancel := context.WithTimeout(ctx, 20*time.Second)
	_, err = r.callOn(loginCtx, session, "login", []any{map[string]any{"resume": creds.AuthToken}})
	loginCancel()
	if err != nil {
		return fmt.Errorf("realtime: login: %w", err)
	}

	r.setState(Connected, nil)

	// Replay every subscription we are supposed to hold.
	r.mu.Lock()
	specs := make(map[string]subSpec, len(r.desired))
	for key, spec := range r.desired {
		specs[key] = spec
	}
	r.mu.Unlock()
	for key, spec := range specs {
		if err := r.sendSub(session, key, spec); err != nil {
			return err
		}
	}

	// Keep the socket warm; Rocket.Chat drops idle connections.
	pinger := time.NewTicker(25 * time.Second)
	defer pinger.Stop()
	for {
		select {
		case err := <-readErr:
			return err
		case <-ctx.Done():
			return drainReadLoop(session, readErr, ctx.Err())
		case <-pinger.C:
			if err := session.send(ddpFrame{Msg: "ping"}, nil); err != nil {
				return err
			}
		}
	}
}

// drainReadLoop closes the socket so the read loop unblocks, then waits for it
// to finish. Without this, a cancelled context can leave the read loop running
// after Run has returned.
func drainReadLoop(session *ddpSession, readErr <-chan error, cause error) error {
	session.close(cause)
	select {
	case <-readErr:
	case <-time.After(2 * time.Second):
	}
	return cause
}

func (r *Realtime) readLoop(ctx context.Context, session *ddpSession) error {
	for {
		_, raw, err := session.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("realtime: read: %w", err)
		}
		var frame ddpFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			r.logger.Debug("realtime: undecodable frame", "err", err)
			continue
		}
		if err := r.handleFrame(session, frame); err != nil {
			return err
		}
	}
}

func (r *Realtime) handleFrame(session *ddpSession, frame ddpFrame) error {
	switch frame.Msg {
	case "ping":
		return session.send(ddpFrame{Msg: "pong", ID: frame.ID}, nil)
	case "pong":
		return nil
	case "connected":
		session.markConnected()
		return nil
	case "failed":
		return fmt.Errorf("realtime: server requires DDP version %q", frame.Version)
	case "result":
		var res ddpResult
		if frame.Error != nil {
			res.err = frame.Error
		} else {
			res.value = frame.Result
		}
		session.resolve(frame.ID, res)
		return nil
	case "nosub":
		if frame.Error != nil {
			r.logger.Warn("realtime: subscription rejected", "err", frame.Error.Error())
		}
		return nil
	case "added", "changed", "removed":
		r.handleStream(frame)
		return nil
	default: // "ready", "updated", "addedBefore", ...
		return nil
	}
}

// handleStream converts a collection push into a domain event.
func (r *Realtime) handleStream(frame ddpFrame) {
	if frame.Fields == nil {
		return
	}
	event := frame.Fields.EventName
	args := frame.Fields.Args

	switch frame.Collection {
	case streamRoomMessages:
		for _, arg := range args {
			var msg Message
			if err := json.Unmarshal(arg, &msg); err != nil {
				continue
			}
			if msg.ID == "" {
				continue
			}
			if msg.RoomID == "" && event != allMessages {
				msg.RoomID = event
			}
			r.emit(MessageEvent{Message: msg})
		}

	case streamNotifyRoom:
		roomID, suffix := splitEventName(event)
		switch suffix {
		case "typing":
			// Legacy shape: [username, isTyping]
			if len(args) < 2 {
				return
			}
			username, ok := decodeString(args[0])
			if !ok {
				return
			}
			var typing bool
			if err := json.Unmarshal(args[1], &typing); err != nil {
				return
			}
			r.emit(TypingEvent{RoomID: roomID, Username: username, Typing: typing})

		case "user-activity":
			// Current shape: [username, ["user-typing", ...], {extras}]
			if len(args) < 2 {
				return
			}
			username, ok := decodeString(args[0])
			if !ok {
				return
			}
			var activities []string
			if err := json.Unmarshal(args[1], &activities); err != nil {
				return
			}
			typing := false
			for _, activity := range activities {
				if activity == "user-typing" {
					typing = true
					break
				}
			}
			r.emit(TypingEvent{RoomID: roomID, Username: username, Typing: typing})

		case "deleteMessage":
			if len(args) < 1 {
				return
			}
			var deleted struct {
				ID string `json:"_id"`
			}
			if err := json.Unmarshal(args[0], &deleted); err != nil || deleted.ID == "" {
				return
			}
			r.emit(MessageDeletedEvent{MessageID: deleted.ID, RoomID: roomID})
		}

	case streamNotifyUser:
		_, suffix := splitEventName(event)
		if len(args) < 2 {
			return
		}
		action, ok := decodeString(args[0])
		if !ok {
			return
		}
		switch suffix {
		case "subscriptions-changed":
			var sub Subscription
			if err := json.Unmarshal(args[1], &sub); err != nil || sub.RoomID == "" {
				return
			}
			r.emit(SubscriptionEvent{Action: action, Subscription: sub})
		case "rooms-changed":
			var room Room
			if err := json.Unmarshal(args[1], &room); err != nil || room.ID == "" {
				return
			}
			r.emit(RoomChangedEvent{Action: action, Room: room})
		}
	}
}

// splitEventName splits "<roomId>/typing" into its parts.
func splitEventName(event string) (prefix, suffix string) {
	idx := strings.LastIndex(event, "/")
	if idx < 0 {
		return event, ""
	}
	return event[:idx], event[idx+1:]
}

func decodeString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// emit publishes an event, dropping it if the consumer has fallen far behind
// rather than stalling the read loop.
func (r *Realtime) emit(event Event) {
	select {
	case r.events <- event:
	default:
		r.logger.Warn("realtime: event buffer full, dropping event", "type", fmt.Sprintf("%T", event))
	}
}

func (r *Realtime) setState(state ConnState, err error) {
	r.mu.Lock()
	unchanged := r.state == state && err == nil
	r.state = state
	r.mu.Unlock()
	if unchanged {
		return
	}
	r.emit(ConnStateEvent{State: state, Err: err})
}

// ---- subscription management -------------------------------------------------

// SubscribeRoomMessages streams new messages for a room. This is in addition to
// the global stream, which is enough for unread counts but not guaranteed to
// include rooms opened ad hoc.
func (r *Realtime) SubscribeRoomMessages(roomID string) {
	r.subscribe("messages:"+roomID, subSpec{name: streamRoomMessages, params: []any{roomID, false}})
}

// UnsubscribeRoomMessages drops a per-room message stream.
func (r *Realtime) UnsubscribeRoomMessages(roomID string) {
	r.unsubscribe("messages:" + roomID)
}

// SubscribeAllMessages streams every message the user can see, which keeps
// unread state live for rooms that are not currently open.
func (r *Realtime) SubscribeAllMessages() {
	r.subscribe("messages:all", subSpec{name: streamRoomMessages, params: []any{allMessages, false}})
}

// SubscribeUserEvents streams subscription and room changes (unreads, mentions,
// new DMs, new discussions).
func (r *Realtime) SubscribeUserEvents(userID string) {
	r.subscribe("user:subs", subSpec{name: streamNotifyUser, params: []any{userID + "/subscriptions-changed", false}})
	r.subscribe("user:rooms", subSpec{name: streamNotifyUser, params: []any{userID + "/rooms-changed", false}})
}

// SubscribeRoomActivity streams typing indicators and deletions for a room.
// Both the legacy and current typing streams are requested because which one a
// server emits depends on its version.
func (r *Realtime) SubscribeRoomActivity(roomID string) {
	r.subscribe("typing:"+roomID, subSpec{name: streamNotifyRoom, params: []any{roomID + "/typing", false}})
	r.subscribe("activity:"+roomID, subSpec{name: streamNotifyRoom, params: []any{roomID + "/user-activity", false}})
	r.subscribe("deletes:"+roomID, subSpec{name: streamNotifyRoom, params: []any{roomID + "/deleteMessage", false}})
}

// UnsubscribeRoomActivity drops the per-room activity streams.
func (r *Realtime) UnsubscribeRoomActivity(roomID string) {
	r.unsubscribe("typing:" + roomID)
	r.unsubscribe("activity:" + roomID)
	r.unsubscribe("deletes:" + roomID)
}

func (r *Realtime) subscribe(key string, spec subSpec) {
	r.mu.Lock()
	if existing, ok := r.desired[key]; ok && existing.name == spec.name {
		session := r.session
		r.mu.Unlock()
		// Already desired; make sure it is live on the current session.
		if session != nil {
			session.mu.Lock()
			_, live := session.active[key]
			session.mu.Unlock()
			if live {
				return
			}
			if err := r.sendSub(session, key, spec); err != nil {
				r.logger.Debug("realtime: subscribe failed", "key", key, "err", err)
			}
		}
		return
	}
	r.desired[key] = spec
	session := r.session
	r.mu.Unlock()

	if session == nil {
		return // replayed on next connect
	}
	if err := r.sendSub(session, key, spec); err != nil {
		r.logger.Debug("realtime: subscribe failed", "key", key, "err", err)
	}
}

func (r *Realtime) unsubscribe(key string) {
	r.mu.Lock()
	delete(r.desired, key)
	session := r.session
	r.mu.Unlock()
	if session == nil {
		return
	}
	session.mu.Lock()
	subID, ok := session.active[key]
	delete(session.active, key)
	session.mu.Unlock()
	if !ok {
		return
	}
	if err := session.send(ddpFrame{Msg: "unsub", ID: subID}, nil); err != nil {
		r.logger.Debug("realtime: unsub failed", "key", key, "err", err)
	}
}

func (r *Realtime) sendSub(session *ddpSession, key string, spec subSpec) error {
	subID := r.newID()
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return errors.New("realtime: session closed")
	}
	session.active[key] = subID
	session.mu.Unlock()

	return session.sendSub(subID, spec)
}

// ---- method calls ------------------------------------------------------------

// NotifyTyping tells the server the user started or stopped typing, the same way
// the web client does. Both stream shapes are sent so the indicator shows up
// regardless of server version.
func (r *Realtime) NotifyTyping(ctx context.Context, roomID, username string, typing bool) error {
	r.mu.Lock()
	session := r.session
	state := r.state
	r.mu.Unlock()
	if session == nil || state != Connected {
		return errors.New("realtime: not connected")
	}

	activities := []any{}
	if typing {
		activities = append(activities, "user-typing")
	}

	// Fire and forget: the result carries no information we act on, and typing
	// notifications must never block the input loop.
	if err := session.sendMethod(r.newID(), "stream-notify-room",
		[]any{roomID + "/user-activity", username, activities, map[string]any{}}); err != nil {
		return err
	}
	return session.sendMethod(r.newID(), "stream-notify-room",
		[]any{roomID + "/typing", username, typing})
}

// Call invokes a DDP method and waits for its result.
func (r *Realtime) Call(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	r.mu.Lock()
	session := r.session
	state := r.state
	r.mu.Unlock()
	if session == nil || state != Connected {
		return nil, errors.New("realtime: not connected")
	}
	return r.callOn(ctx, session, method, params)
}

func (r *Realtime) callOn(ctx context.Context, session *ddpSession, method string, params []any) (json.RawMessage, error) {
	id := r.newID()
	resultCh := make(chan ddpResult, 1)

	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return nil, errors.New("realtime: session closed")
	}
	session.pending[id] = resultCh
	session.mu.Unlock()

	cleanup := func() {
		session.mu.Lock()
		delete(session.pending, id)
		session.mu.Unlock()
	}

	if err := session.sendMethod(id, method, params); err != nil {
		cleanup()
		return nil, err
	}

	select {
	case res := <-resultCh:
		cleanup()
		return res.value, res.err
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	}
}

func (r *Realtime) newID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	return strconv.FormatInt(r.nextID, 10)
}

// ---- session plumbing --------------------------------------------------------

func (s *ddpSession) markConnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.connOnce:
	default:
		close(s.connOnce)
	}
}

func (s *ddpSession) resolve(id string, res ddpResult) {
	s.mu.Lock()
	ch, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ok {
		ch <- res
	}
}

func (s *ddpSession) close(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	pending := s.pending
	s.pending = make(map[string]chan ddpResult)
	s.active = make(map[string]string)
	s.mu.Unlock()

	for _, ch := range pending {
		ch <- ddpResult{err: err}
	}
	_ = s.conn.Close()
}

// send writes a raw frame. support is only used by the connect handshake, which
// needs a field the shared frame struct does not carry.
func (s *ddpSession) send(frame ddpFrame, support []string) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if support != nil {
		var asMap map[string]any
		if err := json.Unmarshal(payload, &asMap); err != nil {
			return err
		}
		asMap["support"] = support
		if payload, err = json.Marshal(asMap); err != nil {
			return err
		}
	}
	return s.writeRaw(payload)
}

func (s *ddpSession) sendMethod(id, method string, params []any) error {
	if params == nil {
		params = []any{}
	}
	payload, err := json.Marshal(map[string]any{
		"msg":    "method",
		"method": method,
		"id":     id,
		"params": params,
	})
	if err != nil {
		return err
	}
	return s.writeRaw(payload)
}

func (s *ddpSession) sendSub(id string, spec subSpec) error {
	params := spec.params
	if params == nil {
		params = []any{}
	}
	payload, err := json.Marshal(map[string]any{
		"msg":    "sub",
		"id":     id,
		"name":   spec.name,
		"params": params,
	})
	if err != nil {
		return err
	}
	return s.writeRaw(payload)
}

func (s *ddpSession) writeRaw(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("realtime: write: %w", err)
	}
	return nil
}
