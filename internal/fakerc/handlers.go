package fakerc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// ---- REST -------------------------------------------------------------------

func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != AuthToken || r.Header.Get("X-User-Id") != UserID {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"status":  "error",
				"message": "You must be logged in to do this.",
				"success": false,
			})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
		Code     string `json:"code"`
		Resume   string `json:"resume"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	code := body.Code
	if code == "" {
		code = r.Header.Get("X-2fa-Code")
	}

	switch {
	case body.Resume != "":
		if body.Resume != AuthToken {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"status": "error", "message": "Unauthorized", "success": false,
			})
			return
		}
	case body.User == "" || body.Password != Password:
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"status": "error", "message": "Unauthorized", "success": false,
		})
		return
	case s.RequireTOTP && code == "":
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"success": false,
			"error": map[string]any{
				"error":  "totp-required",
				"reason": "TOTP Required",
			},
		})
		return
	case s.RequireTOTP && code != TOTPCode:
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"success": false,
			"error": map[string]any{
				"error":  "totp-invalid",
				"reason": "TOTP Invalid",
			},
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data": map[string]any{
			"authToken": AuthToken,
			"userId":    UserID,
			"me":        meBody(),
		},
	})
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request) {
	body := meBody()
	body["success"] = true
	writeJSON(w, http.StatusOK, body)
}

func meBody() map[string]any {
	return map[string]any{
		"_id":      UserID,
		"username": Username,
		"name":     "Test Tester",
		"emails":   []any{map[string]any{"address": "tester@example.com", "verified": true}},
	}
}

func (s *Server) handleSubscriptions(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	subs := append([]map[string]any(nil), s.subscriptions...)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"update": subs, "remove": []any{}, "success": true})
}

func (s *Server) handleRooms(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	rooms := append([]map[string]any(nil), s.rooms...)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"update": rooms, "remove": []any{}, "success": true})
}

func (s *Server) handleRoomInfo(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("roomId")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, room := range s.rooms {
		if room["_id"] == roomID {
			writeJSON(w, http.StatusOK, map[string]any{"room": room, "success": true})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "room not found"})
}

// handleMembers returns the room's roster, or an empty list for a room no
// members were registered for.
func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("roomId")
	s.mu.Lock()
	members := append([]map[string]any(nil), s.members[roomID]...)
	// Recorded so tests can check the client picked the right endpoint for the
	// room type; a real server rejects the wrong one.
	s.memberPaths = append(s.memberPaths, r.URL.Path)
	s.mu.Unlock()
	if members == nil {
		members = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"members": members,
		"count":   len(members),
		"total":   len(members),
		"success": true,
	})
}

// handleHistory returns messages newest-first, honouring count and latest the
// way the real endpoints do.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	roomID := query.Get("roomId")
	count, _ := strconv.Atoi(query.Get("count"))
	if count <= 0 {
		count = 50
	}
	var latest time.Time
	if raw := query.Get("latest"); raw != "" {
		latest, _ = time.Parse(time.RFC3339Nano, raw)
	}

	s.mu.Lock()
	var matches []map[string]any
	for _, msg := range s.messages {
		if msg["rid"] != roomID {
			continue
		}
		at := messageTime(msg)
		if !latest.IsZero() && !at.Before(latest) {
			continue
		}
		matches = append(matches, msg)
	}
	s.mu.Unlock()

	sort.Slice(matches, func(i, j int) bool {
		return messageTime(matches[i]).After(messageTime(matches[j]))
	})
	if len(matches) > count {
		matches = matches[:count]
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": matches, "success": true})
}

func (s *Server) handleSyncMessages(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	roomID := query.Get("roomId")
	since, _ := time.Parse(time.RFC3339Nano, query.Get("lastUpdate"))

	s.mu.Lock()
	var updated []map[string]any
	for _, msg := range s.messages {
		if msg["rid"] != roomID {
			continue
		}
		if messageTime(msg).After(since) {
			updated = append(updated, msg)
		}
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"result":  map[string]any{"updated": updated, "deleted": []any{}},
		"success": true,
	})
}

func (s *Server) handleThreadsList(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("rid")
	s.mu.Lock()
	var threads []map[string]any
	for _, thread := range s.threads {
		if thread["rid"] == roomID {
			threads = append(threads, thread)
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"threads": threads, "total": len(threads), "success": true,
	})
}

func (s *Server) handleThreadMessages(w http.ResponseWriter, r *http.Request) {
	threadID := r.URL.Query().Get("tmid")
	s.mu.Lock()
	var replies []map[string]any
	for _, msg := range s.messages {
		if msg["tmid"] == threadID {
			replies = append(replies, msg)
		}
	}
	s.mu.Unlock()
	sort.Slice(replies, func(i, j int) bool {
		return messageTime(replies[i]).Before(messageTime(replies[j]))
	})
	writeJSON(w, http.StatusOK, map[string]any{"messages": replies, "success": true})
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	messageID := r.URL.Query().Get("msgId")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msg := range s.messages {
		if msg["_id"] == messageID {
			writeJSON(w, http.StatusOK, map[string]any{"message": msg, "success": true})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "message not found"})
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message struct {
			RoomID   string `json:"rid"`
			Text     string `json:"msg"`
			ThreadID string `json:"tmid"`
		} `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "bad body"})
		return
	}

	s.mu.Lock()
	s.nextID++
	id := "sent-" + strconv.Itoa(s.nextID)
	s.sent = append(s.sent, SentMessage{
		RoomID:   body.Message.RoomID,
		Text:     body.Message.Text,
		ThreadID: body.Message.ThreadID,
	})
	extra := map[string]any{}
	if body.Message.ThreadID != "" {
		extra["tmid"] = body.Message.ThreadID
	}
	msg := s.buildMessage(id, body.Message.RoomID, Username, body.Message.Text, time.Now(), extra)
	msg["u"] = map[string]any{"_id": UserID, "username": Username, "name": "Test Tester"}
	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"message": msg, "success": true})
}

