package rocket

import (
	"encoding/json"
	"fmt"
)

// Wire format for DDP, the Meteor protocol Rocket.Chat speaks over
// /websocket. We only implement the subset needed for realtime push.
//
// Reference frames:
//
//	→ {"msg":"connect","version":"1","support":["1"]}
//	← {"msg":"connected","session":"..."}
//	→ {"msg":"method","method":"login","id":"1","params":[{"resume":"tok"}]}
//	→ {"msg":"sub","id":"2","name":"stream-room-messages","params":["rid",false]}
//	← {"msg":"changed","collection":"stream-room-messages",
//	   "fields":{"eventName":"rid","args":[{...message...}]}}
type ddpFrame struct {
	Msg        string          `json:"msg"`
	ID         string          `json:"id,omitempty"`
	Session    string          `json:"session,omitempty"`
	Version    string          `json:"version,omitempty"`
	Collection string          `json:"collection,omitempty"`
	Fields     *ddpFields      `json:"fields,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      *ddpError       `json:"error,omitempty"`
	Subs       []string        `json:"subs,omitempty"`
	Methods    []string        `json:"methods,omitempty"`
}

type ddpFields struct {
	EventName string            `json:"eventName"`
	Args      []json.RawMessage `json:"args"`
}

type ddpError struct {
	Code      any    `json:"error"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	ErrorType string `json:"errorType"`
}

func (e *ddpError) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason != "" {
		return fmt.Sprintf("rocket/ddp: %s", e.Reason)
	}
	if e.Message != "" {
		return fmt.Sprintf("rocket/ddp: %s", e.Message)
	}
	return fmt.Sprintf("rocket/ddp: %v", e.Code)
}

// ddpResult is what a method call resolves to.
type ddpResult struct {
	value json.RawMessage
	err   error
}

// Collections and stream names we subscribe to.
const (
	streamRoomMessages = "stream-room-messages"
	streamNotifyRoom   = "stream-notify-room"
	streamNotifyUser   = "stream-notify-user"

	// allMessages is the pseudo-room that pushes every message the user is
	// subscribed to, so unread counts stay live without a sub per room.
	allMessages = "__my_messages__"
)
