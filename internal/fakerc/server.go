// Package fakerc is a stand-in Rocket.Chat server for tests: REST endpoints
// plus a DDP websocket that speaks the same frames a real server does,
// including EJSON `{"$date": ms}` timestamps.
package fakerc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// AuthToken and UserID are the credentials the fake server accepts.
const (
	AuthToken = "test-auth-token"
	UserID    = "self-user-id"
	Username  = "tester"
	Password  = "hunter2"
	TOTPCode  = "424242"
)

// Notification is a typing notification the client sent us.
type Notification struct {
	EventName string
	Username  string
	Typing    bool
}

// SentMessage is a message the client posted.
type SentMessage struct {
	RoomID   string
	Text     string
	ThreadID string
}

// EditedMessage is a chat.update call the client made.
type EditedMessage struct {
	RoomID    string
	MessageID string
	Text      string
}

// UnreadMark is a subscriptions.unread call. Exactly one field is set: the
// endpoint takes either a room or a first-unread message, never both.
type UnreadMark struct {
	RoomID    string
	MessageID string
}

// Upload is a file the client posted. Route records which of the two upload
// flows carried it, since a client is expected to prefer one and fall back to
// the other.
type Upload struct {
	RoomID   string
	Route    string // "media" or "upload"
	Filename string
	// MIME is the Content-Type the client put on the file part, not one the
	// server guessed. Getting this wrong is what makes an image arrive as an
	// opaque download, so a test needs to see exactly what was sent.
	MIME     string
	Bytes    []byte
	Text     string
	ThreadID string
}

// wsConn serializes writes to one websocket. gorilla panics on concurrent
// writes, and both request handling and broadcasts write to the same socket.
type wsConn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (c *wsConn) write(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.WriteMessage(websocket.TextMessage, payload)
}

// Server is a fake Rocket.Chat instance.
type Server struct {
	*httptest.Server

	// RequireTOTP makes the first password login fail with totp-required.
	RequireTOTP bool
	// RejectUnread makes subscriptions.unread fail the way a real server does
	// for a message the caller wrote, so clients can be tested against a refusal.
	RejectUnread bool
	// RejectEdit makes chat.update fail the way a server with editing disabled
	// or time-limited does.
	RejectEdit bool
	// NoMediaRoute makes rooms.media 404, the way a server predating that route
	// does, so a client's fallback to rooms.upload can be exercised.
	NoMediaRoute bool
	// RejectUpload makes both upload routes fail, standing in for a rejected
	// media type or an oversized file.
	RejectUpload bool
	// RejectCommandList makes commands.list fail the way a server that does not
	// serve it, or an account without the permission for it, does.
	RejectCommandList bool
	// NoSuchUsers are usernames users.info refuses, so that a command working
	// through a list of people can be caught partway.
	NoSuchUsers []string

	mu            sync.Mutex
	rooms         []map[string]any
	subscriptions []map[string]any
	messages      []map[string]any
	threads       []map[string]any
	members       map[string][]map[string]any
	memberPaths   []string
	conns         []*wsConn
	notifications []Notification
	sent          []SentMessage
	edited        []EditedMessage
	readRooms     []string
	unreadMarks   []UnreadMark
	uploads       []Upload
	commands      []map[string]any
	ranCommands   []RanCommand
	roomActions   []RoomAction
	pendingMedia  map[string]Upload
	fileRequests  int
	nextID        int
	t             *testing.T
}

