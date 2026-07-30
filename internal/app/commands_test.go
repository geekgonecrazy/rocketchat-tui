package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// lastCommands returns the most recent registry the core published.
func (h *harness) lastCommands() ([]model.Command, bool) {
	events := h.snapshot()
	for i := len(events) - 1; i >= 0; i-- {
		if update, ok := events[i].(app.CommandsUpdated); ok {
			return update.Commands, true
		}
	}
	return nil, false
}

// waitForCommand blocks until the registry holds a command and returns it.
func (h *harness) waitForCommand(name string) model.Command {
	return waitFor(h.t, "command /"+name, func() (model.Command, bool) {
		commands, ok := h.lastCommands()
		if !ok {
			return model.Command{}, false
		}
		return model.FindCommand(commands, name)
	})
}

// waitForNotice blocks until a notice containing want has been published.
func (h *harness) waitForNotice(want string) app.Notice {
	return waitFor(h.t, "notice containing "+want, func() (app.Notice, bool) {
		for _, event := range h.snapshot() {
			if notice, ok := event.(app.Notice); ok && strings.Contains(notice.Text, want) {
				return notice, true
			}
		}
		return app.Notice{}, false
	})
}

// The registry is published before any network call, so the composer can offer
// the commands rctui implements itself while the server is still being asked.
func TestClientCommandsAreAvailableWithoutDiscovery(t *testing.T) {
	h := newHarness(t)
	h.start()

	exit := h.waitForCommand("exit")
	if exit.Scope != model.ScopeClient {
		t.Errorf("/exit scope = %v, want client", exit.Scope)
	}
	leave := h.waitForCommand("leave")
	if leave.Scope != model.ScopeLocal {
		t.Errorf("/leave scope = %v, want local before discovery", leave.Scope)
	}
}

// The server is the authority on its own commands: where it offers one, its
// version displaces the fallback, description and all.
func TestDiscoveredCommandsDisplaceTheLocalFallbacks(t *testing.T) {
	h := newHarness(t)
	h.server.AddCommand("leave", "", "Leave the channel", false)
	h.server.AddCommand("gimme", "<message>", "Sends a message with a table flipper", false)
	h.start()

	leave := waitFor(t, "the server's /leave", func() (model.Command, bool) {
		commands, ok := h.lastCommands()
		if !ok {
			return model.Command{}, false
		}
		command, found := model.FindCommand(commands, "leave")
		return command, found && command.Scope == model.ScopeServer
	})
	if leave.Description != "Leave the channel" {
		t.Errorf("description = %q, want the server's", leave.Description)
	}
	if gimme := h.waitForCommand("gimme"); gimme.Scope != model.ScopeServer {
		t.Errorf("/gimme scope = %v, want server", gimme.Scope)
	}
}

// A client command is never displaced: no server can quit rctui for us, and its
// own registration of /open is flagged clientOnly for exactly that reason.
func TestClientCommandsSurviveAServerRegistrationOfTheSameName(t *testing.T) {
	h := newHarness(t)
	h.server.AddCommand("open", "#channel", "Open a channel", true)
	h.server.AddCommand("upload", "", "Upload a file", false)
	h.start()
	h.waitForCommand("leave") // discovery has landed by the time this is here

	commands, _ := h.lastCommands()
	for _, name := range []string{"open", "upload", "exit"} {
		command, found := model.FindCommand(commands, name)
		if !found {
			t.Fatalf("/%s missing from the registry", name)
		}
		if command.Scope != model.ScopeClient {
			t.Errorf("/%s scope = %v, want client", name, command.Scope)
		}
	}
}

// A clientOnly command we do not implement cannot run anywhere: commands.run
// will not execute it and neither can we. It is kept out of the completer and
// invoking it says why rather than posting a no-op at the server.
func TestClientOnlyCommandsWeCannotRunAreMarkedUnsupported(t *testing.T) {
	h := newHarness(t)
	h.server.AddCommand("jitsi", "", "Start a video call", true)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")

	jitsi := waitFor(t, "the discovered /jitsi", func() (model.Command, bool) {
		commands, ok := h.lastCommands()
		if !ok {
			return model.Command{}, false
		}
		command, found := model.FindCommand(commands, "jitsi")
		return command, found && command.Scope == model.ScopeUnsupported
	})
	if jitsi.Offerable() {
		t.Error("an unsupported command should not be offered by the completer")
	}

	h.core.RunCommand("room-1", "", "jitsi", "")
	notice := h.waitForNotice("no implementation")
	if !notice.IsErr {
		t.Error("the refusal should read as an error")
	}
	if len(h.server.RanCommands()) != 0 {
		t.Error("nothing should have been sent to commands.run")
	}
}

