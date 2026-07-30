package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// Slash commands come from three places and only the first is fixed:
//
//   - the client commands below, which act on rctui itself;
//   - the local fallbacks below, which rctui can perform over REST for servers
//     that do not offer them;
//   - whatever the server advertises through commands.list, which differs per
//     deployment and is the only authority on its own commands.
//
// The merge is one-directional (see mergeCommands): a client command is never
// displaced, and a server command always displaces a local fallback of the same
// name, because the server's version is what that deployment means by it.

// clientCommands are executed by the UI, not here. The core lists them so they
// appear in the completer alongside everything else, and rejects them if one
// reaches it, which would mean the UI failed to claim it.
var clientCommands = []model.Command{
	{Name: "exit", Description: "leave rctui", Scope: model.ScopeClient},
	{Name: "quit", Description: "leave rctui", Scope: model.ScopeClient},
	{Name: "upload", Params: "[path]", Description: "attach a file to this message", Scope: model.ScopeClient},
	{Name: "open", Params: "<room>", Description: "jump to a room in the sidebar", Scope: model.ScopeClient},
	{Name: "help", Description: "show the key reference", Scope: model.ScopeClient},
}

// ClientCommands is the set the UI implements itself.
//
// The UI holds these from the moment it is built rather than waiting for the
// registry to arrive: /exit has to work on a screen that has not finished
// loading, which is exactly the screen someone is most likely to want out of.
func ClientCommands() []model.Command {
	return append([]model.Command(nil), clientCommands...)
}

// localHandler performs a command against the REST API. It runs off the loop
// inside background, so it must come back through enqueue to touch state.
type localHandler func(ctx context.Context, c *Core, inv invocation) error

// invocation is everything a local handler is given: the command's arguments
// and the room it was typed in, already resolved to a wire type.
type invocation struct {
	Name     string
	Params   string
	RoomID   string
	RoomType string
	ThreadID string
}

// localCommand pairs a fallback's advertisement with its implementation.
type localCommand struct {
	command model.Command
	run     localHandler
}

// localCommands are the ones a client can carry out itself when the server does
// not offer them. They are ordinary REST calls; nothing here is a re-implementation
// of server behaviour beyond the single call each one makes.
var localCommands = []localCommand{
	{model.Command{Name: "leave", Description: "leave this room", Scope: model.ScopeLocal}, runLeave},
	{model.Command{Name: "part", Description: "leave this room", Scope: model.ScopeLocal}, runLeave},
	{model.Command{Name: "hide", Description: "hide this room until there is something new", Scope: model.ScopeLocal}, runHide},
	{model.Command{Name: "join", Params: "#channel", Description: "join a public channel", Scope: model.ScopeLocal}, runJoin},
	{model.Command{Name: "invite", Params: "@username…", Description: "add people to this room", Scope: model.ScopeLocal}, runInvite},
	{model.Command{Name: "kick", Params: "@username", Description: "remove someone from this room", Scope: model.ScopeLocal}, runKick},
	{model.Command{Name: "topic", Params: "<text>", Description: "set this room's topic", Scope: model.ScopeLocal}, runTopic},
	{model.Command{Name: "archive", Description: "archive this room", Scope: model.ScopeLocal}, runArchive},
	{model.Command{Name: "unarchive", Description: "unarchive this room", Scope: model.ScopeLocal}, runUnarchive},
	{model.Command{Name: "create", Params: "<name>", Description: "create a public channel", Scope: model.ScopeLocal}, runCreate},
	{model.Command{Name: "msg", Params: "@username <message>", Description: "send a direct message", Scope: model.ScopeLocal}, runDirectMessage},
	{model.Command{Name: "shrug", Params: "[message]", Description: `append ¯\_(ツ)_/¯`, Scope: model.ScopeLocal}, decorator(`¯\_(ツ)_/¯`)},
	{model.Command{Name: "tableflip", Params: "[message]", Description: "append (╯°□°)╯︵ ┻━┻", Scope: model.ScopeLocal}, decorator("(╯°□°)╯︵ ┻━┻")},
	{model.Command{Name: "unflip", Params: "[message]", Description: "append ┬─┬ ノ( ゜-゜ノ)", Scope: model.ScopeLocal}, decorator("┬─┬ ノ( ゜-゜ノ)")},
	{model.Command{Name: "lennyface", Params: "[message]", Description: "append ( ͡° ͜ʖ ͡°)", Scope: model.ScopeLocal}, decorator("( ͡° ͜ʖ ͡°)")},
}

// ---- registry ---------------------------------------------------------------