func (s *Server) handleUpdateMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID    string `json:"roomId"`
		MessageID string `json:"msgId"`
		Text      string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "bad body"})
		return
	}

	s.mu.Lock()
	if s.RejectEdit {
		s.mu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success":   false,
			"error":     "Message editing not allowed [error-message-editing-not-allowed]",
			"errorType": "error-message-editing-not-allowed",
		})
		return
	}
	s.edited = append(s.edited, EditedMessage{
		RoomID:    body.RoomID,
		MessageID: body.MessageID,
		Text:      body.Text,
	})
	var updated map[string]any
	for _, msg := range s.messages {
		if msg["_id"] == body.MessageID {
			msg["msg"] = body.Text
			// A real server stamps editedAt and bumps _updatedAt, which is what
			// makes the timeline print "(edited)" and chat.syncMessages notice.
			msg["editedAt"] = isoNow()
			msg["editedBy"] = map[string]any{"_id": UserID, "username": Username}
			msg["_updatedAt"] = isoNow()
			updated = msg
			break
		}
	}
	s.mu.Unlock()

	if updated == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success":   false,
			"error":     "Invalid message [error-invalid-message]",
			"errorType": "error-invalid-message",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": updated, "success": true})
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID string `json:"rid"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	s.readRooms = append(s.readRooms, body.RoomID)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleMarkUnread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID             string `json:"roomId"`
		FirstUnreadMessage struct {
			ID string `json:"_id"`
		} `json:"firstUnreadMessage"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	s.mu.Lock()
	reject := s.RejectUnread
	if !reject {
		s.unreadMarks = append(s.unreadMarks, UnreadMark{
			RoomID:    body.RoomID,
			MessageID: body.FirstUnreadMessage.ID,
		})
	}
	s.mu.Unlock()

	if reject {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success":   false,
			"error":     "Not allowed [error-action-not-allowed]",
			"errorType": "error-action-not-allowed",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---- DDP --------------------------------------------------------------------

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	raw, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := &wsConn{ws: raw}
	s.mu.Lock()
	s.conns = append(s.conns, conn)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		for i, existing := range s.conns {
			if existing == conn {
				s.conns = append(s.conns[:i], s.conns[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		_ = conn.ws.Close()
	}()

	for {
		_, payload, err := conn.ws.ReadMessage()
		if err != nil {
			return
		}
		var frame struct {
			Msg    string          `json:"msg"`
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &frame); err != nil {
			continue
		}

		switch frame.Msg {
		case "connect":
			s.send(conn, map[string]any{"msg": "connected", "session": "test-session"})
		case "ping":
			s.send(conn, map[string]any{"msg": "pong"})
		case "pong":
			// nothing to do
		case "sub":
			s.send(conn, map[string]any{"msg": "ready", "subs": []string{frame.ID}})
		case "unsub":
			s.send(conn, map[string]any{"msg": "nosub", "id": frame.ID})
		case "method":
			s.handleMethod(conn, frame.ID, frame.Method, frame.Params)
		}
	}
}

func (s *Server) handleMethod(conn *wsConn, id, method string, rawParams json.RawMessage) {
	switch method {
	case "login":
		var params []struct {
			Resume string `json:"resume"`
		}
		_ = json.Unmarshal(rawParams, &params)
		if len(params) == 0 || params[0].Resume != AuthToken {
			s.send(conn, map[string]any{
				"msg": "result", "id": id,
				"error": map[string]any{"error": 403, "reason": "Invalid token"},
			})
			return
		}
		s.send(conn, map[string]any{
			"msg": "result", "id": id,
			"result": map[string]any{
				"id":           UserID,
				"token":        AuthToken,
				"tokenExpires": map[string]any{"$date": time.Now().Add(24 * time.Hour).UnixMilli()},
			},
		})

	case "stream-notify-room":
		s.recordNotification(rawParams)
		s.send(conn, map[string]any{"msg": "result", "id": id, "result": nil})

	default:
		s.send(conn, map[string]any{"msg": "result", "id": id, "result": nil})
	}
}

// recordNotification decodes both typing notification shapes the client emits.
func (s *Server) recordNotification(rawParams json.RawMessage) {
	var params []json.RawMessage
	if err := json.Unmarshal(rawParams, &params); err != nil || len(params) < 3 {
		return
	}
	var eventName, username string
	if json.Unmarshal(params[0], &eventName) != nil {
		return
	}
	if json.Unmarshal(params[1], &username) != nil {
		return
	}

	notification := Notification{EventName: eventName, Username: username}
	var legacyTyping bool
	if err := json.Unmarshal(params[2], &legacyTyping); err == nil {
		notification.Typing = legacyTyping
	} else {
		var activities []string
		if json.Unmarshal(params[2], &activities) != nil {
			return
		}
		for _, activity := range activities {
			if activity == "user-typing" {
				notification.Typing = true
			}
		}
	}

	s.mu.Lock()
	s.notifications = append(s.notifications, notification)
	s.mu.Unlock()
}

func (s *Server) send(conn *wsConn, frame map[string]any) {
	payload, err := json.Marshal(frame)
	if err != nil {
		return
	}
	_ = conn.write(payload)
}

func messageTime(msg map[string]any) time.Time {
	raw, ok := msg["ts"].(string)
	if !ok {
		return time.Time{}
	}
	at, _ := time.Parse(time.RFC3339Nano, raw)
	return at
}

func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ---- uploads ----------------------------------------------------------------

// handleRoomsMedia is the first half of the modern upload flow: it takes the
// bytes and hands back a file id, posting nothing to the room yet.
func (s *Server) handleRoomsMedia(w http.ResponseWriter, r *http.Request) {
	if s.NoMediaRoute {
		// What a server predating this route answers, and the signal a client
		// uses to fall back to rooms.upload.
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false, "error": "unknown API endpoint",
		})
		return
	}
	roomID := strings.TrimPrefix(r.URL.Path, "/api/v1/rooms.media/")
	upload, err := readUpload(r, roomID, "media")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if s.RejectUpload {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": "error-invalid-file-type", "errorType": "error-invalid-file-type",
		})
		return
	}

	s.mu.Lock()
	s.nextID++
	fileID := "file-" + strconv.Itoa(s.nextID)
	// Held, not recorded: the upload only becomes real when it is confirmed, so
	// an abandoned first half must not show up as a sent file.
	s.pendingMedia[fileID] = upload
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"file":    map[string]any{"_id": fileID, "url": "/file-upload/" + fileID + "/" + upload.Filename},
	})
}