// New starts a fake server. It is shut down when the test ends.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{
		t:            t,
		members:      make(map[string][]map[string]any),
		pendingMedia: make(map[string]Upload),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/login", s.handleLogin)
	mux.HandleFunc("/api/v1/me", s.authed(s.handleMe))
	mux.HandleFunc("/api/v1/subscriptions.get", s.authed(s.handleSubscriptions))
	mux.HandleFunc("/api/v1/rooms.get", s.authed(s.handleRooms))
	mux.HandleFunc("/api/v1/rooms.info", s.authed(s.handleRoomInfo))
	mux.HandleFunc("/api/v1/channels.history", s.authed(s.handleHistory))
	mux.HandleFunc("/api/v1/groups.history", s.authed(s.handleHistory))
	mux.HandleFunc("/api/v1/im.history", s.authed(s.handleHistory))
	mux.HandleFunc("/api/v1/channels.members", s.authed(s.handleMembers))
	mux.HandleFunc("/api/v1/groups.members", s.authed(s.handleMembers))
	mux.HandleFunc("/api/v1/im.members", s.authed(s.handleMembers))
	mux.HandleFunc("/api/v1/chat.syncMessages", s.authed(s.handleSyncMessages))
	mux.HandleFunc("/api/v1/chat.getThreadsList", s.authed(s.handleThreadsList))
	mux.HandleFunc("/api/v1/chat.getThreadMessages", s.authed(s.handleThreadMessages))
	mux.HandleFunc("/api/v1/chat.getMessage", s.authed(s.handleGetMessage))
	mux.HandleFunc("/api/v1/chat.sendMessage", s.authed(s.handleSendMessage))
	mux.HandleFunc("/api/v1/chat.update", s.authed(s.handleUpdateMessage))
	mux.HandleFunc("/api/v1/subscriptions.read", s.authed(s.handleMarkRead))
	mux.HandleFunc("/api/v1/subscriptions.unread", s.authed(s.handleMarkUnread))
	mux.HandleFunc("/api/v1/rooms.media/", s.authed(s.handleRoomsMedia))
	mux.HandleFunc("/api/v1/rooms.mediaConfirm/", s.authed(s.handleRoomsMediaConfirm))
	mux.HandleFunc("/api/v1/rooms.upload/", s.authed(s.handleRoomsUpload))
	mux.HandleFunc("/api/v1/commands.list", s.authed(s.handleCommandsList))
	mux.HandleFunc("/api/v1/commands.run", s.authed(s.handleCommandsRun))
	mux.HandleFunc("/api/v1/users.info", s.authed(s.handleUserInfo))
	mux.HandleFunc("/api/v1/channels.info", s.authed(s.handleChannelInfo))
	for _, endpoint := range roomActionEndpoints {
		mux.HandleFunc("/api/v1/"+endpoint, s.authed(s.handleRoomAction))
	}
	mux.HandleFunc("/file-upload/", s.authed(s.handleFileUpload))
	mux.HandleFunc("/websocket", s.handleWebSocket)

	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// Close shuts the server and every open websocket down.
func (s *Server) Close() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.ws.Close()
	}
	s.Server.Close()
}

// ---- fixtures ---------------------------------------------------------------

