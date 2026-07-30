package rocket

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Command is one slash command the server has registered. The set differs from
// server to server — the built-ins plus whatever apps are installed — which is
// why it is discovered rather than assumed.
type Command struct {
	Command     string `json:"command"`
	Params      string `json:"params"`      // a usage hint, e.g. "@username"
	Description string `json:"description"` // often an i18n key rather than prose
	// ClientOnly marks a command whose implementation lives in the client. The
	// server lists it so clients know it exists, but commands.run cannot execute
	// it: only a client that implements it can.
	ClientOnly bool `json:"clientOnly"`
	// ProvidesPreview marks a command that expects a preview to be requested and
	// one of its results chosen (the /giphy shape) rather than being run outright.
	ProvidesPreview bool `json:"providesPreview"`
	// AppID is set for commands contributed by an app rather than the core server.
	AppID string `json:"appId"`
}

// commandPageSize is how many commands we ask for at a time. The endpoint
// paginates, and a server with a few apps installed passes the default page.
const commandPageSize = 100

// Commands lists every slash command the server offers.
//
// The response is paginated, so this pages until it has `total` of them rather
// than taking the first page as the whole set — a server with apps installed
// routinely has more commands than one page holds, and a short read would
// silently hide the tail of the list from the completer.
func (c *Client) Commands(ctx context.Context) ([]Command, error) {
	var commands []Command
	for offset := 0; ; {
		query := url.Values{
			"count":  {strconv.Itoa(commandPageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		var resp struct {
			Commands []Command `json:"commands"`
			Total    int       `json:"total"`
		}
		if err := c.do(ctx, request{method: "GET", endpoint: "commands.list", query: query}, &resp); err != nil {
			return nil, err
		}
		commands = append(commands, resp.Commands...)
		offset += len(resp.Commands)

		// Stop on a short page as well as on the count: a server that reports no
		// total, or a wrong one, would otherwise spin here forever.
		if len(resp.Commands) == 0 || len(resp.Commands) < commandPageSize || offset >= resp.Total {
			break
		}
	}
	return commands, nil
}

// RunOptions describes a slash command being executed by the server.
type RunOptions struct {
	Command string // the name without its leading slash
	Params  string // everything the user typed after the name
	RoomID  string
	// ThreadID scopes the command to an open thread, the way the web client does
	// when the message box is a thread's.
	ThreadID string
}

// RunCommand executes a slash command on the server.
//
// A trigger id goes out with every call. It is what an app's command uses to
// open a modal in response, and an app that wants one fails the whole call
// without it — so it is cheaper to always send one than to guess which commands
// need it. Nothing here renders the modal that may come back; see
// docs/api-deviations.md § 19.
func (c *Client) RunCommand(ctx context.Context, opts RunOptions) error {
	name := strings.TrimPrefix(strings.TrimSpace(opts.Command), "/")
	if name == "" {
		return fmt.Errorf("rocket: run requires a command")
	}
	if opts.RoomID == "" {
		return fmt.Errorf("rocket: run requires a room id")
	}
	body := map[string]any{
		"command":   name,
		"params":    opts.Params,
		"roomId":    opts.RoomID,
		"triggerId": newTriggerID(),
	}
	if opts.ThreadID != "" {
		body["tmid"] = opts.ThreadID
	}
	return c.do(ctx, request{method: "POST", endpoint: "commands.run", body: body}, nil)
}

// newTriggerID mints the correlation id an app command answers a modal with.
// The server only ever echoes it back, so any unique string will do; a failure
// to read the random source is not worth failing the command over.
func newTriggerID() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "rctui-trigger"
	}
	return hex.EncodeToString(buf[:])
}
