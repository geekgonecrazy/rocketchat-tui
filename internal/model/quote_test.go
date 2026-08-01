package model_test

import (
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

func TestMessageLinkRoutesByWireType(t *testing.T) {
	cases := []struct {
		name string
		room model.Room
		want string
	}{
		{
			"channel",
			model.Room{ID: "r1", Type: "c", Name: "general", Kind: model.KindChannel},
			"https://chat.example.com/channel/general?msg=abc",
		},
		{
			"private group",
			model.Room{ID: "r2", Type: "p", Name: "secret-plans", Kind: model.KindPrivate},
			"https://chat.example.com/group/secret-plans?msg=abc",
		},
		{
			"direct message",
			model.Room{ID: "r3", Type: "d", Name: "alice", Kind: model.KindDirect},
			"https://chat.example.com/direct/alice?msg=abc",
		},
		// The two the Kind alone would get wrong: both are "p" underneath while
		// being neither KindPrivate nor anything that implies a group route.
		{
			"private team",
			model.Room{ID: "r4", Type: "p", Name: "eng", Kind: model.KindTeam, TeamMain: true},
			"https://chat.example.com/group/eng?msg=abc",
		},
		{
			"private discussion",
			model.Room{ID: "r5", Type: "p", Name: "auth-spike", Kind: model.KindDiscussion, ParentRoomID: "r4"},
			"https://chat.example.com/group/auth-spike?msg=abc",
		},
		{
			"no slug falls back to the room id",
			model.Room{ID: "r6", Type: "d", Kind: model.KindDirect},
			"https://chat.example.com/direct/r6?msg=abc",
		},
		{
			"a name that needs escaping",
			model.Room{ID: "r7", Type: "c", Name: "a b", Kind: model.KindChannel},
			"https://chat.example.com/channel/a%20b?msg=abc",
		},
		// A room pushed over realtime before any sync has no type letter yet, so
		// the kind has to answer — right for everything but a private team.
		{
			"kind stands in when the type is unknown",
			model.Room{ID: "r8", Name: "random", Kind: model.KindPrivate},
			"https://chat.example.com/group/random?msg=abc",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := model.MessageLink("https://chat.example.com", tc.room, "abc"); got != tc.want {
				t.Errorf("MessageLink() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A link that cannot be built must come back empty rather than half-formed: the
// UI refuses to start the quote on that, where a broken link would be sent.
func TestMessageLinkRefusesWhatItCannotAddress(t *testing.T) {
	room := model.Room{ID: "r1", Type: "c", Name: "general"}
	cases := []struct {
		name      string
		serverURL string
		room      model.Room
		messageID string
	}{
		{"no server", "", room, "abc"},
		{"no message", "https://chat.example.com", room, ""},
		{"no room at all", "https://chat.example.com", model.Room{Type: "c"}, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := model.MessageLink(tc.serverURL, tc.room, tc.messageID); got != "" {
				t.Errorf("MessageLink() = %q, want empty", got)
			}
		})
	}
}

func TestMessageLinkKeepsAServerSubpath(t *testing.T) {
	room := model.Room{ID: "r1", Type: "c", Name: "general"}
	want := "https://example.com/rocketchat/channel/general?msg=abc"
	if got := model.MessageLink("https://example.com/rocketchat/", room, "abc"); got != want {
		t.Errorf("MessageLink() = %q, want %q", got, want)
	}
}

func TestQuoteMarkupIsTheWebClientsForm(t *testing.T) {
	room := model.Room{ID: "r1", Type: "c", Name: "general"}
	want := "[ ](https://chat.example.com/channel/general?msg=abc) "
	if got := model.QuoteMarkup("https://chat.example.com", room, "abc"); got != want {
		t.Errorf("QuoteMarkup() = %q, want %q", got, want)
	}
	if got := model.QuoteMarkup("", room, "abc"); got != "" {
		t.Errorf("QuoteMarkup() with no server = %q, want empty", got)
	}
}

func TestStripQuoteMarkup(t *testing.T) {
	link := "https://chat.example.com/channel/general?msg=abc"
	cases := []struct {
		name string
		text string
		want string
	}{
		{"our own form", "[ ](" + link + ") sure, let's do that", "sure, let's do that"},
		{"the empty-label form", "[](" + link + ") sure", "sure"},
		{"nothing but the quote", "[ ](" + link + ") ", ""},
		{"two quotes stacked", "[ ](" + link + ") [ ](" + link + ") both of these", "both of these"},
		{"a quote on its own line", "[ ](" + link + ")\nmy answer", "my answer"},
		{"no quote at all", "just a message", "just a message"},

		// A link somebody wrote is theirs to keep: it has a label, so it renders
		// as something, and stripping it would lose text the author typed.
		{"a labelled link", "see [the docs](https://example.com)", "see [the docs](https://example.com)"},
		{"brackets that are not a link", "[WIP] still working", "[WIP] still working"},
		{"an unclosed link", "[ ](https://example.com", "[ ](https://example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := model.StripQuoteMarkup(tc.text); got != tc.want {
				t.Errorf("StripQuoteMarkup(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// The quote arrives as an attachment with a message_link and no file behind it,
// which is what tells it apart from an upload and from an unfurled preview.
func TestQuoteAttachmentConverts(t *testing.T) {
	converted, ok := model.FromRocketAttachment(rocket.Attachment{
		AuthorName:  "Alice",
		Text:        "can we ship friday?",
		MessageLink: "https://chat.example.com/channel/general?msg=abc",
	})
	if !ok {
		t.Fatal("a quote attachment was dropped as having nothing to show")
	}
	if !converted.IsQuote() {
		t.Error("attachment with a message_link does not report IsQuote")
	}
	if converted.Title != "Alice" || converted.Text != "can we ship friday?" {
		t.Errorf("quote = %q / %q, want the author and what they said",
			converted.Title, converted.Text)
	}
	// Nothing to view, save or open: a quote is words, not a file.
	if converted.Source != "" || converted.Upload {
		t.Errorf("quote looks like a file: source %q, upload %v", converted.Source, converted.Upload)
	}

	upload, _ := model.FromRocketAttachment(rocket.Attachment{
		Title: "shot.png", TitleLink: "/file-upload/a/shot.png", TitleLinkDownload: true,
	})
	if upload.IsQuote() {
		t.Error("an uploaded file reports itself as a quote")
	}
}