// loadCommands publishes what we know about slash commands and then refreshes it
// from the server.
//
// The cached list is served first for the same reason the room list is: the
// completer should be complete the moment the composer accepts input, not one
// round trip later. Discovery is additive — a server that refuses commands.list
// (it can be permission-gated) leaves the client commands and the fallbacks
// exactly as they are rather than emptying the list.
func (c *Core) loadCommands() {
	cached, err := c.store.Commands()
	if err != nil {
		c.logger.Debug("cached commands unavailable", "err", err)
	}
	c.setCommands(cached)

	c.background(func(ctx context.Context) error {
		discovered, err := c.client.Commands(ctx)
		if err != nil {
			// Not reported to the user: a server without this endpoint, or an
			// account without the permission, is not a failure the user can act
			// on, and everything else still works.
			c.logger.Debug("slash command discovery failed", "err", err)
			return nil
		}
		if err := c.store.SaveCommands(discovered); err != nil {
			return err
		}
		c.enqueue(func(c *Core) { c.setCommands(discovered) })
		return nil
	})
}

// setCommands rebuilds the registry from a server list and publishes it.
func (c *Core) setCommands(discovered []rocket.Command) {
	c.commands = mergeCommands(discovered)
	c.emit(CommandsUpdated{Commands: append([]model.Command(nil), c.commands...)})
}

// mergeCommands folds what the server offers into what we implement.
//
// Precedence, in one place because it is the whole design:
//
//   - a client command wins outright — the server cannot quit rctui for us, and
//     its own registration of /open is flagged clientOnly for the same reason;
//   - a server command displaces a local fallback of the same name, taking its
//     params and description with it, since the server is the authority on what
//     that command does on that deployment;
//   - except when the server flags it clientOnly, which means commands.run will
//     not execute it. Then our fallback stands, and where we have none the
//     command is recorded as unsupported so that invoking it can say why
//     instead of posting a no-op at the server.
func mergeCommands(discovered []rocket.Command) []model.Command {
	merged := make(map[string]model.Command, len(discovered)+len(clientCommands)+len(localCommands))
	for _, command := range clientCommands {
		merged[command.Name] = command
	}
	for _, local := range localCommands {
		if _, taken := merged[local.command.Name]; !taken {
			merged[local.command.Name] = local.command
		}
	}

	for _, command := range discovered {
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(command.Command, "/")))
		if name == "" {
			continue
		}
		existing, known := merged[name]
		if known && existing.Scope == model.ScopeClient {
			continue
		}
		if command.ClientOnly {
			if known {
				continue // our own implementation stands
			}
			merged[name] = model.Command{
				Name:        name,
				Params:      command.Params,
				Description: command.Description,
				Scope:       model.ScopeUnsupported,
			}
			continue
		}
		merged[name] = model.Command{
			Name:        name,
			Params:      command.Params,
			Description: command.Description,
			Scope:       model.ScopeServer,
		}
	}

	commands := make([]model.Command, 0, len(merged))
	for _, command := range merged {
		commands = append(commands, command)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commands
}

// ---- dispatch ---------------------------------------------------------------

// RunCommand executes a slash command typed into the composer. The UI has
// already claimed the client commands; everything else arrives here.
func (c *Core) RunCommand(roomID, threadID, name, params string) {
	c.enqueue(func(c *Core) {
		name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
		command, known := model.FindCommand(c.commands, name)
		switch {
		case !known:
			c.emit(Notice{Text: "no such command: /" + name, IsErr: true})
			return
		case command.Scope == model.ScopeUnsupported:
			c.emit(Notice{
				Text:  "/" + name + " runs in the client and rctui has no implementation of it",
				IsErr: true,
			})
			return
		case command.Scope == model.ScopeClient:
			// The UI owns these; reaching the core means it did not claim one.
			c.emit(Notice{Text: "/" + name + " cannot run here", IsErr: true})
			return
		case roomID == "":
			c.emit(Notice{Text: "/" + name + " needs an open room", IsErr: true})
			return
		}

		c.background(func(ctx context.Context) error {
			roomType, err := c.resolveRoomType(ctx, roomID)
			if err != nil {
				return err
			}
			inv := invocation{
				Name:     name,
				Params:   params,
				RoomID:   roomID,
				RoomType: roomType,
				ThreadID: threadID,
			}
			if command.Scope == model.ScopeServer {
				return c.runServerCommand(ctx, inv)
			}
			for _, local := range localCommands {
				if local.command.Name == name {
					return local.run(ctx, c, inv)
				}
			}
			return fmt.Errorf("/%s has no implementation", name)
		})
	})
}

