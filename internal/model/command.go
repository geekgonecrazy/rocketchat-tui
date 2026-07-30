package model

import "strings"

// CommandScope is who runs a slash command, which is the only thing about a
// command the rest of the app has to reason about.
type CommandScope int

const (
	// ScopeClient is a command rctui implements itself because it acts on this
	// client: /exit ends the session, /upload picks a file off this machine.
	// No server has an opinion about these, so a server registration of the same
	// name never displaces one.
	ScopeClient CommandScope = iota
	// ScopeLocal is a command rctui implements over REST for servers that do not
	// offer it. The server's own version wins when there is one: it is the
	// authority on what /leave means on that deployment.
	ScopeLocal
	// ScopeServer is a command the server advertises and executes.
	ScopeServer
	// ScopeUnsupported is a command the server advertises but nobody here can
	// run: it is flagged clientOnly, so commands.run will not execute it, and
	// rctui has no implementation of its own. Kept so that invoking it can say
	// so, but never offered as a completion.
	ScopeUnsupported
)

// Command is one entry in the slash command registry, wherever it came from.
type Command struct {
	// Name is the command without its leading slash.
	Name string
	// Params is the usage hint shown beside the name, e.g. "@username".
	Params string
	// Description is one line of prose. Server descriptions are sometimes i18n
	// keys rather than sentences, which is the server's business, not ours.
	Description string
	Scope       CommandScope
}

// Offerable reports whether the completer should list this command. Anything
// that cannot run is not a candidate: offering it would be offering a failure.
func (c Command) Offerable() bool { return c.Scope != ScopeUnsupported }

// Usage renders "/name <params>" for the completer and for error messages.
func (c Command) Usage() string {
	if c.Params == "" {
		return "/" + c.Name
	}
	return "/" + c.Name + " " + c.Params
}

// ParseCommand splits a composed line into a slash command and its arguments.
//
// Only a single line qualifies, and only one whose first character is a slash
// followed by command characters and then a space or the end of the line. That
// rules out the things people actually type: "/usr/bin/env is missing" opens
// with a slash but "usr" is followed by another slash, and a multi-line message
// that happens to start with one is a message.
func ParseCommand(text string) (name, params string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") || strings.ContainsRune(trimmed, '\n') {
		return "", "", false
	}

	rest := trimmed[1:]
	end := 0
	for end < len(rest) && isCommandByte(rest[end]) {
		end++
	}
	if end == 0 {
		return "", "", false
	}
	// The name has to end the word. Anything else — "/usr/bin", "/2:30" — is text
	// that merely opens with a slash.
	if end < len(rest) && rest[end] != ' ' && rest[end] != '\t' {
		return "", "", false
	}
	return strings.ToLower(rest[:end]), strings.TrimSpace(rest[end:]), true
}

// isCommandByte reports whether c can appear in a slash command's name.
// Rocket.Chat's own commands use letters, digits, dashes and underscores.
func isCommandByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '_':
		return true
	default:
		return false
	}
}

// FindCommand looks a name up in a registry, case-insensitively.
func FindCommand(commands []Command, name string) (Command, bool) {
	for _, command := range commands {
		if strings.EqualFold(command.Name, name) {
			return command, true
		}
	}
	return Command{}, false
}
