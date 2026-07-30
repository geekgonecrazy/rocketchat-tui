package rocket

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// HistoryQuery describes a page of room history. Rocket.Chat pages backwards:
// Before is the exclusive upper bound, so paging older means passing the
// oldest timestamp you already hold.
type HistoryQuery struct {
	RoomID   string
	RoomType string
	Count    int
	Before   time.Time // exclusive; zero means "latest"
	After    time.Time // exclusive lower bound; zero means unbounded
}

// historyEndpoint maps a room type onto its type-specific history endpoint.
// Teams and discussions are ordinary c/p rooms, so they need no special case.
func historyEndpoint(roomType string) (string, error) {
	switch roomType {
	case RoomTypeChannel:
		return "channels.history", nil
	case RoomTypePrivate:
		return "groups.history", nil
	case RoomTypeDirect:
		return "im.history", nil
	case RoomTypeLive:
		return "channels.history", nil
	case "":
		return "", fmt.Errorf("rocket: room type is required to load history")
	default:
		return "", fmt.Errorf("rocket: unsupported room type %q", roomType)
	}
}

// History returns a page of messages, newest first.
func (c *Client) History(ctx context.Context, q HistoryQuery) ([]Message, error) {
	endpoint, err := historyEndpoint(q.RoomType)
	if err != nil {
		return nil, err
	}
	if q.Count <= 0 {
		q.Count = 50
	}
	query := url.Values{
		"roomId": {q.RoomID},
		"count":  {strconv.Itoa(q.Count)},
	}
	if !q.Before.IsZero() {
		query.Set("latest", q.Before.UTC().Format(time.RFC3339Nano))
	}
	if !q.After.IsZero() {
		query.Set("oldest", q.After.UTC().Format(time.RFC3339Nano))
	}
	query.Set("inclusive", "false")

	var resp struct {
		Messages []Message `json:"messages"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: endpoint, query: query}, &resp); err != nil {
		return nil, err
	}
	return resp.Messages, nil
}

// SyncMessages returns messages created, changed, or deleted since a point in
// time. This is the cheap catch-up call after the app has been offline.
func (c *Client) SyncMessages(ctx context.Context, roomID string, since time.Time) (updated, deleted []Message, err error) {
	if since.IsZero() {
		return nil, nil, fmt.Errorf("rocket: sync requires a since timestamp")
	}
	query := url.Values{
		"roomId":     {roomID},
		"lastUpdate": {since.UTC().Format(time.RFC3339Nano)},
	}
	var resp struct {
		Result struct {
			Updated []Message `json:"updated"`
			Deleted []Message `json:"deleted"`
		} `json:"result"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: "chat.syncMessages", query: query}, &resp); err != nil {
		return nil, nil, err
	}
	return resp.Result.Updated, resp.Result.Deleted, nil
}

// ThreadsList returns the thread-parent messages in a room, newest activity
// first.
func (c *Client) ThreadsList(ctx context.Context, roomID string, count, offset int) ([]Message, int, error) {
	if count <= 0 {
		count = 50
	}
	// No "type" filter: the parameter only accepts a specific set of values
	// (and rejects "all" outright on 8.x), whereas omitting it returns every
	// thread in the room, which is what the thread list wants.
	query := url.Values{
		"rid":    {roomID},
		"count":  {strconv.Itoa(count)},
		"offset": {strconv.Itoa(offset)},
	}
	var resp struct {
		Threads []Message `json:"threads"`
		Total   int       `json:"total"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: "chat.getThreadsList", query: query}, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Threads, resp.Total, nil
}

// ThreadMessages returns the replies inside one thread, oldest first.
func (c *Client) ThreadMessages(ctx context.Context, threadID string, count, offset int) ([]Message, error) {
	if count <= 0 {
		count = 100
	}
	query := url.Values{
		"tmid":   {threadID},
		"count":  {strconv.Itoa(count)},
		"offset": {strconv.Itoa(offset)},
		"sort":   {`{"ts":1}`},
	}
	var resp struct {
		Messages []Message `json:"messages"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: "chat.getThreadMessages", query: query}, &resp); err != nil {
		return nil, err
	}
	return resp.Messages, nil
}

// GetMessage fetches one message by id, used to resolve a thread parent that is
// older than our cached history.
func (c *Client) GetMessage(ctx context.Context, messageID string) (Message, error) {
	query := url.Values{"msgId": {messageID}}
	var resp struct {
		Message Message `json:"message"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: "chat.getMessage", query: query}, &resp); err != nil {
		return Message{}, err
	}
	return resp.Message, nil
}

// SendOptions describes an outgoing message. Set ThreadID to reply in a thread;
// set AlsoSendToChannel to mirror the reply into the main timeline, matching the
// web client's checkbox.
type SendOptions struct {
	RoomID            string
	Text              string
	ThreadID          string
	AlsoSendToChannel bool
}

// Send posts a message and returns the stored message.
func (c *Client) Send(ctx context.Context, opts SendOptions) (Message, error) {
	if opts.RoomID == "" {
		return Message{}, fmt.Errorf("rocket: send requires a room id")
	}
	payload := map[string]any{
		"rid": opts.RoomID,
		"msg": opts.Text,
	}
	if opts.ThreadID != "" {
		payload["tmid"] = opts.ThreadID
		if opts.AlsoSendToChannel {
			payload["tshow"] = true
		}
	}
	var resp struct {
		Message Message `json:"message"`
	}
	err := c.do(ctx, request{
		method:   "POST",
		endpoint: "chat.sendMessage",
		body:     map[string]any{"message": payload},
	}, &resp)
	if err != nil {
		return Message{}, err
	}
	return resp.Message, nil
}
