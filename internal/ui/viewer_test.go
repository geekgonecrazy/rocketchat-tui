package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/termimg"
)

const (
	enterAltScreen = "\x1b[?1049h"
	leaveAltScreen = "\x1b[?1049l"
)

// newTestViewer wires a viewer to a scripted keyboard and a buffer for a screen.
func newTestViewer(t *testing.T, keys string, protocol termimg.Protocol) (*attachmentViewer, *bytes.Buffer) {
	t.Helper()

	image := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			image.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), A: 0xff})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	screen := &bytes.Buffer{}
	viewer := &attachmentViewer{
		attachment:  model.Attachment{Title: "shot.png", Source: "/file-upload/shot.png", MIME: "image/png"},
		path:        path,
		protocol:    protocol,
		downloadDir: t.TempDir(),
		width:       60,
		height:      20,
	}
	viewer.SetStdin(strings.NewReader(keys))
	viewer.SetStdout(screen)
	return viewer, screen
}

func TestViewerEntersAndLeavesItsOwnAltScreen(t *testing.T) {
	viewer, screen := newTestViewer(t, "q", termimg.Blocks)
	if err := viewer.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := screen.String()
	if !strings.HasPrefix(out, enterAltScreen) {
		t.Error("viewer did not push its own alt screen, so the chat below it would be destroyed")
	}
	if !strings.HasSuffix(out, leaveAltScreen) {
		t.Error("viewer did not leave the alt screen, so the chat would not come back")
	}
	if !strings.Contains(out, "\x1b[?25l") || !strings.Contains(out, "\x1b[?25h") {
		t.Error("the cursor should be hidden while drawing and restored on the way out")
	}
	if viewer.outcome != viewerClosed {
		t.Errorf("outcome = %v, want viewerClosed", viewer.outcome)
	}
}

func TestViewerRestoresTheTerminalWhenTheImageIsUnreadable(t *testing.T) {
	viewer, screen := newTestViewer(t, "q", termimg.Blocks)
	if err := os.WriteFile(viewer.path, []byte("not an image"), 0o644); err != nil {
		t.Fatalf("clobber image: %v", err)
	}

	if err := viewer.Run(); err != nil {
		// A draw failure must not fail the command: Bubbletea skips its own
		// terminal restore when an ExecCommand returns an error.
		t.Fatalf("Run should report draw failures through the caption, got %v", err)
	}
	if !strings.HasSuffix(screen.String(), leaveAltScreen) {
		t.Error("the alt screen must be left even when the image cannot be drawn")
	}
	if !strings.Contains(screen.String(), "press o to open") {
		t.Errorf("an undrawable image should say what to do instead:\n%q", screen.String())
	}
}

func TestViewerCaptionDescribesTheImage(t *testing.T) {
	viewer, screen := newTestViewer(t, "q", termimg.Blocks)
	if err := viewer.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := screen.String()
	for _, want := range []string{"shot.png", "16×16", "q back", "s save", "o open"} {
		if !strings.Contains(out, want) {
			t.Errorf("caption missing %q", want)
		}
	}
	if strings.Contains(out, "n/p cycle") {
		t.Error("a lone image should not advertise cycling")
	}
}

func TestViewerOffersCyclingOnlyWithSiblings(t *testing.T) {
	viewer, screen := newTestViewer(t, "q", termimg.Blocks)
	viewer.position = "2 of 3"
	if err := viewer.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := screen.String()
	if !strings.Contains(out, "n/p cycle") {
		t.Error("expected cycling keys when the message carries more than one image")
	}
	if !strings.Contains(out, "2 of 3") {
		t.Error("expected the position in the caption")
	}
}

func TestViewerKeysMapToOutcomes(t *testing.T) {
	cases := []struct {
		keys string
		want viewerOutcome
	}{
		{"q", viewerClosed},
		{"\x1b", viewerClosed}, // esc
		{"\r", viewerClosed},   // enter
		{"z", viewerClosed},    // anything unrecognised is an exit
		{"n", viewerNext},
		{" ", viewerNext},
		{"\x1b[C", viewerNext}, // right arrow
		{"p", viewerPrev},
		{"\x1b[D", viewerPrev}, // left arrow
	}

	for _, tc := range cases {
		t.Run(strings.ToValidUTF8(tc.keys, "esc"), func(t *testing.T) {
			viewer, _ := newTestViewer(t, tc.keys, termimg.Blocks)
			if err := viewer.Run(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if viewer.outcome != tc.want {
				t.Errorf("%q gave outcome %v, want %v", tc.keys, viewer.outcome, tc.want)
			}
		})
	}
}

func TestViewerSaveKeepsTheViewerOpen(t *testing.T) {
	viewer, screen := newTestViewer(t, "sq", termimg.Blocks)
	if err := viewer.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	saved := filepath.Join(viewer.downloadDir, "shot.png")
	if _, err := os.Stat(saved); err != nil {
		t.Fatalf("expected the image saved to %s: %v", saved, err)
	}
	if !strings.Contains(viewer.notice, "saved to") {
		t.Errorf("notice = %q, want it to report the path", viewer.notice)
	}
	// Saving repaints rather than exiting, so the screen was cleared twice.
	if n := strings.Count(screen.String(), "\x1b[2J"); n < 2 {
		t.Errorf("screen was painted %d times, want a repaint after saving", n)
	}
	if viewer.outcome != viewerClosed {
		t.Errorf("outcome = %v, want the viewer to have exited on q afterwards", viewer.outcome)
	}
}

func TestViewerExitsOnEndOfInput(t *testing.T) {
	// A closed stdin must not spin: the read returns immediately and forever.
	viewer, _ := newTestViewer(t, "", termimg.Blocks)
	if err := viewer.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if viewer.outcome != viewerClosed {
		t.Errorf("outcome = %v, want viewerClosed", viewer.outcome)
	}
}

func TestIndentCentresBlocksPerLine(t *testing.T) {
	drawn := []byte("AAA\r\nBBB\r\n")
	got := string(indent(drawn, 2, termimg.Blocks))
	if got != "  AAA\r\n  BBB\r\n" {
		t.Errorf("indent = %q, want each row given its own left margin", got)
	}
}

func TestIndentMovesTheCursorOnceForGraphicsProtocols(t *testing.T) {
	drawn := []byte("\x1b_Ga=T;payload\x1b\\")
	got := string(indent(drawn, 5, termimg.Kitty))
	if !strings.HasPrefix(got, "\x1b[5C") {
		t.Errorf("indent = %q, want a single cursor-forward before the image", got)
	}
	if strings.Count(got, "\x1b[5C") != 1 {
		t.Error("a placed image needs the cursor moved once, not per line")
	}
}

func TestIndentIsANoOpWithoutRoom(t *testing.T) {
	drawn := []byte("AAA\r\n")
	if got := string(indent(drawn, 0, termimg.Blocks)); got != "AAA\r\n" {
		t.Errorf("indent with no padding changed the output: %q", got)
	}
}