// AddRoom registers a room. roomType is one of c, p, d.
func (s *Server) AddRoom(id, roomType, name string, extra map[string]any) {
	room := map[string]any{
		"_id":         id,
		"t":           roomType,
		"name":        name,
		"fname":       name,
		"_updatedAt":  isoNow(),
		"usersCount":  3,
		"topic":       "",
		"ro":          false,
		"archived":    false,
		"description": "",
	}
	for k, v := range extra {
		room[k] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rooms = append(s.rooms, room)
}

// AddSubscription registers the user's view of a room.
func (s *Server) AddSubscription(roomID, roomType, name string, unread, mentions int, lastSeen time.Time, extra map[string]any) {
	sub := map[string]any{
		"_id":           "sub-" + roomID,
		"rid":           roomID,
		"t":             roomType,
		"name":          name,
		"fname":         name,
		"open":          true,
		"alert":         unread > 0,
		"unread":        unread,
		"userMentions":  mentions,
		"groupMentions": 0,
		"f":             false,
		"_updatedAt":    isoNow(),
	}
	if !lastSeen.IsZero() {
		sub["ls"] = lastSeen.UTC().Format(time.RFC3339Nano)
	}
	for k, v := range extra {
		sub[k] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscriptions = append(s.subscriptions, sub)
}

// AddMembers registers who is in a room, as the members endpoints report them.
func (s *Server) AddMembers(roomID string, usernames ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, username := range usernames {
		s.members[roomID] = append(s.members[roomID], map[string]any{
			"_id":      "user-" + username,
			"username": username,
			"name":     strings.ToUpper(username[:1]) + username[1:],
			"status":   "online",
		})
	}
}

// AddMessage registers a stored message.
func (s *Server) AddMessage(id, roomID, username, text string, at time.Time, extra map[string]any) {
	msg := s.buildMessage(id, roomID, username, text, at, extra)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	if count, ok := msg["tcount"].(int); ok && count > 0 {
		s.threads = append(s.threads, msg)
	}
}

// PromoteToThreadParent marks an existing message as a thread parent, the way
// the server does once someone replies to it.
func (s *Server) PromoteToThreadParent(messageID string, count int, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msg := range s.messages {
		if msg["_id"] != messageID {
			continue
		}
		msg["tcount"] = count
		msg["tlm"] = at.UTC().Format(time.RFC3339Nano)
		for _, existing := range s.threads {
			if existing["_id"] == messageID {
				return
			}
		}
		s.threads = append(s.threads, msg)
		return
	}
}

func (s *Server) buildMessage(id, roomID, username, text string, at time.Time, extra map[string]any) map[string]any {
	msg := map[string]any{
		"_id":        id,
		"rid":        roomID,
		"msg":        text,
		"ts":         at.UTC().Format(time.RFC3339Nano),
		"_updatedAt": at.UTC().Format(time.RFC3339Nano),
		"u": map[string]any{
			"_id":      "user-" + username,
			"username": username,
			"name":     strings.ToUpper(username[:1]) + username[1:],
		},
	}
	for k, v := range extra {
		msg[k] = v
	}
	return msg
}

// ---- realtime pushes --------------------------------------------------------

// PushMessage broadcasts a new message on stream-room-messages, using the
// EJSON date encoding a real server sends over DDP.
func (s *Server) PushMessage(id, roomID, username, text string, at time.Time, extra map[string]any) {
	msg := map[string]any{
		"_id":        id,
		"rid":        roomID,
		"msg":        text,
		"ts":         map[string]any{"$date": at.UTC().UnixMilli()},
		"_updatedAt": map[string]any{"$date": at.UTC().UnixMilli()},
		"u": map[string]any{
			"_id":      "user-" + username,
			"username": username,
			"name":     username,
		},
	}
	for k, v := range extra {
		msg[k] = v
	}

	s.mu.Lock()
	s.messages = append(s.messages, s.buildMessage(id, roomID, username, text, at, extra))
	s.mu.Unlock()

	s.broadcast(map[string]any{
		"msg":        "changed",
		"collection": "stream-room-messages",
		"id":         "id",
		"fields":     map[string]any{"eventName": roomID, "args": []any{msg}},
	})
}

// PushTyping broadcasts a typing notification in the modern user-activity shape.
func (s *Server) PushTyping(roomID, username string, typing bool) {
	activities := []any{}
	if typing {
		activities = append(activities, "user-typing")
	}
	s.broadcast(map[string]any{
		"msg":        "changed",
		"collection": "stream-notify-room",
		"id":         "id",
		"fields": map[string]any{
			"eventName": roomID + "/user-activity",
			"args":      []any{username, activities, map[string]any{}},
		},
	})
}

// PushLegacyTyping broadcasts the older [username, bool] typing shape.
func (s *Server) PushLegacyTyping(roomID, username string, typing bool) {
	s.broadcast(map[string]any{
		"msg":        "changed",
		"collection": "stream-notify-room",
		"id":         "id",
		"fields": map[string]any{
			"eventName": roomID + "/typing",
			"args":      []any{username, typing},
		},
	})
}

// PushSubscription broadcasts changed unread counts.
func (s *Server) PushSubscription(roomID string, unread, mentions int) {
	s.broadcast(map[string]any{
		"msg":        "changed",
		"collection": "stream-notify-user",
		"id":         "id",
		"fields": map[string]any{
			"eventName": UserID + "/subscriptions-changed",
			"args": []any{"updated", map[string]any{
				"_id":           "sub-" + roomID,
				"rid":           roomID,
				"unread":        unread,
				"userMentions":  mentions,
				"groupMentions": 0,
				"alert":         unread > 0,
				"open":          true,
				"t":             "c",
				"_updatedAt":    map[string]any{"$date": time.Now().UnixMilli()},
			}},
		},
	})
}

func (s *Server) broadcast(frame map[string]any) {
	payload, err := json.Marshal(frame)
	if err != nil {
		s.t.Errorf("fakerc: encode frame: %v", err)
		return
	}
	s.mu.Lock()
	conns := append([]*wsConn(nil), s.conns...)
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.write(payload)
	}
}

// ConnCount reports how many websocket clients are connected.
func (s *Server) ConnCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Notifications returns the typing notifications the client sent.
func (s *Server) Notifications() []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Notification(nil), s.notifications...)
}

// SentMessages returns the messages the client posted.
func (s *Server) SentMessages() []SentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SentMessage(nil), s.sent...)
}

// EditedMessages returns the chat.update calls the client made, in order.
func (s *Server) EditedMessages() []EditedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]EditedMessage(nil), s.edited...)
}

// MemberPaths returns the members endpoints the client called, in order.
func (s *Server) MemberPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.memberPaths...)
}

// ReadRooms returns the rooms the client marked read.
func (s *Server) ReadRooms() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.readRooms...)
}

// UnreadMarks returns the mark-unread calls the client made, in order.
func (s *Server) UnreadMarks() []UnreadMark {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]UnreadMark(nil), s.unreadMarks...)
}

func isoNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }
