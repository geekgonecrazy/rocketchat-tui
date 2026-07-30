//go:build livetest || integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// waitFor polls until check finds what it is looking for, returning the value.
func waitFor[T any](t *testing.T, what string, check func() (T, bool)) T {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if value, ok := check(); ok {
			return value
		}
		time.Sleep(100 * time.Millisecond)
	}
	var zero T
	t.Fatalf("timed out waiting for %s", what)
	return zero
}

// fixturePath is where the live test fixture is recorded so that every run
// reuses the same team, channel and discussion instead of littering the server.
const fixturePath = "testdata/live-fixture.json"

// Names are stable so the fixture can be recovered by lookup even if the file is
// lost, and so anything left on the server is obviously ours.
const (
	fixtureTeamName       = "rctui-testing"
	fixtureChannelName    = "rctui-test-channel"
	fixtureDiscussionName = "rctui-test-discussion"
	fixtureGuestUsername  = "aaron"
)

// Fixture is the set of rooms the live tests operate in. Everything the tests
// write goes here; no pre-existing room is ever posted to.
type Fixture struct {
	ServerURL string `json:"server_url"`

	TeamID     string `json:"team_id"`
	TeamRoomID string `json:"team_room_id"`
	TeamName   string `json:"team_name"`

	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`

	DiscussionID   string `json:"discussion_id"`
	DiscussionName string `json:"discussion_name"`

	CreatedAt string `json:"created_at"`
	Note      string `json:"note"`
}

// loadFixture reads the recorded fixture, returning false when there is none for
// this server.
func loadFixture(serverURL string) (Fixture, bool) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", fixturePath, err)
		}
		return Fixture{}, false
	}
	var fixture Fixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return Fixture{}, false
	}
	if fixture.ServerURL != serverURL || fixture.TeamRoomID == "" {
		return Fixture{}, false
	}
	return fixture, true
}

func saveFixture(fixture Fixture) error {
	if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(fixturePath, append(encoded, '\n'), 0o644)
}

// ensureFixture returns the shared test fixture, creating whatever is missing.
//
// It is idempotent in three layers: the recorded file, then a lookup by name,
// then creation. That way a lost file does not create a second team, and a
// half-created fixture is completed rather than duplicated.
func ensureFixture(ctx context.Context, client *rocket.Client, now time.Time) (Fixture, []string, error) {
	var actions []string

	fixture, found := loadFixture(client.ServerURL())
	if found {
		// Trust the file only if the team room still exists.
		if _, err := client.RoomInfo(ctx, fixture.TeamRoomID); err == nil {
			actions = append(actions, "reused recorded fixture")
			return completeFixture(ctx, client, fixture, actions, now)
		}
		actions = append(actions, "recorded fixture was stale; rebuilding")
		fixture = Fixture{}
	}

	fixture.ServerURL = client.ServerURL()
	fixture.TeamName = fixtureTeamName
	fixture.ChannelName = fixtureChannelName
	fixture.DiscussionName = fixtureDiscussionName
	fixture.CreatedAt = now.UTC().Format(time.RFC3339)
	fixture.Note = "created by rctui live tests; safe to delete"

	// Recover an existing team by name before creating one.
	if team, err := client.TeamInfo(ctx, fixtureTeamName); err == nil && team.ID != "" {
		fixture.TeamID, fixture.TeamRoomID = team.ID, team.RoomID
		actions = append(actions, "found existing team "+fixtureTeamName)
	} else {
		team, err := client.CreateTeam(ctx, fixtureTeamName, rocket.Private)
		if err != nil {
			return fixture, actions, fmt.Errorf("create team: %w", err)
		}
		fixture.TeamID, fixture.TeamRoomID = team.ID, team.RoomID
		actions = append(actions, "created team "+fixtureTeamName)
	}

	return completeFixture(ctx, client, fixture, actions, now)
}

// completeFixture fills in whatever parts of the fixture are missing.
func completeFixture(ctx context.Context, client *rocket.Client, fixture Fixture, actions []string, now time.Time) (Fixture, []string, error) {
	if fixture.ChannelID == "" {
		channel, err := client.CreateTeamChannel(ctx, fixture.TeamID, fixtureChannelName, rocket.Private)
		if err != nil {
			// A name clash means it already exists from an earlier run.
			actions = append(actions, "team channel not created ("+err.Error()+")")
		} else {
			fixture.ChannelID = channel.ID
			actions = append(actions, "created team channel "+fixtureChannelName)
		}
	}

	if fixture.DiscussionID == "" && fixture.ChannelID != "" {
		discussion, err := client.CreateDiscussion(ctx, fixture.ChannelID, fixtureDiscussionName, "")
		if err != nil {
			actions = append(actions, "discussion not created ("+err.Error()+")")
		} else {
			fixture.DiscussionID = discussion.ID
			actions = append(actions, "created discussion "+fixtureDiscussionName)
		}
	}

	// Make sure the named guest is a team member.
	if user, err := client.UserByUsername(ctx, fixtureGuestUsername); err == nil && user.ID != "" {
		members, err := client.TeamMembers(ctx, fixture.TeamID)
		already := false
		if err == nil {
			for _, member := range members {
				if member.ID == user.ID {
					already = true
				}
			}
		}
		if already {
			actions = append(actions, fixtureGuestUsername+" already a team member")
		} else if err := client.AddTeamMembers(ctx, fixture.TeamID, user.ID); err != nil {
			actions = append(actions, "could not add "+fixtureGuestUsername+" ("+err.Error()+")")
		} else {
			actions = append(actions, "added "+fixtureGuestUsername+" to the team")
		}
	} else {
		actions = append(actions, "could not resolve user "+fixtureGuestUsername)
	}

	fixture.ServerURL = client.ServerURL()
	if err := saveFixture(fixture); err != nil {
		return fixture, actions, fmt.Errorf("record fixture: %w", err)
	}
	return fixture, actions, nil
}
