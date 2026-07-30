package rocket

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Subscriptions returns every subscription for the logged-in user. Pass a
// non-zero since to fetch only what changed.
func (c *Client) Subscriptions(ctx context.Context, since time.Time) ([]Subscription, error) {
	query := url.Values{}
	if !since.IsZero() {
		query.Set("updatedSince", since.UTC().Format(time.RFC3339Nano))
	}
	var resp struct {
		Update []Subscription `json:"update"`
		Remove []Subscription `json:"remove"`
		// The non-delta form returns a flat list instead.
		Subscriptions []Subscription `json:"subscriptions"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: "subscriptions.get", query: query}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Subscriptions) > 0 {
		return resp.Subscriptions, nil
	}
	return resp.Update, nil
}

// Rooms returns room metadata for every room the user belongs to. Pass a
// non-zero since to fetch only what changed.
func (c *Client) Rooms(ctx context.Context, since time.Time) ([]Room, error) {
	query := url.Values{}
	if !since.IsZero() {
		query.Set("updatedSince", since.UTC().Format(time.RFC3339Nano))
	}
	var resp struct {
		Update []Room `json:"update"`
		Remove []Room `json:"remove"`
		Rooms  []Room `json:"rooms"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: "rooms.get", query: query}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Rooms) > 0 {
		return resp.Rooms, nil
	}
	return resp.Update, nil
}

// RoomInfo fetches a single room, used when realtime hands us a room id we have
// never seen (a new DM, or a discussion someone just created).
func (c *Client) RoomInfo(ctx context.Context, roomID string) (Room, error) {
	query := url.Values{"roomId": {roomID}}
	var resp struct {
		Room Room `json:"room"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: "rooms.info", query: query}, &resp); err != nil {
		return Room{}, err
	}
	return resp.Room, nil
}

// MarkRead clears the unread counter for a room server-side.
func (c *Client) MarkRead(ctx context.Context, roomID string) error {
	return c.do(ctx, request{
		method:   "POST",
		endpoint: "subscriptions.read",
		body:     map[string]any{"rid": roomID},
	}, nil)
}

// MarkUnread marks a whole room unread.
//
// The endpoint also documents a firstUnreadMessage form for marking unread from
// a specific message, but the server rejects it with error-action-not-allowed
// (observed on 8.4 when the target message is the caller's own), so only the
// room-level form is exposed here.
func (c *Client) MarkUnread(ctx context.Context, roomID string) error {
	return c.do(ctx, request{
		method:   "POST",
		endpoint: "subscriptions.unread",
		body:     map[string]any{"roomId": roomID},
	}, nil)
}

// TeamChannels lists the channels belonging to a team's main room.
func (c *Client) TeamChannels(ctx context.Context, teamID string) ([]Room, error) {
	query := url.Values{"teamId": {teamID}, "count": {"100"}}
	var resp struct {
		Rooms []Room `json:"rooms"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: "teams.listRooms", query: query}, &resp); err != nil {
		return nil, err
	}
	return resp.Rooms, nil
}

// CreateDirectMessage opens a DM room with the given usernames, returning the
// existing room if one is already open. Passing the logged-in user's own name
// creates the self-DM, which is a useful scratch room.
func (c *Client) CreateDirectMessage(ctx context.Context, usernames ...string) (Room, error) {
	if len(usernames) == 0 {
		return Room{}, fmt.Errorf("rocket: at least one username is required")
	}
	body := map[string]any{}
	if len(usernames) == 1 {
		body["username"] = usernames[0]
	} else {
		body["usernames"] = strings.Join(usernames, ",")
	}
	var resp struct {
		Room Room `json:"room"`
	}
	if err := c.do(ctx, request{method: "POST", endpoint: "im.create", body: body}, &resp); err != nil {
		return Room{}, err
	}
	// im.create omits `t` on some versions; a DM is a DM.
	if resp.Room.Type == "" {
		resp.Room.Type = RoomTypeDirect
	}
	return resp.Room, nil
}

// Discussions lists discussions whose parent is roomID.
func (c *Client) Discussions(ctx context.Context, roomID string) ([]Room, error) {
	query := url.Values{"roomId": {roomID}, "count": {"100"}}
	var resp struct {
		Discussions []Room `json:"discussions"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: "rooms.getDiscussions", query: query}, &resp); err != nil {
		return nil, err
	}
	return resp.Discussions, nil
}
