// Package rocket is a small, focused Rocket.Chat client.
//
// It deliberately covers only what a chat TUI needs: REST for state that is
// fetched and paginated (login, rooms, history, threads), and DDP over
// WebSocket purely for realtime push (new messages, unread counts, typing).
package rocket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Room types as reported by Rocket.Chat in the `t` field.
const (
	RoomTypeChannel = "c" // public channel (also team main room when TeamMain)
	RoomTypePrivate = "p" // private group (also private team / discussion)
	RoomTypeDirect  = "d" // direct message
	RoomTypeLive    = "l" // omnichannel / livechat
)

// Timestamp handles the two date encodings Rocket.Chat uses: RFC3339 strings
// over REST, and EJSON `{"$date": millis}` over DDP.
type Timestamp struct {
	time.Time
}

func NewTimestamp(t time.Time) Timestamp { return Timestamp{Time: t} }

func (t *Timestamp) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "" || s == "null":
		return nil
	case s[0] == '{':
		var wrapper struct {
			Date json.Number `json:"$date"`
		}
		if err := json.Unmarshal(b, &wrapper); err != nil {
			return err
		}
		ms, err := wrapper.Date.Int64()
		if err != nil {
			return fmt.Errorf("rocket: bad $date %q: %w", wrapper.Date, err)
		}
		t.Time = time.UnixMilli(ms).UTC()
		return nil
	case s[0] == '"':
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		if str == "" {
			return nil
		}
		parsed, err := time.Parse(time.RFC3339Nano, str)
		if err != nil {
			return fmt.Errorf("rocket: bad timestamp %q: %w", str, err)
		}
		t.Time = parsed.UTC()
		return nil
	default:
		ms, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("rocket: bad timestamp %q: %w", s, err)
		}
		t.Time = time.UnixMilli(ms).UTC()
		return nil
	}
}

func (t Timestamp) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.UTC().Format(time.RFC3339Nano))
}

// User is the trimmed user shape embedded in messages and mentions.
type User struct {
	ID       string `json:"_id"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
}

// Me is the authenticated account.
type Me struct {
	ID       string `json:"_id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"-"`
	Emails   []struct {
		Address  string `json:"address"`
		Verified bool   `json:"verified"`
	} `json:"emails"`
	UTCOffset float64 `json:"utcOffset"`
}

// Room is server-side room metadata.
type Room struct {
	ID           string     `json:"_id"`
	Type         string     `json:"t"`
	Name         string     `json:"name"`
	DisplayName  string     `json:"fname"`
	Topic        string     `json:"topic"`
	Description  string     `json:"description"`
	TeamID       string     `json:"teamId"`
	TeamMain     bool       `json:"teamMain"`
	ParentRoomID string     `json:"prid"` // set on discussions
	UpdatedAt    Timestamp  `json:"_updatedAt"`
	LastMessage  *Timestamp `json:"lm"`
	UserCount    int        `json:"usersCount"`
	ReadOnly     bool       `json:"ro"`
	Archived     bool       `json:"archived"`
	Broadcast    bool       `json:"broadcast"`
	Usernames    []string   `json:"usernames"`
}

// Subscription is the per-user view of a room: unread counts, mentions, and
// the last-seen marker that drives the "new messages" divider.
type Subscription struct {
	ID            string     `json:"_id"`
	RoomID        string     `json:"rid"`
	Name          string     `json:"name"`
	DisplayName   string     `json:"fname"`
	Type          string     `json:"t"`
	Open          bool       `json:"open"`
	Alert         bool       `json:"alert"`
	Unread        int        `json:"unread"`
	UserMentions  int        `json:"userMentions"`
	GroupMentions int        `json:"groupMentions"`
	LastSeen      *Timestamp `json:"ls"`
	UpdatedAt     Timestamp  `json:"_updatedAt"`
	Favorite      bool       `json:"f"`
	TeamID        string     `json:"teamId"`
	TeamMain      bool       `json:"teamMain"`
	ParentRoomID  string     `json:"prid"`
	Roles         []string   `json:"roles"`
}

// Reaction is one emoji plus the usernames that used it.
type Reaction struct {
	Usernames []string `json:"usernames"`
}

// Attachment is rendered as a compact one-liner in the TUI; we keep only the
// fields worth showing in a terminal.
type Attachment struct {
	Title       string `json:"title"`
	TitleLink   string `json:"title_link"`
	Text        string `json:"text"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url"`
	AuthorName  string `json:"author_name"`
	Type        string `json:"type"`
}

// Message is a single chat message. Thread replies carry ThreadParentID;
// thread parents carry ThreadCount > 0.
type Message struct {
	ID             string              `json:"_id"`
	RoomID         string              `json:"rid"`
	Msg            string              `json:"msg"`
	Timestamp      Timestamp           `json:"ts"`
	UpdatedAt      Timestamp           `json:"_updatedAt"`
	EditedAt       *Timestamp          `json:"editedAt"`
	User           User                `json:"u"`
	Type           string              `json:"t"` // non-empty for system messages
	ThreadParentID string              `json:"tmid"`
	ThreadCount    int                 `json:"tcount"`
	ThreadLastAt   *Timestamp          `json:"tlm"`
	ShowInParent   bool                `json:"tshow"`
	Mentions       []User              `json:"mentions"`
	Reactions      map[string]Reaction `json:"reactions"`
	Attachments    []Attachment        `json:"attachments"`
	Replies        []string            `json:"replies"`
	Groupable      *bool               `json:"groupable"`
}

// IsSystem reports whether the message is a join/leave/topic-change style event
// rather than user-authored text.
func (m Message) IsSystem() bool { return m.Type != "" }

// IsThreadReply reports whether the message lives inside a thread.
func (m Message) IsThreadReply() bool { return m.ThreadParentID != "" }

// APIError is a non-2xx response from the REST API.
type APIError struct {
	StatusCode int
	ErrorType  string
	Message    string
	Endpoint   string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if e.ErrorType != "" {
		return fmt.Sprintf("rocket: %s: %s (%s)", e.Endpoint, msg, e.ErrorType)
	}
	return fmt.Sprintf("rocket: %s: %s", e.Endpoint, msg)
}

// TOTPRequired reports whether the server rejected a login pending a 2FA code.
func (e *APIError) TOTPRequired() bool {
	return e.ErrorType == "totp-required" || e.ErrorType == "totp-invalid" ||
		strings.Contains(strings.ToLower(e.Message), "totp")
}

// Unauthorized reports whether stored credentials are no longer valid.
func (e *APIError) Unauthorized() bool {
	return e.StatusCode == 401 && !e.TOTPRequired()
}
