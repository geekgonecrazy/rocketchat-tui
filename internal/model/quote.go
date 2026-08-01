package model

import (
	"net/url"
	"strings"
)

// Quoting in Rocket.Chat is a link, not a field. A client that wants to quote a
// message puts a permalink to it at the front of the reply and sends ordinary
// text; the server recognises the link as one of its own, resolves the message
// behind it, and hands every client back a quote attachment carrying the author
// and what they said. Nothing about the quote is stored on the message itself,
// which is why both halves live here: building the markup on the way out, and
// taking it back off the text on the way in.

// quoteLabel is the link text a quote carries. A single space is what the web
// client writes, and what makes the markup invisible wherever it is rendered as
// markdown: an empty label would be a link with nothing to click.
const quoteLabel = " "

// MessageLink is the permalink to a message, the same URL the web client copies
// from "Get link". It is empty when the room or message is unknown, because a
// link to nowhere quoted into a message would be worse than no quote at all.
//
// The route has to match the room's wire type rather than its Kind: teams and
// discussions are channels or groups underneath, and Kind cannot tell which.
func MessageLink(serverURL string, room Room, messageID string) string {
	base := strings.TrimSuffix(strings.TrimSpace(serverURL), "/")
	if base == "" || messageID == "" {
		return ""
	}
	// The slug is what a Rocket.Chat route addresses a room by, except for DMs,
	// which the server also accepts by room id. An id is the safer fallback for
	// anything else too: a room we know no slug for is still reachable by it.
	name := room.Name
	if name == "" {
		name = room.ID
	}
	if name == "" {
		return ""
	}
	return base + "/" + roomRoute(room) + "/" + url.PathEscape(name) +
		"?msg=" + url.QueryEscape(messageID)
}

// roomRoute is the path segment Rocket.Chat routes this room under.
func roomRoute(room Room) string {
	switch room.Type {
	case "d":
		return "direct"
	case "p":
		return "group"
	case "l":
		return "live"
	case "c":
		return "channel"
	}
	// No wire type cached — a room we have only ever seen through a realtime
	// push, say. Kind is the next best thing, and is right for everything but a
	// private team or discussion.
	switch room.Kind {
	case KindDirect:
		return "direct"
	case KindPrivate:
		return "group"
	case KindOmnichannel:
		return "live"
	default:
		return "channel"
	}
}

// QuoteMarkup is the prefix that turns a reply into a quote of messageID: a
// markdown link with a blank label, followed by the space that separates it from
// whatever the user typed. It is empty when no link can be built, so a quote that
// cannot be addressed degrades to an ordinary message rather than to a broken one.
func QuoteMarkup(serverURL string, room Room, messageID string) string {
	link := MessageLink(serverURL, room, messageID)
	if link == "" {
		return ""
	}
	return "[" + quoteLabel + "](" + link + ") "
}

// StripQuoteMarkup removes blank-label links — "[ ](url)" and "[](url)" — from a
// message body, which is how a quote reads once the server has turned it into an
// attachment: the link has already said everything it is going to say, and the
// attachment says it again in words.
//
// This is a rendering step and nothing more. The stored text keeps its markup, so
// editing a quoted message and saving it leaves the quote where it was.
func StripQuoteMarkup(text string) string {
	if !strings.Contains(text, "](") {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if width, ok := blankLink(text[i:]); ok {
			i += width
			// The space the markup was followed by belonged to it, not to the
			// message; leaving it behind would indent every quoted reply.
			for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
				i++
			}
			continue
		}
		b.WriteByte(text[i])
		i++
	}
	return strings.TrimLeft(b.String(), "\n")
}

// blankLink reports whether text starts with a markdown link whose label is
// blank, and how long it is.
func blankLink(text string) (int, bool) {
	if len(text) == 0 || text[0] != '[' {
		return 0, false
	}
	label := strings.IndexByte(text, ']')
	if label < 0 || strings.TrimSpace(text[1:label]) != "" {
		return 0, false
	}
	if label+1 >= len(text) || text[label+1] != '(' {
		return 0, false
	}
	// A URL cannot contain an unescaped ")" or whitespace, so the first of either
	// ends the link. Anything else is not a link and is left alone.
	for i := label + 2; i < len(text); i++ {
		switch text[i] {
		case ')':
			return i + 1, true
		case ' ', '\t', '\n':
			return 0, false
		}
	}
	return 0, false
}
