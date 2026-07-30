// Package termimg draws images in a terminal.
//
// Three back ends, picked by what the terminal can do: the kitty graphics
// protocol (kitty, Ghostty, WezTerm, Konsole), iTerm2 inline images (iTerm2,
// mintty, VS Code), and a universal fallback that paints half-block characters
// in 24-bit colour. The fallback is coarse but works over ssh, in tmux, and in
// any terminal with truecolour, so asking to see an image always shows
// something.
package termimg

import (
	"os"
	"strings"
)

// Protocol is how an image gets onto the screen.
type Protocol int

const (
	// Blocks paints coloured half-block characters. Always available.
	Blocks Protocol = iota
	// Kitty is the kitty graphics protocol.
	Kitty
	// ITerm2 is the iTerm2 inline image protocol.
	ITerm2
)

func (p Protocol) String() string {
	switch p {
	case Kitty:
		return "kitty"
	case ITerm2:
		return "iterm2"
	default:
		return "blocks"
	}
}

// EnvOverride lets a user force a protocol when detection guesses wrong, which
// is inevitable: terminals impersonate each other and multiplexers rewrite the
// evidence. Accepts a protocol name, or "blocks" to opt out of graphics.
const EnvOverride = "RCTUI_IMAGE_PROTOCOL"

// Detect picks a protocol from the environment. Pass os.Getenv; the indirection
// is so this can be tested without mutating the process environment.
func Detect(getenv func(string) string) Protocol {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvOverride))) {
	case "kitty":
		return Kitty
	case "iterm2", "iterm":
		return ITerm2
	case "blocks", "ansi", "none":
		return Blocks
	}

	// Inside a multiplexer the graphics escape never reaches the real terminal
	// unless passthrough is configured, and even then the multiplexer's own
	// redraws paint over the pixels because it has no idea they are there.
	// Blocks are made of ordinary cells, so they survive all of that.
	if getenv("TMUX") != "" || strings.HasPrefix(getenv("TERM"), "screen") {
		return Blocks
	}

	term := strings.ToLower(getenv("TERM"))
	program := strings.ToLower(getenv("TERM_PROGRAM"))

	switch {
	case strings.Contains(term, "kitty"), getenv("KITTY_WINDOW_ID") != "":
		return Kitty
	case program == "ghostty", getenv("GHOSTTY_RESOURCES_DIR") != "":
		// Ghostty implements the kitty protocol and no other image protocol.
		return Kitty
	case program == "wezterm", getenv("WEZTERM_PANE") != "":
		// WezTerm speaks both; kitty's is the better-specified of the two.
		return Kitty
	case getenv("KONSOLE_VERSION") != "":
		return Kitty
	case program == "iterm.app", program == "iterm2":
		return ITerm2
	case program == "vscode", program == "mintty":
		return ITerm2
	default:
		return Blocks
	}
}

// DetectEnv is Detect against the real environment.
func DetectEnv() Protocol { return Detect(os.Getenv) }
