package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
)

// queuedFile writes a file and resolves it the way the composer would.
func queuedFile(t *testing.T, dir, name string, content []byte) app.Upload {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	upload, err := app.NewUpload(path)
	if err != nil {
		t.Fatalf("NewUpload(%s): %v", name, err)
	}
	return upload
}

func TestNewUploadResolvesWhatTheComposerNeedsToShow(t *testing.T) {
	dir := t.TempDir()
	upload := queuedFile(t, dir, "shot.png", fakerc.FilePNG)

	if upload.Name != "shot.png" {
		t.Errorf("Name = %q, want shot.png", upload.Name)
	}
	if upload.Size != int64(len(fakerc.FilePNG)) {
		t.Errorf("Size = %d, want %d", upload.Size, len(fakerc.FilePNG))
	}
	if upload.MIME != "image/png" || !upload.IsImage() {
		t.Errorf("MIME = %q (image=%v), want an image", upload.MIME, upload.IsImage())
	}
}

// Resolution happens when the file is picked, not when it is sent, so the
// reason a path is no good reaches the user while they are still typing it.
func TestNewUploadExplainsWhatItCannotSend(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		path string
		want string
	}{
		{"empty", "  ", "give a path"},
		{"missing", filepath.Join(dir, "nope.png"), "no such file"},
		{"directory", dir, "is a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := app.NewUpload(tc.path); err == nil {
				t.Fatal("expected a refusal")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestExpandPathHandlesTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	cases := map[string]string{
		"~":             home,
		"~/shots/a.png": filepath.Join(home, "shots/a.png"),
		// Only a leading "~" is a home reference; anything else is a name.
		"./~/a.png":  "./~/a.png",
		"/tmp/a.png": "/tmp/a.png",
		"~notauser":  "~notauser",
	}
	for input, want := range cases {
		got, err := app.ExpandPath(input)
		if err != nil {
			t.Fatalf("ExpandPath(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("ExpandPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSendUploadsFilesInOrderWithTextOnTheFirst(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")

	dir := t.TempDir()
	h.core.Send(app.SendRequest{RoomID: "room-1", Text: "look at these", Uploads: []app.Upload{
		queuedFile(t, dir, "one.png", fakerc.FilePNG),
		queuedFile(t, dir, "two.txt", []byte("words")),
		queuedFile(t, dir, "three.png", fakerc.FilePNG),
	}})

	uploads := waitFor(t, "three uploads", func() ([]fakerc.Upload, bool) {
		got := h.server.Uploads()
		return got, len(got) == 3
	})

	for i, want := range []string{"one.png", "two.txt", "three.png"} {
		if uploads[i].Filename != want {
			t.Errorf("upload %d = %q, want %q", i, uploads[i].Filename, want)
		}
	}
	if uploads[0].Text != "look at these" {
		t.Errorf("first upload text = %q, want the message", uploads[0].Text)
	}
	for _, upload := range uploads[1:] {
		if upload.Text != "" {
			t.Errorf("%s carried %q; the text belongs to one message only",
				upload.Filename, upload.Text)
		}
	}
}

func TestSendUploadsIntoAThread(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")

	h.core.Send(app.SendRequest{
		RoomID:   "room-1",
		ThreadID: "thread-1",
		Text:     "in the thread",
		Uploads:  []app.Upload{queuedFile(t, t.TempDir(), "reply.png", fakerc.FilePNG)},
	})

	uploads := waitFor(t, "the upload", func() ([]fakerc.Upload, bool) {
		got := h.server.Uploads()
		return got, len(got) == 1
	})
	if uploads[0].ThreadID != "thread-1" {
		t.Errorf("tmid = %q, want thread-1", uploads[0].ThreadID)
	}
}

// A server that refuses the file must not also swallow what the user wrote:
// they watched the text leave the composer, so it has to land somewhere.
func TestSendKeepsTheMessageWhenEveryUploadIsRefused(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")
	h.server.RejectUpload = true

	h.core.Send(app.SendRequest{
		RoomID:  "room-1",
		Text:    "here is the thing",
		Uploads: []app.Upload{queuedFile(t, t.TempDir(), "banned.exe", []byte("MZ"))},
	})

	sent := waitFor(t, "the message posted anyway", func() ([]fakerc.SentMessage, bool) {
		got := h.server.SentMessages()
		return got, len(got) == 1
	})
	if sent[0].Text != "here is the thing" {
		t.Errorf("posted %q, want the user's text", sent[0].Text)
	}
	if len(h.server.Uploads()) != 0 {
		t.Error("nothing should have been stored")
	}

	notice := waitFor(t, "a failure notice", func() (app.Notice, bool) {
		for _, event := range h.snapshot() {
			if n, ok := event.(app.Notice); ok && n.IsErr && strings.Contains(n.Text, "banned.exe") {
				return n, true
			}
		}
		return app.Notice{}, false
	})
	if !strings.Contains(notice.Text, "could not upload") {
		t.Errorf("notice = %q, want it to name the failure", notice.Text)
	}
}

// One bad file in a queue of three is not a reason to drop the other two.
func TestSendContinuesPastAFailedFile(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")

	dir := t.TempDir()
	good := queuedFile(t, dir, "good.png", fakerc.FilePNG)
	// Resolved while it existed, deleted before the send: the same shape as a
	// file that goes away between attaching and pressing enter.
	gone := queuedFile(t, dir, "gone.png", fakerc.FilePNG)
	if err := os.Remove(gone.Path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	alsoGood := queuedFile(t, dir, "also-good.png", fakerc.FilePNG)

	h.core.Send(app.SendRequest{
		RoomID:  "room-1",
		Text:    "three of them",
		Uploads: []app.Upload{gone, good, alsoGood},
	})

	uploads := waitFor(t, "the two surviving uploads", func() ([]fakerc.Upload, bool) {
		got := h.server.Uploads()
		return got, len(got) == 2
	})
	if uploads[0].Filename != "good.png" || uploads[1].Filename != "also-good.png" {
		t.Errorf("uploaded %q and %q, want the two that still exist",
			uploads[0].Filename, uploads[1].Filename)
	}
	// The text was queued behind a file that failed, so it moves to the first
	// one that actually made it rather than being lost with the first attempt.
	if uploads[0].Text != "three of them" {
		t.Errorf("text = %q, want it carried to the first successful upload", uploads[0].Text)
	}
	if len(h.server.SentMessages()) != 0 {
		t.Error("the text went with a file, so no plain message should have been posted")
	}
}

func TestSendFallsBackToTheLegacyUploadRoute(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")
	h.server.NoMediaRoute = true

	h.core.Send(app.SendRequest{
		RoomID:  "room-1",
		Text:    "old server",
		Uploads: []app.Upload{queuedFile(t, t.TempDir(), "shot.png", fakerc.FilePNG)},
	})

	uploads := waitFor(t, "the upload", func() ([]fakerc.Upload, bool) {
		got := h.server.Uploads()
		return got, len(got) == 1
	})
	if uploads[0].Route != "upload" {
		t.Errorf("route = %q, want the legacy route", uploads[0].Route)
	}
	if uploads[0].Text != "old server" {
		t.Errorf("text = %q, want it carried as a form field", uploads[0].Text)
	}
}

// The uploaded message has to reach the timeline the same way a typed one does,
// or the file appears only after the next resync.
func TestUploadedFileAppearsInTheTimeline(t *testing.T) {
	h := newHarness(t)
	h.seedRoom("room-1", "general", 0, 0, time.Now().Add(-time.Hour))
	h.start()
	h.waitForRoomInSidebar("room-1")
	h.core.OpenRoom("room-1")

	h.core.Send(app.SendRequest{
		RoomID:  "room-1",
		Text:    "shipped",
		Uploads: []app.Upload{queuedFile(t, t.TempDir(), "shot.png", fakerc.FilePNG)},
	})

	waitFor(t, "the upload in the timeline", func() (bool, bool) {
		timeline, ok := h.lastTimeline("room-1")
		if !ok {
			return false, false
		}
		for _, message := range timeline.Messages {
			for _, attachment := range message.Attachments {
				if attachment.Filename() == "shot.png" && attachment.IsImage() {
					return true, true
				}
			}
		}
		return false, false
	})
}
