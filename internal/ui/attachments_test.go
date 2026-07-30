package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// chatWithAttachments puts one message carrying the given attachments on screen
// with the cursor sitting on it.
func chatWithAttachments(t *testing.T, attachments ...model.Attachment) chatModel {
	t.Helper()
	m := newTestChat(t)
	m = event(m, app.RoomsUpdated{Rooms: sampleRooms()})
	m = event(m, app.TimelineUpdated{
		RoomID: "r1",
		Room:   model.Room{ID: "r1", DisplayName: "general", Kind: model.KindChannel},
		Messages: []model.Message{{
			ID: "msg-1", Username: "alice", Author: "Alice", Text: "have a look",
			At: time.Now(), Attachments: attachments,
		}},
	})
	m.focus = focusMessages
	m.msgCursor = 0
	return m
}

func imageAttachment(name string) model.Attachment {
	return model.Attachment{
		Title:  name,
		Source: "/file-upload/" + name,
		MIME:   "image/png",
		Upload: true,
	}
}

func TestViewKeyReportsWhenThereIsNothingToShow(t *testing.T) {
	cases := []struct {
		name        string
		attachments []model.Attachment
		wantNotice  string
	}{
		{"no attachments at all", nil, "no attachment on this message"},
		{"not an image", []model.Attachment{{
			Title: "report.pdf", Source: "/file-upload/report.pdf", MIME: "application/pdf", Upload: true,
		}}, "not an image"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := chatWithAttachments(t, tc.attachments...)
			m, _ = m.Update(press("v"))

			if !strings.Contains(m.notice, tc.wantNotice) {
				t.Errorf("notice = %q, want it to mention %q", m.notice, tc.wantNotice)
			}
			if m.pending.active {
				t.Error("nothing should have been fetched")
			}
		})
	}
}

func TestViewKeyOnANonImageOffersSaveAndOpen(t *testing.T) {
	m := chatWithAttachments(t, model.Attachment{
		Title: "report.pdf", Source: "/file-upload/report.pdf", MIME: "application/pdf", Upload: true,
	})
	m, _ = m.Update(press("v"))

	// A dead end is not good enough: the file is still reachable, and the
	// message has to say how.
	if !strings.Contains(m.notice, "s to save") || !strings.Contains(m.notice, "o to open") {
		t.Errorf("notice = %q, want it to point at the s and o keys", m.notice)
	}
}

func TestViewKeyRequestsTheFirstImage(t *testing.T) {
	m := chatWithAttachments(t,
		model.Attachment{Title: "notes.txt", Source: "/file-upload/notes.txt", Upload: true},
		imageAttachment("second.png"),
	)
	m, _ = m.Update(press("v"))

	if !m.pending.active {
		t.Fatal("expected a fetch to be in flight")
	}
	// Index is the position within the message's own list, not within the
	// filtered image list, because that is what identifies it on the way back.
	if m.pending.index != 1 {
		t.Errorf("pending index = %d, want 1 (the png, skipping the text file)", m.pending.index)
	}
	if m.pending.action != actionView {
		t.Errorf("pending action = %v, want actionView", m.pending.action)
	}
}

func TestSaveAndOpenKeysActOnNonImagesToo(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want attachmentAction
	}{{"s", actionSave}, {"o", actionOpen}} {
		t.Run(tc.key, func(t *testing.T) {
			m := chatWithAttachments(t, model.Attachment{
				Title: "report.pdf", Source: "/file-upload/report.pdf", Upload: true,
			})
			m, _ = m.Update(press(tc.key))

			if !m.pending.active {
				t.Fatalf("%q should have started a fetch even for a non-image", tc.key)
			}
			if m.pending.action != tc.want {
				t.Errorf("action = %v, want %v", m.pending.action, tc.want)
			}
		})
	}
}

func TestStaleFetchResultIsIgnored(t *testing.T) {
	m := chatWithAttachments(t, imageAttachment("first.png"), imageAttachment("second.png"))
	m, _ = m.Update(press("v"))

	// A result for an attachment nobody is waiting on any more: the user moved
	// on while it was in flight.
	m, cmd := m.attachmentFetched(app.AttachmentFetched{
		MessageID: "msg-1", Index: 1,
		Attachment: imageAttachment("second.png"),
		Path:       "/nonexistent/second.png",
	})
	if cmd != nil {
		t.Error("a stale result should not open a viewer or set a notice")
	}
	if !m.pending.active {
		t.Error("the fetch that is still outstanding should stay pending")
	}
}