// runServerCommand hands a command to the server and refreshes afterwards.
//
// What a command did is not in its response — it may have posted a message,
// changed the room, or done nothing visible — so the room is re-synced rather
// than guessed at. Realtime usually beats the refresh to it; the refresh is what
// covers the commands realtime says nothing about.
func (c *Core) runServerCommand(ctx context.Context, inv invocation) error {
	if err := c.client.RunCommand(ctx, rocket.RunOptions{
		Command:  inv.Name,
		Params:   inv.Params,
		RoomID:   inv.RoomID,
		ThreadID: inv.ThreadID,
	}); err != nil {
		return err
	}
	c.enqueue(func(c *Core) { c.catchUpRoom(inv.RoomID) })
	return nil
}

// ---- local handlers ---------------------------------------------------------

func runLeave(ctx context.Context, c *Core, inv invocation) error {
	label := c.roomLabel(inv.RoomID)
	if err := c.client.LeaveRoom(ctx, inv.RoomID, inv.RoomType); err != nil {
		return err
	}
	c.enqueue(func(c *Core) { c.forgetRoom(inv.RoomID, "left "+label) })
	return nil
}

func runHide(ctx context.Context, c *Core, inv invocation) error {
	label := c.roomLabel(inv.RoomID)
	if err := c.client.HideRoom(ctx, inv.RoomID, inv.RoomType); err != nil {
		return err
	}
	c.enqueue(func(c *Core) { c.forgetRoom(inv.RoomID, "hid "+label) })
	return nil
}

func runJoin(ctx context.Context, c *Core, inv invocation) error {
	name := strings.TrimPrefix(firstField(inv.Params), "#")
	if name == "" {
		return fmt.Errorf("/join takes a channel name")
	}
	channel, err := c.client.ChannelByName(ctx, name)
	if err != nil {
		return err
	}
	if err := c.client.JoinRoom(ctx, channel.ID, ""); err != nil {
		return err
	}
	if err := c.syncRooms(ctx); err != nil {
		return err
	}
	c.enqueue(func(c *Core) {
		c.emit(Notice{Text: "joined #" + name + " — /open " + name + " to go there"})
	})
	return nil
}

func runInvite(ctx context.Context, c *Core, inv invocation) error {
	usernames := parseUsernames(inv.Params)
	if len(usernames) == 0 {
		return fmt.Errorf("/invite takes one or more usernames")
	}
	for _, username := range usernames {
		user, err := c.client.UserByUsername(ctx, username)
		if err != nil {
			// Name the user we stopped on: anyone earlier in the list is already
			// in the room, and nothing here undoes that.
			return fmt.Errorf("invite @%s: %w", username, err)
		}
		if err := c.client.InviteToRoom(ctx, inv.RoomID, inv.RoomType, user.ID); err != nil {
			return fmt.Errorf("invite @%s: %w", username, err)
		}
	}
	c.enqueue(func(c *Core) {
		c.emit(Notice{Text: "invited @" + strings.Join(usernames, ", @")})
		// The roster feeds the @ completer, and it just changed.
		delete(c.membersSynced, inv.RoomID)
		c.syncMembers(inv.RoomID)
	})
	return nil
}

func runKick(ctx context.Context, c *Core, inv invocation) error {
	usernames := parseUsernames(inv.Params)
	if len(usernames) != 1 {
		return fmt.Errorf("/kick takes one username")
	}
	user, err := c.client.UserByUsername(ctx, usernames[0])
	if err != nil {
		return err
	}
	if err := c.client.RemoveFromRoom(ctx, inv.RoomID, inv.RoomType, user.ID); err != nil {
		return err
	}
	c.enqueue(func(c *Core) {
		c.emit(Notice{Text: "removed @" + usernames[0]})
		delete(c.membersSynced, inv.RoomID)
		c.syncMembers(inv.RoomID)
	})
	return nil
}

func runTopic(ctx context.Context, c *Core, inv invocation) error {
	if err := c.client.SetTopic(ctx, inv.RoomID, inv.RoomType, inv.Params); err != nil {
		return err
	}
	// The topic is in the header, so re-read the room rather than patching the
	// cached copy with what we asked for: the server may have trimmed it.
	room, err := c.client.RoomInfo(ctx, inv.RoomID)
	if err != nil {
		return err
	}
	if err := c.store.SaveRooms([]rocket.Room{room}); err != nil {
		return err
	}
	c.enqueue(func(c *Core) {
		c.refreshRooms()
		if c.currentRoom == inv.RoomID {
			c.emitTimeline(inv.RoomID)
		}
	})
	return nil
}

