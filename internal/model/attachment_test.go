package model_test

import (
	"strings"
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

func TestAttachmentIsImage(t *testing.T) {
	cases := []struct {
		name       string
		attachment model.Attachment
		want       bool
	}{
		{"declared mime", model.Attachment{Source: "/file-upload/a/x", MIME: "image/png"}, true},
		{"non-image mime", model.Attachment{Source: "/file-upload/a/x.png", MIME: "application/pdf"}, false},
		{"extension when mime is missing", model.Attachment{Source: "/file-upload/a/shot.JPG"}, true},
		{"extension behind a query string", model.Attachment{Source: "/file-upload/a/shot.png?t=1"}, true},
		{"unknown extension", model.Attachment{Source: "/file-upload/a/notes.txt"}, false},
		{"no extension at all", model.Attachment{Source: "/file-upload/a/blob"}, false},
		{"nothing to fetch", model.Attachment{Title: "shot.png"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.attachment.IsImage(); got != tc.want {
				t.Errorf("IsImage() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAttachmentFilename(t *testing.T) {
	cases := []struct {
		name       string
		attachment model.Attachment
		want       string
	}{
		{"title is the upload name", model.Attachment{Title: "diagram.png", Source: "/file-upload/a/x"}, "diagram.png"},
		{"falls back to the source", model.Attachment{Source: "/file-upload/a/shot.png"}, "shot.png"},
		{"unescapes the source", model.Attachment{Source: "/file-upload/a/my%20shot.png"}, "my shot.png"},
		{"ignores the query string", model.Attachment{Source: "/file-upload/a/shot.png?download=1"}, "shot.png"},
		{"last resort", model.Attachment{Source: "/"}, "attachment"},

		// A title is arbitrary text somebody else typed. It names a file we are
		// about to create, so it must not be able to choose where.
		{"strips path separators", model.Attachment{Title: "../../etc/passwd", Source: "/f/a"}, "etcpasswd"},
		{"strips a leading dot", model.Attachment{Title: ".bashrc", Source: "/f/a"}, "bashrc"},
		// Newlines included: a name that wraps would break the status line it is
		// reported on as surely as a slash would break the path.
		{"strips control characters", model.Attachment{Title: "sh\x00ot\n.png", Source: "/f/a"}, "shot.png"},
		{"falls through when the title is only dots", model.Attachment{Title: "..", Source: "/f/a/real.png"}, "real.png"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.attachment.Filename()
			if got != tc.want {
				t.Errorf("Filename() = %q, want %q", got, tc.want)
			}
			if strings.ContainsAny(got, `/\`) {
				t.Errorf("Filename() = %q, which can escape the download directory", got)
			}
		})
	}
}

func TestFromRocketMessageCarriesUploadMetadata(t *testing.T) {
	converted := model.FromRocketMessage(rocket.Message{
		ID: "m1",
		Attachments: []rocket.Attachment{{
			Title:             "screenshot.png",
			TitleLink:         "/file-upload/abc/screenshot.png",
			TitleLinkDownload: true,
			ImageURL:          "/file-upload/abc/screenshot.png",
			ImageType:         "image/png",
			ImageSize:         2048,
		}},
	}, "self", "selfname")

	if len(converted.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(converted.Attachments))
	}
	got := converted.Attachments[0]
	if got.Source != "/file-upload/abc/screenshot.png" {
		t.Errorf("Source = %q", got.Source)
	}
	if got.MIME != "image/png" || got.Size != 2048 || !got.Upload {
		t.Errorf("upload metadata did not survive conversion: %+v", got)
	}
	if !got.IsImage() {
		t.Error("an uploaded png should be viewable")
	}
}

func TestFromRocketMessageTreatsLinkPreviewAsNotOurs(t *testing.T) {
	// An unfurled web page: a title link, but not a download, and the image is
	// somebody else's thumbnail rather than a file on our server.
	converted := model.FromRocketMessage(rocket.Message{
		ID: "m1",
		Attachments: []rocket.Attachment{{
			Title:       "Some Article",
			TitleLink:   "https://news.example.com/article",
			Description: "a summary",
		}},
	}, "self", "selfname")

	if len(converted.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(converted.Attachments))
	}
	got := converted.Attachments[0]
	if got.Upload {
		t.Error("a link preview is not an upload")
	}
	if got.Source != "" {
		t.Errorf("Source = %q, want empty: a preview's title link is a web page, not a file", got.Source)
	}
	if got.IsImage() {
		t.Error("a link preview with no image should not be viewable")
	}
	if got.Link != "https://news.example.com/article" {
		t.Errorf("the one-liner still needs the link, got %q", got.Link)
	}
}

func TestFromRocketMessageKeepsRemotePreviewImageViewable(t *testing.T) {
	// An unfurled page that did carry an image: showing it is fine, the point
	// is only that it is not an upload we own.
	converted := model.FromRocketMessage(rocket.Message{
		ID: "m1",
		Attachments: []rocket.Attachment{{
			Title:     "Some Article",
			TitleLink: "https://news.example.com/article",
			ImageURL:  "https://news.example.com/hero.jpg",
		}},
	}, "self", "selfname")

	got := converted.Attachments[0]
	if !got.IsImage() {
		t.Error("a preview image should still be viewable")
	}
	if got.Upload {
		t.Error("a preview image is not an upload")
	}
}
