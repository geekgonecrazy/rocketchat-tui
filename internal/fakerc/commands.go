package fakerc

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// RanCommand is a commands.run call the client made.
type RanCommand struct {
	Command  string
	Params   string
	RoomID   string
	ThreadID string
	// TriggerID is the correlation id an app command would answer a modal with.
	// A client that omits it breaks app commands, so tests need to see it.
	TriggerID string
}

// RoomAction is one of the room-level REST calls the slash command fallbacks
// make: leaving, hiding, joining, inviting, and so on. One type covers them all
// because what a test cares about is which endpoint was called with what, and
// the bodies differ by no more than a field.
type RoomAction struct {
	Endpoint string // e.g. "channels.leave"
	RoomID   string
	UserID   string
	Topic    string
}

// AddCommand registers a slash command for commands.list to report.
func (s *Server) AddCommand(name, params, description string, clientOnly bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, map[string]any{
		"command":         name,
		"params":          params,
		"description":     description,
		"clientOnly":      clientOnly,
		"providesPreview": false,
	})
}

// RanCommands returns the commands.run calls the client made, in order.
func (s *Server) RanCommands() []RanCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RanCommand(nil), s.ranCommands...)
}

// RoomActions returns the room-level calls the client made, in order.
func (s *Server) RoomActions() []RoomAction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RoomAction(nil), s.roomActions...)
}

// handleCommandsList reports the registered commands one page at a time, the
// way the real endpoint does — a client that reads only the first page must
// come up short here too.
func (s *Server) handleCommandsList(w http.ResponseWriter, r *http.Request) {
	if s.RejectCommandList {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"success":   false,
			"error":     "unauthorized",
			"errorType": "error-unauthorized",
		})
		return
	}

	count, _ := strconv.Atoi(r.URL.Query().Get("count"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if count <= 0 {
		count = 50
	}

	s.mu.Lock()
	total := len(s.commands)
	page := []map[string]any{}
	if offset < total {
		page = append(page, s.commands[offset:min(offset+count, total)]...)
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"commands": page,
		"offset":   offset,
		"count":    len(page),
		"total":    total,
		"success":  true,
	})
}

// handleCommandsRun records the call and reports success. A real server would
// also do whatever the command means; nothing here does, because what a client
// has to get right is the request.
func (s *Server) handleCommandsRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command   string `json:"command"`
		Params    string `json:"params"`
		RoomID    string `json:"roomId"`
		ThreadID  string `json:"tmid"`
		TriggerID string `json:"triggerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "bad body"})
		return
	}

	s.mu.Lock()
	known := false
	for _, command := range s.commands {
		if command["command"] == body.Command {
			known = true
			break
		}
	}
	if known {
		s.ranCommands = append(s.ranCommands, RanCommand{
			Command:   body.Command,
			Params:    body.Params,
			RoomID:    body.RoomID,
			ThreadID:  body.ThreadID,
			TriggerID: body.TriggerID,
		})
	}
	s.mu.Unlock()

	if !known {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success":   false,
			"error":     "The command is invalid",
			"errorType": "error-invalid-command",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// roomActionEndpoints are the room-level routes the fallbacks use. They all take
// a room id and answer {"success": true}, so one handler serves the lot.
var roomActionEndpoints = []string{
	"channels.leave", "groups.leave",
	"channels.close", "groups.close", "im.close",
	"channels.join",
	"channels.kick", "groups.kick",
	"channels.invite", "groups.invite",
	"channels.setTopic", "groups.setTopic", "im.setTopic",
	"channels.archive", "groups.archive",
	"channels.unarchive", "groups.unarchive",
}

func (s *Server) handleRoomAction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID string `json:"roomId"`
		UserID string `json:"userId"`
		Topic  string `json:"topic"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	s.mu.Lock()
	s.roomActions = append(s.roomActions, RoomAction{
		Endpoint: strings.TrimPrefix(r.URL.Path, "/api/v1/"),
		RoomID:   body.RoomID,
		UserID:   body.UserID,
		Topic:    body.Topic,
	})
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleUserInfo resolves a username to a user id, which is what the invite and
// kick fallbacks need before they can call anything.
func (s *Server) handleUserInfo(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	if username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "no username"})
		return
	}
	for _, unknown := range s.NoSuchUsers {
		if unknown == username {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"success":   false,
				"error":     "User not found.",
				"errorType": "error-invalid-user",
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"_id":      "user-" + username,
			"username": username,
			"name":     strings.ToUpper(username[:1]) + username[1:],
		},
		"success": true,
	})
}

// handleChannelInfo looks a channel up by slug, the way /join has to before it
// can join anything.
func (s *Server) handleChannelInfo(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("roomName")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, room := range s.rooms {
		if room["name"] == name {
			writeJSON(w, http.StatusOK, map[string]any{"channel": room, "success": true})
			return
		}
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"success":   false,
		"error":     "The required \"roomName\" param provided does not match any channel",
		"errorType": "error-room-not-found",
	})
}
