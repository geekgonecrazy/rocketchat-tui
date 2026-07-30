package rocket

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
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

// MarkUnread marks a whole room unread. The server flags the subscription with
// alert: true and unread: 1 — the last message becomes the new one.
func (c *Client) MarkUnread(ctx context.Context, roomID string) error {
	return c.do(ctx, request{
		method:   "POST",
		endpoint: "subscriptions.unread",
		body:     map[string]any{"roomId": roomID},
	}, nil)
}

// MarkUnreadFrom marks a room unread from a specific message onwards, so that
// message and everything after it counts as new.
//
// The server refuses this form with error-action-not-allowed when the target is
// the caller's own message (observed on 8.4) — a user cannot mark unread from
// something they wrote. Callers should keep their own messages away from here
// rather than letting that surface as a bare 400; see docs/api-deviations.md §12.
func (c *Client) MarkUnreadFrom(ctx context.Context, messageID string) error {
	if messageID == "" {
		return fmt.Errorf("rocket: mark unread requires a message id")
	}
	return c.do(ctx, request{
		method:   "POST",
		endpoint: "subscriptions.unread",
		body:     map[string]any{"firstUnreadMessage": map[string]any{"_id": messageID}},
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

// membersEndpoint maps a room type onto its type-specific members endpoint.
func membersEndpoint(roomType string) (string, error) {
	prefix, err := roomPrefix(roomType)
	if err != nil {
		return "", fmt.Errorf("%w (listing members)", err)
	}
	return prefix + ".members", nil
}

// RoomMembers lists the users in a room, newest joiners last.
//
// count bounds the page; the server caps it well below the size of a busy
// channel, so callers should treat the result as "the members worth offering"
// rather than a complete roster.
func (c *Client) RoomMembers(ctx context.Context, roomID, roomType string, count int) ([]User, error) {
	endpoint, err := membersEndpoint(roomType)
	if err != nil {
		return nil, err
	}
	if count <= 0 {
		count = 100
	}
	query := url.Values{
		"roomId": {roomID},
		"count":  {strconv.Itoa(count)},
	}
	var resp struct {
		Members []User `json:"members"`
	}
	if err := c.do(ctx, request{method: "GET", endpoint: endpoint, query: query}, &resp); err != nil {
		return nil, err
	}
	return resp.Members, nil
}

// roomPrefix maps a room type onto the family of endpoints that operate on it.
// Rocket.Chat has no generic room API: every operation exists three times, once
// per type, and picking the wrong one is an error about the room not existing
// rather than about the endpoint.
func roomPrefix(roomType string) (string, error) {
	switch roomType {
	case RoomTypeChannel, RoomTypeLive:
		return "channels", nil
	case RoomTypePrivate:
		return "groups", nil
	case RoomTypeDirect:
		return "im", nil
	case "":
		return "", fmt.Errorf("rocket: room type is required")
	default:
		return "", fmt.Errorf("rocket: unsupported room type %q", roomType)
	}
}

// LeaveRoom removes the logged-in user from a channel or private group.
//
// A direct message has no membership to give up — the DM exists as long as both
// people are on the server — so it is refused here with something a user can act
// on instead of the server's complaint about an unknown endpoint.
func (c *Client) LeaveRoom(ctx context.Context, roomID, roomType string) error {
	if roomType == RoomTypeDirect {
		return fmt.Errorf("rocket: a direct message cannot be left; hide it instead")
	}
	prefix, err := roomPrefix(roomType)
	if err != nil {
		return err
	}
	return c.do(ctx, request{
		method:   "POST",
		endpoint: prefix + ".leave",
		body:     map[string]any{"roomId": roomID},
	}, nil)
}

// HideRoom closes a room for this user: it leaves the sidebar without anyone
// leaving the room, and reopens the moment there is something new in it.
func (c *Client) HideRoom(ctx context.Context, roomID, roomType string) error {
	prefix, err := roomPrefix(roomType)
	if err != nil {
		return err
	}
	return c.do(ctx, request{
		method:   "POST",
		endpoint: prefix + ".close",
		body:     map[string]any{"roomId": roomID},
	}, nil)
}

// JoinRoom adds the logged-in user to a public channel. joinCode is the code a
// channel may be protected with, and is omitted when empty.
func (c *Client) JoinRoom(ctx context.Context, roomID, joinCode string) error {
	body := map[string]any{"roomId": roomID}
	if joinCode != "" {
		body["joinCode"] = joinCode
	}
	return c.do(ctx, request{method: "POST", endpoint: "channels.join", body: body}, nil)
}

// ChannelByName looks a public channel up by its slug, which is what a user
// types: joining takes a room id, and a channel you have not joined is not in
// the local cache to resolve one from.
func (c *Client) ChannelByName(ctx context.Context, name string) (Room, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "#")
	if name == "" {
		return Room{}, fmt.Errorf("rocket: channel name is required")
	}
	var resp struct {
		Channel Room `json:"channel"`
	}
	if err := c.do(ctx, request{
		method:   "GET",
		endpoint: "channels.info",
		query:    url.Values{"roomName": {name}},
	}, &resp); err != nil {
		return Room{}, err
	}
	return resp.Channel, nil
}

// RemoveFromRoom removes another user from a channel or private group.
func (c *Client) RemoveFromRoom(ctx context.Context, roomID, roomType, userID string) error {
	if roomType == RoomTypeDirect {
		return fmt.Errorf("rocket: nobody can be removed from a direct message")
	}
	prefix, err := roomPrefix(roomType)
	if err != nil {
		return err
	}
	return c.do(ctx, request{
		method:   "POST",
		endpoint: prefix + ".kick",
		body:     map[string]any{"roomId": roomID, "userId": userID},
	}, nil)
}

// SetTopic replaces a room's topic.
func (c *Client) SetTopic(ctx context.Context, roomID, roomType, topic string) error {
	prefix, err := roomPrefix(roomType)
	if err != nil {
		return err
	}
	return c.do(ctx, request{
		method:   "POST",
		endpoint: prefix + ".setTopic",
		body:     map[string]any{"roomId": roomID, "topic": topic},
	}, nil)
}

// SetArchived archives or unarchives a channel or private group.
func (c *Client) SetArchived(ctx context.Context, roomID, roomType string, archived bool) error {
	if roomType == RoomTypeDirect {
		return fmt.Errorf("rocket: a direct message cannot be archived")
	}
	prefix, err := roomPrefix(roomType)
	if err != nil {
		return err
	}
	action := ".unarchive"
	if archived {
		action = ".archive"
	}
	return c.do(ctx, request{
		method:   "POST",
		endpoint: prefix + action,
		body:     map[string]any{"roomId": roomID},
	}, nil)
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
