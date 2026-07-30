package rocket

import (
	"context"
	"encoding/json"
	"fmt"
)

// Team is a Rocket.Chat team: a main room plus the channels grouped under it.
type Team struct {
	ID        string    `json:"_id"`
	Name      string    `json:"name"`
	RoomID    string    `json:"roomId"`
	Type      int       `json:"type"` // 0 public, 1 private
	CreatedAt Timestamp `json:"createdAt"`
}

// TeamVisibility selects whether a created team or channel is public.
type TeamVisibility int

const (
	Public  TeamVisibility = 0
	Private TeamVisibility = 1
)

// CreateTeam creates a team and its main room.
func (c *Client) CreateTeam(ctx context.Context, name string, visibility TeamVisibility) (Team, error) {
	if name == "" {
		return Team{}, fmt.Errorf("rocket: team name is required")
	}
	var resp struct {
		Team Team `json:"team"`
	}
	err := c.do(ctx, request{
		method:   "POST",
		endpoint: "teams.create",
		body:     map[string]any{"name": name, "type": int(visibility)},
	}, &resp)
	if err != nil {
		return Team{}, err
	}
	return resp.Team, nil
}

// TeamInfo looks a team up by name.
func (c *Client) TeamInfo(ctx context.Context, name string) (Team, error) {
	var resp struct {
		TeamInfo Team `json:"teamInfo"`
	}
	err := c.do(ctx, request{
		method:   "GET",
		endpoint: "teams.info",
		query:    map[string][]string{"teamName": {name}},
	}, &resp)
	if err != nil {
		return Team{}, err
	}
	return resp.TeamInfo, nil
}

// CreateTeamChannel creates a channel and attaches it to a team.
//
// This is deliberately two calls rather than teams.createRoom: that endpoint is
// documented but is not registered on 8.x (it answers a plain-text 404), whereas
// creating the room and then teams.addRooms works across versions.
func (c *Client) CreateTeamChannel(ctx context.Context, teamID, name string, visibility TeamVisibility) (Room, error) {
	if teamID == "" || name == "" {
		return Room{}, fmt.Errorf("rocket: team id and channel name are required")
	}

	room, err := c.CreateRoom(ctx, name, visibility)
	if err != nil {
		return Room{}, err
	}
	if err := c.AddTeamRooms(ctx, teamID, room.ID); err != nil {
		// The room exists either way; report it so the caller can still use it.
		return room, fmt.Errorf("rocket: created %s but could not attach it to the team: %w", name, err)
	}
	room.TeamID = teamID
	return room, nil
}

// CreateRoom creates a standalone channel (public) or private group.
func (c *Client) CreateRoom(ctx context.Context, name string, visibility TeamVisibility) (Room, error) {
	if name == "" {
		return Room{}, fmt.Errorf("rocket: room name is required")
	}
	endpoint, key := "channels.create", "channel"
	if visibility == Private {
		endpoint, key = "groups.create", "group"
	}

	var resp map[string]json.RawMessage
	if err := c.do(ctx, request{
		method:   "POST",
		endpoint: endpoint,
		body:     map[string]any{"name": name},
	}, &resp); err != nil {
		return Room{}, err
	}

	var room Room
	if raw, ok := resp[key]; ok {
		if err := json.Unmarshal(raw, &room); err != nil {
			return Room{}, fmt.Errorf("rocket: %s: decode %s: %w", endpoint, key, err)
		}
	}
	if room.ID == "" {
		return Room{}, fmt.Errorf("rocket: %s returned no room", endpoint)
	}
	return room, nil
}

// AddTeamRooms attaches existing rooms to a team.
func (c *Client) AddTeamRooms(ctx context.Context, teamID string, roomIDs ...string) error {
	if teamID == "" || len(roomIDs) == 0 {
		return nil
	}
	return c.do(ctx, request{
		method:   "POST",
		endpoint: "teams.addRooms",
		body:     map[string]any{"teamId": teamID, "rooms": roomIDs},
	}, nil)
}

// CreateDiscussion creates a discussion whose parent is parentRoomID. When
// messageID is non-empty the discussion is anchored to that message, which is
// how the web client turns a message into a discussion.
func (c *Client) CreateDiscussion(ctx context.Context, parentRoomID, name, messageID string) (Room, error) {
	if parentRoomID == "" || name == "" {
		return Room{}, fmt.Errorf("rocket: parent room and discussion name are required")
	}
	body := map[string]any{"prid": parentRoomID, "t_name": name}
	if messageID != "" {
		body["pmid"] = messageID
	}
	var resp struct {
		Discussion Room `json:"discussion"`
	}
	err := c.do(ctx, request{
		method:   "POST",
		endpoint: "rooms.createDiscussion",
		body:     body,
	}, &resp)
	if err != nil {
		return Room{}, err
	}
	return resp.Discussion, nil
}

// AddTeamMembers adds users to a team by user id. Members added to the team gain
// access to its main room; team channels are joined separately.
func (c *Client) AddTeamMembers(ctx context.Context, teamID string, userIDs ...string) error {
	if teamID == "" || len(userIDs) == 0 {
		return nil
	}
	members := make([]map[string]any, 0, len(userIDs))
	for _, userID := range userIDs {
		members = append(members, map[string]any{
			"userId": userID,
			"roles":  []string{"member"},
		})
	}
	return c.do(ctx, request{
		method:   "POST",
		endpoint: "teams.addMembers",
		body:     map[string]any{"teamId": teamID, "members": members},
	}, nil)
}

// TeamMembers lists a team's members.
func (c *Client) TeamMembers(ctx context.Context, teamID string) ([]User, error) {
	var resp struct {
		Members []struct {
			User User `json:"user"`
		} `json:"members"`
	}
	err := c.do(ctx, request{
		method:   "GET",
		endpoint: "teams.members",
		query:    map[string][]string{"teamId": {teamID}, "count": {"100"}},
	}, &resp)
	if err != nil {
		return nil, err
	}
	users := make([]User, 0, len(resp.Members))
	for _, member := range resp.Members {
		users = append(users, member.User)
	}
	return users, nil
}

// InviteToRoom adds users to a channel or group by user id.
func (c *Client) InviteToRoom(ctx context.Context, roomID, roomType string, userIDs ...string) error {
	if len(userIDs) == 0 {
		return nil
	}
	endpoint := "channels.invite"
	if roomType == RoomTypePrivate {
		endpoint = "groups.invite"
	}
	for _, userID := range userIDs {
		err := c.do(ctx, request{
			method:   "POST",
			endpoint: endpoint,
			body:     map[string]any{"roomId": roomID, "userId": userID},
		}, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

// UserByUsername resolves a username to a user record, used to invite someone
// without asking for their internal id.
func (c *Client) UserByUsername(ctx context.Context, username string) (User, error) {
	var resp struct {
		User User `json:"user"`
	}
	err := c.do(ctx, request{
		method:   "GET",
		endpoint: "users.info",
		query:    map[string][]string{"username": {username}},
	}, &resp)
	if err != nil {
		return User{}, err
	}
	return resp.User, nil
}