func TestServerCommandRunsThroughCommandsRun(t *testing.T) {
	h := newHarness(t)
	h.server.AddCommand("archive", "", "Archive", false)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")
	h.waitForCommand("archive")

	h.core.RunCommand("room-1", "thread-1", "archive", "old stuff")

	ran := waitFor(t, "the command to reach the server", func() ([]fakerc.RanCommand, bool) {
		calls := h.server.RanCommands()
		return calls, len(calls) > 0
	})
	if ran[0].Command != "archive" || ran[0].Params != "old stuff" {
		t.Errorf("ran %+v", ran[0])
	}
	if ran[0].RoomID != "room-1" || ran[0].ThreadID != "thread-1" {
		t.Errorf("the room and thread should travel with the command: %+v", ran[0])
	}
}

// Without a server registration the fallback runs, and it runs against the room
// type: a private group is groups.leave, not channels.leave.
func TestLeaveFallsBackToRESTAndDropsTheRoom(t *testing.T) {
	h := newHarness(t)
	lastSeen := time.Now().Add(-time.Hour)
	h.server.AddRoom("room-1", "p", "secrets", nil)
	h.server.AddSubscription("room-1", "p", "secrets", 0, 0, lastSeen, nil)
	h.start()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")

	h.core.RunCommand("room-1", "", "leave", "")

	action := waitFor(t, "the leave call", func() (string, bool) {
		actions := h.server.RoomActions()
		if len(actions) == 0 {
			return "", false
		}
		return actions[0].Endpoint, true
	})
	if action != "groups.leave" {
		t.Errorf("endpoint = %s, want groups.leave", action)
	}

	// The sidebar must lose the room now rather than at the next full sync: the
	// delta form of subscriptions.get reports removals separately, so nothing
	// else would take it away.
	waitFor(t, "the room to leave the sidebar", func() (bool, bool) {
		snapshot, ok := h.lastRooms()
		if !ok {
			return false, false
		}
		for _, room := range snapshot.Rooms {
			if room.ID == "room-1" {
				return false, false
			}
		}
		return true, true
	})
	waitFor(t, "the room to be closed", func() (bool, bool) {
		for _, event := range h.snapshot() {
			if closed, ok := event.(app.RoomClosed); ok && closed.RoomID == "room-1" {
				return true, true
			}
		}
		return false, false
	})
}

func TestInviteResolvesUsernamesBeforeInviting(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")

	h.core.RunCommand("room-1", "", "invite", "@jane bob")

	waitFor(t, "both invites", func() (bool, bool) {
		var invited []string
		for _, action := range h.server.RoomActions() {
			if action.Endpoint == "channels.invite" {
				invited = append(invited, action.UserID)
			}
		}
		return true, len(invited) == 2 && invited[0] == "user-jane" && invited[1] == "user-bob"
	})
	h.waitForNotice("invited @jane, @bob")
}

// An invite runs through the list one user at a time and nothing rolls back, so
// a failure partway has to name who it stopped on: everyone before them is
// already in the room, and everyone after them is not.
func TestInviteThatFailsPartwayNamesTheUserItStoppedOn(t *testing.T) {
	h := newHarness(t)
	h.server.NoSuchUsers = []string{"ghost"}
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")

	h.core.RunCommand("room-1", "", "invite", "@jane @ghost @bob")

	notice := h.waitForNotice("invite @ghost")
	if !notice.IsErr {
		t.Error("a refused invite should read as an error")
	}

	var invited []string
	for _, action := range h.server.RoomActions() {
		if action.Endpoint == "channels.invite" {
			invited = append(invited, action.UserID)
		}
	}
	if len(invited) != 1 || invited[0] != "user-jane" {
		t.Errorf("invited %v, want only jane — the run stops where it failed", invited)
	}
}

// A command nobody has heard of is reported, not posted. Sending the typo to
// the room as text would be both useless and public.
func TestUnknownCommandIsRefusedRatherThanSent(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")

	h.core.RunCommand("room-1", "", "inivte", "@jane")

	notice := h.waitForNotice("no such command: /inivte")
	if !notice.IsErr {
		t.Error("an unknown command should read as an error")
	}
	if len(h.server.SentMessages()) != 0 {
		t.Errorf("nothing should have been posted, got %+v", h.server.SentMessages())
	}
}

// Discovery is additive. A server that refuses commands.list — it can be
// permission-gated — must leave the client commands and the fallbacks working.
func TestFailedDiscoveryLeavesTheRegistryIntact(t *testing.T) {
	h := newHarness(t)
	h.server.RejectCommandList = true
	h.start()

	if exit := h.waitForCommand("exit"); exit.Scope != model.ScopeClient {
		t.Errorf("/exit scope = %v", exit.Scope)
	}
	if leave := h.waitForCommand("leave"); leave.Scope != model.ScopeLocal {
		t.Errorf("/leave scope = %v", leave.Scope)
	}
}

func TestShrugPostsTheDecoratedMessage(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")

	h.core.RunCommand("room-1", "", "shrug", "who knows")

	sent := waitFor(t, "the decorated message", func() (string, bool) {
		messages := h.server.SentMessages()
		if len(messages) == 0 {
			return "", false
		}
		return messages[0].Text, true
	})
	if sent != `who knows ¯\_(ツ)_/¯` {
		t.Errorf("sent %q", sent)
	}
}