func runArchive(ctx context.Context, c *Core, inv invocation) error {
	return setArchived(ctx, c, inv, true)
}

func runUnarchive(ctx context.Context, c *Core, inv invocation) error {
	return setArchived(ctx, c, inv, false)
}

func setArchived(ctx context.Context, c *Core, inv invocation, archived bool) error {
	if err := c.client.SetArchived(ctx, inv.RoomID, inv.RoomType, archived); err != nil {
		return err
	}
	if err := c.syncRooms(ctx); err != nil {
		return err
	}
	verb := "archived "
	if !archived {
		verb = "unarchived "
	}
	label := c.roomLabel(inv.RoomID)
	c.enqueue(func(c *Core) { c.emit(Notice{Text: verb + label}) })
	return nil
}

func runCreate(ctx context.Context, c *Core, inv invocation) error {
	name := firstField(inv.Params)
	if name == "" {
		return fmt.Errorf("/create takes a channel name")
	}
	if _, err := c.client.CreateRoom(ctx, name, rocket.Public); err != nil {
		return err
	}
	if err := c.syncRooms(ctx); err != nil {
		return err
	}
	c.enqueue(func(c *Core) {
		c.emit(Notice{Text: "created #" + name + " — /open " + name + " to go there"})
	})
	return nil
}

// runDirectMessage opens a DM and posts into it. With no text it just opens the
// conversation, which is what /msg with a bare username means.
func runDirectMessage(ctx context.Context, c *Core, inv invocation) error {
	username, text, _ := strings.Cut(strings.TrimSpace(inv.Params), " ")
	username = strings.TrimPrefix(username, "@")
	if username == "" {
		return fmt.Errorf("/msg takes a username")
	}
	room, err := c.client.CreateDirectMessage(ctx, username)
	if err != nil {
		return err
	}
	if err := c.store.SaveRooms([]rocket.Room{room}); err != nil {
		return err
	}
	if text = strings.TrimSpace(text); text != "" {
		if err := c.sendText(ctx, room.ID, "", text); err != nil {
			return err
		}
	}
	if err := c.syncRooms(ctx); err != nil {
		return err
	}
	c.enqueue(func(c *Core) {
		c.emit(Notice{Text: "messaged @" + username + " — /open " + username + " to go there"})
	})
	return nil
}

// decorator builds the handlers behind /shrug and friends: the message with a
// fixed suffix. The server's own versions do exactly this.
func decorator(suffix string) localHandler {
	return func(ctx context.Context, c *Core, inv invocation) error {
		text := strings.TrimSpace(strings.TrimSpace(inv.Params) + " " + suffix)
		if err := c.sendText(ctx, inv.RoomID, inv.ThreadID, text); err != nil {
			return err
		}
		c.enqueue(func(c *Core) { c.refreshAfterSend(inv.RoomID, inv.ThreadID) })
		return nil
	}
}

// ---- helpers ----------------------------------------------------------------

// forgetRoom drops a room the user has just left or hidden. Waiting for the next
// sync to notice would leave it in the sidebar, and the delta form of
// subscriptions.get reports removals separately from updates.
func (c *Core) forgetRoom(roomID, notice string) {
	if err := c.store.DeleteSubscription(roomID); err != nil {
		c.reportError(err)
		return
	}
	delete(c.unreadMarker, roomID)
	delete(c.unreadAtOpen, roomID)
	delete(c.heldUnread, roomID)
	if c.currentRoom == roomID {
		c.currentRoom = ""
		c.currentThread = ""
		if c.realtime != nil {
			c.realtime.UnsubscribeRoomMessages(roomID)
			c.realtime.UnsubscribeRoomActivity(roomID)
		}
	}
	c.refreshRooms()
	c.emit(RoomClosed{RoomID: roomID})
	c.emit(Notice{Text: notice})
}

// roomLabel names a room for a notice, falling back to its id.
func (c *Core) roomLabel(roomID string) string {
	room, known := c.roomView(roomID)
	if !known {
		return roomID
	}
	return room.Label()
}

// firstField is the first whitespace-separated word of params, empty when there
// is none.
func firstField(params string) string {
	fields := strings.Fields(params)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// parseUsernames splits "@alice @bob" into usernames, tolerating the sigil being
// left off since the completer is what usually puts it there.
func parseUsernames(params string) []string {
	fields := strings.Fields(params)
	usernames := make([]string, 0, len(fields))
	for _, field := range fields {
		if name := strings.TrimPrefix(field, "@"); name != "" {
			usernames = append(usernames, name)
		}
	}
	return usernames
}