func TestFetchErrorIsReported(t *testing.T) {
	m := chatWithAttachments(t, imageAttachment("shot.png"))
	m, _ = m.Update(press("v"))

	m, _ = m.attachmentFetched(app.AttachmentFetched{
		MessageID: "msg-1", Index: 0,
		Attachment: imageAttachment("shot.png"),
		Err:        os.ErrPermission,
	})

	if !m.noticeErr || m.notice == "" {
		t.Errorf("expected an error notice, got %q (isErr=%v)", m.notice, m.noticeErr)
	}
	if m.pending.active {
		t.Error("a failed fetch should clear the pending request")
	}
}

func TestSaveActionWritesToTheDownloadDirectory(t *testing.T) {
	m := chatWithAttachments(t, imageAttachment("shot.png"))
	m, _ = m.Update(press("s"))

	cached := filepath.Join(t.TempDir(), "cached.png")
	if err := os.WriteFile(cached, []byte("image bytes"), 0o644); err != nil {
		t.Fatalf("seed cache file: %v", err)
	}

	m, _ = m.attachmentFetched(app.AttachmentFetched{
		MessageID: "msg-1", Index: 0,
		Attachment: imageAttachment("shot.png"),
		Path:       cached,
	})

	saved := filepath.Join(m.downloadDir, "shot.png")
	written, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("expected the attachment at %s: %v", saved, err)
	}
	if string(written) != "image bytes" {
		t.Errorf("saved %q, want the downloaded bytes", written)
	}
	if !strings.Contains(m.notice, "saved to") {
		t.Errorf("notice = %q, want it to report where the file went", m.notice)
	}
}

func TestNeighbourImageWrapsAndSkipsNonImages(t *testing.T) {
	m := chatWithAttachments(t,
		imageAttachment("a.png"),
		model.Attachment{Title: "notes.txt", Source: "/file-upload/notes.txt", Upload: true},
		imageAttachment("c.png"),
	)

	next, ok := m.neighbourImage("msg-1", 0, 1)
	if !ok || next.index != 2 {
		t.Errorf("next after index 0 = %+v (ok=%v), want the image at index 2", next, ok)
	}
	// Wrapping: forward from the last image returns to the first.
	wrapped, ok := m.neighbourImage("msg-1", 2, 1)
	if !ok || wrapped.index != 0 {
		t.Errorf("next after the last image = %+v (ok=%v), want a wrap to index 0", wrapped, ok)
	}
	back, ok := m.neighbourImage("msg-1", 0, -1)
	if !ok || back.index != 2 {
		t.Errorf("previous from index 0 = %+v (ok=%v), want a wrap to index 2", back, ok)
	}
}

func TestNeighbourImageIsNoOpForASingleImage(t *testing.T) {
	m := chatWithAttachments(t, imageAttachment("only.png"))
	if _, ok := m.neighbourImage("msg-1", 0, 1); ok {
		t.Error("there is nothing to cycle to with one image")
	}
	if position := m.viewerPosition("msg-1", 0); position != "" {
		t.Errorf("position = %q, want empty when there is nothing to cycle", position)
	}
}

func TestViewerPositionCountsOnlyImages(t *testing.T) {
	m := chatWithAttachments(t,
		imageAttachment("a.png"),
		model.Attachment{Title: "notes.txt", Source: "/file-upload/notes.txt", Upload: true},
		imageAttachment("c.png"),
	)
	if got := m.viewerPosition("msg-1", 2); got != "2 of 2" {
		t.Errorf("position = %q, want %q", got, "2 of 2")
	}
}

func TestSaveAttachmentNeverOverwrites(t *testing.T) {
	dir := t.TempDir()

	first, err := saveAttachment(dir, "shot.png", []byte("one"))
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := saveAttachment(dir, "shot.png", []byte("two"))
	if err != nil {
		t.Fatalf("second save: %v", err)
	}

	if first == second {
		t.Fatal("the second save reused the first path and would have overwritten it")
	}
	if got := filepath.Base(second); got != "shot-1.png" {
		t.Errorf("second path = %q, want the name suffixed before the extension", got)
	}
	original, err := os.ReadFile(first)
	if err != nil || string(original) != "one" {
		t.Errorf("the first file was disturbed: %q, %v", original, err)
	}
}

func TestSaveAttachmentCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not", "there", "yet")

	if _, err := saveAttachment(dir, "shot.png", []byte("x")); err != nil {
		t.Fatalf("save into a missing directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shot.png")); err != nil {
		t.Errorf("file was not written: %v", err)
	}
}

func TestSaveAttachmentRefusesWithoutADirectory(t *testing.T) {
	if _, err := saveAttachment("", "shot.png", []byte("x")); err == nil {
		t.Error("expected an error with no download directory configured")
	}
}