// handleRoomsMediaConfirm is the second half: it turns a held file into a
// message.
func (s *Server) handleRoomsMediaConfirm(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/rooms.mediaConfirm/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "bad path"})
		return
	}
	roomID, fileID := parts[0], parts[1]

	var body struct {
		Text     string `json:"msg"`
		ThreadID string `json:"tmid"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	s.mu.Lock()
	upload, ok := s.pendingMedia[fileID]
	if !ok {
		s.mu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": "error-invalid-file", "errorType": "error-invalid-file",
		})
		return
	}
	delete(s.pendingMedia, fileID)
	upload.Text, upload.ThreadID = body.Text, body.ThreadID
	msg := s.recordUpload(roomID, fileID, upload)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": msg})
}

// handleRoomsUpload is the older single-request flow, where the message rides
// along as form fields next to the file.
func (s *Server) handleRoomsUpload(w http.ResponseWriter, r *http.Request) {
	roomID := strings.TrimPrefix(r.URL.Path, "/api/v1/rooms.upload/")
	upload, err := readUpload(r, roomID, "upload")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if s.RejectUpload {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": "error-invalid-file-type", "errorType": "error-invalid-file-type",
		})
		return
	}
	upload.Text = r.FormValue("msg")
	upload.ThreadID = r.FormValue("tmid")

	s.mu.Lock()
	s.nextID++
	fileID := "file-" + strconv.Itoa(s.nextID)
	msg := s.recordUpload(roomID, fileID, upload)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": msg})
}

// readUpload pulls the file part out of a multipart request, keeping the
// Content-Type the client declared on it rather than re-sniffing the bytes.
func readUpload(r *http.Request, roomID, route string) (Upload, error) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return Upload{}, fmt.Errorf("bad multipart body: %w", err)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return Upload{}, fmt.Errorf("no file part: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return Upload{}, fmt.Errorf("read file part: %w", err)
	}
	return Upload{
		RoomID:   roomID,
		Route:    route,
		Filename: header.Filename,
		MIME:     header.Header.Get("Content-Type"),
		Bytes:    data,
	}, nil
}

// recordUpload files the upload away and builds the message a real server
// would create for it, attachment and all. The caller holds s.mu.
func (s *Server) recordUpload(roomID, fileID string, upload Upload) map[string]any {
	upload.RoomID = roomID
	s.uploads = append(s.uploads, upload)

	s.nextID++
	link := "/file-upload/" + fileID + "/" + upload.Filename
	attachment := map[string]any{
		"title":      upload.Filename,
		"title_link": link,
		"type":       "file",
	}
	// Real servers key an image attachment off image_url rather than title_link,
	// which is what tells a client it can be drawn rather than only downloaded.
	if strings.HasPrefix(upload.MIME, "image/") {
		attachment["image_url"] = link
		attachment["image_type"] = upload.MIME
	}
	extra := map[string]any{
		"attachments": []any{attachment},
		"file":        map[string]any{"_id": fileID, "name": upload.Filename, "type": upload.MIME},
	}
	if upload.ThreadID != "" {
		extra["tmid"] = upload.ThreadID
	}

	msg := s.buildMessage("sent-"+strconv.Itoa(s.nextID), roomID, Username, upload.Text, time.Now(), extra)
	msg["u"] = map[string]any{"_id": UserID, "username": Username, "name": "Test Tester"}
	s.messages = append(s.messages, msg)
	return msg
}

// Uploads is every file the client has posted, in order.
func (s *Server) Uploads() []Upload {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Upload(nil), s.uploads...)
}

// handleFileUpload serves an attachment. Like a real server it demands
// credentials: uploads are not public, which is the whole reason a client
// cannot simply hand the URL to something else.
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.fileRequests++
	s.mu.Unlock()

	w.Header().Set("Content-Type", "image/png")
	w.Write(FilePNG)
}

// FileRequests is how many times an attachment has actually been fetched, so a
// test can tell a cache hit from a round trip.
func (s *Server) FileRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fileRequests
}
