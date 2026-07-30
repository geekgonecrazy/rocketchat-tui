package rocket_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// fakeClient is a client authenticated against the fake server, which is a
// different pairing from files_test's authedClient (a bare httptest recorder).
func fakeClient(t *testing.T, server *fakerc.Server) *rocket.Client {
	t.Helper()
	client := newClient(t, server)
	client.SetCredentials(rocket.Credentials{
		ServerURL: server.URL,
		UserID:    fakerc.UserID,
		AuthToken: fakerc.AuthToken,
	})
	return client
}

func TestCommandsPagesToTheEndOfTheList(t *testing.T) {
	server := fakerc.New(t)
	// More than one page: a server with a couple of apps installed has this many
	// commands, and a client that reads only the first page would hide the tail.
	const total = 250
	for i := 0; i < total; i++ {
		server.AddCommand("cmd"+strconv.Itoa(i), "", "command "+strconv.Itoa(i), false)
	}

	commands, err := fakeClient(t, server).Commands(context.Background())
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	if len(commands) != total {
		t.Fatalf("got %d commands, want %d", len(commands), total)
	}
	if commands[0].Command != "cmd0" || commands[total-1].Command != "cmd"+strconv.Itoa(total-1) {
		t.Errorf("pages arrived out of order: first %q, last %q",
			commands[0].Command, commands[total-1].Command)
	}
}

func TestCommandsReportsClientOnlyAndPreviewFlags(t *testing.T) {
	server := fakerc.New(t)
	server.AddCommand("open", "#channel", "Open a room", true)
	server.AddCommand("archive", "", "Archive", false)

	commands, err := fakeClient(t, server).Commands(context.Background())
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("got %d commands, want 2", len(commands))
	}
	if !commands[0].ClientOnly {
		t.Error("open should be reported as client-only: nothing else says commands.run cannot execute it")
	}
	if commands[1].ClientOnly {
		t.Error("archive is not client-only")
	}
	if commands[0].Params != "#channel" {
		t.Errorf("params = %q, want #channel", commands[0].Params)
	}
}

func TestRunCommandSendsRoomThreadAndTrigger(t *testing.T) {
	server := fakerc.New(t)
	server.AddCommand("archive", "", "Archive", false)
	client := fakeClient(t, server)

	err := client.RunCommand(context.Background(), rocket.RunOptions{
		// The leading slash is what the user typed; the endpoint wants it gone.
		Command:  "/archive",
		Params:   "old stuff",
		RoomID:   "room-1",
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}

	ran := server.RanCommands()
	if len(ran) != 1 {
		t.Fatalf("got %d runs, want 1", len(ran))
	}
	if ran[0].Command != "archive" || ran[0].Params != "old stuff" {
		t.Errorf("ran %+v, want archive with params", ran[0])
	}
	if ran[0].RoomID != "room-1" || ran[0].ThreadID != "thread-1" {
		t.Errorf("ran %+v, want room-1 / thread-1", ran[0])
	}
	// An app command that opens a modal fails outright without one, so it goes
	// out on every call rather than being guessed at per command.
	if ran[0].TriggerID == "" {
		t.Error("no trigger id was sent")
	}
}

func TestRunCommandSurfacesTheServersRefusal(t *testing.T) {
	server := fakerc.New(t)
	client := fakeClient(t, server)

	err := client.RunCommand(context.Background(), rocket.RunOptions{Command: "nope", RoomID: "room-1"})
	if err == nil {
		t.Fatal("an unregistered command should fail")
	}
	var apiErr *rocket.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorType != "error-invalid-command" {
		t.Errorf("err = %v, want the server's error-invalid-command", err)
	}
}

func TestRoomOperationsDispatchOnRoomType(t *testing.T) {
	server := fakerc.New(t)
	client := fakeClient(t, server)
	ctx := context.Background()

	if err := client.LeaveRoom(ctx, "room-1", rocket.RoomTypeChannel); err != nil {
		t.Fatalf("LeaveRoom channel: %v", err)
	}
	if err := client.LeaveRoom(ctx, "room-2", rocket.RoomTypePrivate); err != nil {
		t.Fatalf("LeaveRoom group: %v", err)
	}
	if err := client.HideRoom(ctx, "room-3", rocket.RoomTypeDirect); err != nil {
		t.Fatalf("HideRoom dm: %v", err)
	}
	if err := client.SetTopic(ctx, "room-1", rocket.RoomTypeChannel, "release week"); err != nil {
		t.Fatalf("SetTopic: %v", err)
	}
	if err := client.SetArchived(ctx, "room-1", rocket.RoomTypeChannel, false); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}

	actions := server.RoomActions()
	want := []string{"channels.leave", "groups.leave", "im.close", "channels.setTopic", "channels.unarchive"}
	if len(actions) != len(want) {
		t.Fatalf("got %d actions %+v, want %d", len(actions), actions, len(want))
	}
	for i, endpoint := range want {
		if actions[i].Endpoint != endpoint {
			t.Errorf("action %d = %s, want %s", i, actions[i].Endpoint, endpoint)
		}
	}
	if actions[3].Topic != "release week" {
		t.Errorf("topic = %q", actions[3].Topic)
	}
}

// A direct message has no membership to give up. The server answers a bare 400
// for the endpoint that does not exist, so the refusal is made here instead.
func TestLeavingADirectMessageIsRefusedLocally(t *testing.T) {
	server := fakerc.New(t)
	err := fakeClient(t, server).LeaveRoom(context.Background(), "room-1", rocket.RoomTypeDirect)
	if err == nil {
		t.Fatal("leaving a DM should be refused")
	}
	if len(server.RoomActions()) != 0 {
		t.Error("nothing should have been sent")
	}
}
