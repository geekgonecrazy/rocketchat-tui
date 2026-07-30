package app_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// lastFetch returns the most recent attachment result.
func (h *harness) lastFetch() (app.AttachmentFetched, bool) {
	events := h.snapshot()
	for i := len(events) - 1; i >= 0; i-- {
		if fetched, ok := events[i].(app.AttachmentFetched); ok {
			return fetched, true
		}
	}
	return app.AttachmentFetched{}, false
}

func TestFetchAttachmentCachesToDisk(t *testing.T) {
	// Redirect the cache so the test never writes into the real user's.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	h := newHarness(t)
	h.start()

	attachment := model.Attachment{
		Title:  "shot.png",
		Source: "/file-upload/abc123/shot.png",
		MIME:   "image/png",
		Upload: true,
	}
	h.core.FetchAttachment("msg-1", 0, attachment)

	fetched := waitFor(t, "the attachment to arrive", func() (app.AttachmentFetched, bool) {
		return h.lastFetch()
	})

	if fetched.Err != nil {
		t.Fatalf("fetch failed: %v", fetched.Err)
	}
	if fetched.MessageID != "msg-1" || fetched.Index != 0 {
		t.Errorf("result identifies %s/%d, want msg-1/0", fetched.MessageID, fetched.Index)
	}
	if fetched.MIME != "image/png" {
		t.Errorf("MIME = %q, want image/png", fetched.MIME)
	}

	written, err := os.ReadFile(fetched.Path)
	if err != nil {
		t.Fatalf("cached file: %v", err)
	}
	if !bytes.Equal(written, fakerc.FilePNG) {
		t.Error("the cached bytes are not what the server sent")
	}
	if server := h.server.FileRequests(); server != 1 {
		t.Errorf("server saw %d requests, want 1", server)
	}
}

func TestFetchAttachmentSecondTimeIsACacheHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	h := newHarness(t)
	h.start()

	attachment := model.Attachment{
		Title:  "shot.png",
		Source: "/file-upload/abc123/shot.png",
		MIME:   "image/png",
		Upload: true,
	}

	h.core.FetchAttachment("msg-1", 0, attachment)
	first := waitFor(t, "the first fetch", func() (app.AttachmentFetched, bool) {
		return h.lastFetch()
	})

	h.core.FetchAttachment("msg-1", 0, attachment)
	waitFor(t, "the second fetch", func() (app.AttachmentFetched, bool) {
		// Two results now, and both name the same cached file.
		count := 0
		for _, event := range h.snapshot() {
			if _, ok := event.(app.AttachmentFetched); ok {
				count++
			}
		}
		fetched, ok := h.lastFetch()
		return fetched, ok && count >= 2
	})

	if got := h.server.FileRequests(); got != 1 {
		t.Errorf("server saw %d requests, want 1: the second view should come from the cache", got)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Errorf("the cached file went missing: %v", err)
	}
}

func TestFetchAttachmentLeavesNoPartialFileOnFailure(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	h := newHarness(t)
	h.start()

	// A path the fake server does not serve as a file.
	h.core.FetchAttachment("msg-1", 0, model.Attachment{
		Title:  "missing.png",
		Source: "/api/v1/nope/missing.png",
		Upload: true,
	})

	fetched := waitFor(t, "the failed fetch", func() (app.AttachmentFetched, bool) {
		return h.lastFetch()
	})
	if fetched.Err == nil {
		t.Fatal("expected an error for a file the server will not serve")
	}
	if fetched.Path != "" {
		t.Errorf("a failed fetch reported a path: %q", fetched.Path)
	}

	// A half-written file would be treated as a cache hit forever afterwards.
	entries, err := os.ReadDir(filepath.Join(cacheHome, "rctui", "attachments"))
	if err == nil && len(entries) > 0 {
		t.Errorf("failed fetch left %d file(s) behind in the cache", len(entries))
	}
}

func TestFetchAttachmentWithNothingToFetchReportsIt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	h := newHarness(t)
	h.start()

	h.core.FetchAttachment("msg-1", 0, model.Attachment{Title: "a link preview"})

	fetched := waitFor(t, "the rejected fetch", func() (app.AttachmentFetched, bool) {
		return h.lastFetch()
	})
	if fetched.Err == nil {
		t.Error("an attachment with no source should report why nothing happened")
	}
}
