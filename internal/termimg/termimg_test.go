package termimg_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/termimg"
)

func TestDetectPicksProtocolFromEnvironment(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want termimg.Protocol
	}{
		{"kitty by TERM", map[string]string{"TERM": "xterm-kitty"}, termimg.Kitty},
		{"kitty by window id", map[string]string{"KITTY_WINDOW_ID": "1"}, termimg.Kitty},
		{"ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, termimg.Kitty},
		{"ghostty by resources", map[string]string{"GHOSTTY_RESOURCES_DIR": "/x"}, termimg.Kitty},
		{"wezterm", map[string]string{"TERM_PROGRAM": "WezTerm"}, termimg.Kitty},
		{"konsole", map[string]string{"KONSOLE_VERSION": "220400"}, termimg.Kitty},
		{"iterm2", map[string]string{"TERM_PROGRAM": "iTerm.app"}, termimg.ITerm2},
		{"vscode", map[string]string{"TERM_PROGRAM": "vscode"}, termimg.ITerm2},
		{"plain xterm", map[string]string{"TERM": "xterm-256color"}, termimg.Blocks},
		{"nothing at all", map[string]string{}, termimg.Blocks},

		// A multiplexer swallows graphics escapes, and TERM_PROGRAM leaks in
		// from the terminal tmux was launched under, so it must not be trusted.
		{"tmux over kitty", map[string]string{
			"TMUX": "/tmp/tmux-0/default", "TERM": "tmux-256color", "TERM_PROGRAM": "ghostty",
		}, termimg.Blocks},
		{"screen over kitty", map[string]string{
			"TERM": "screen.xterm-256color", "KITTY_WINDOW_ID": "1",
		}, termimg.Blocks},

		{"override wins over detection", map[string]string{
			"RCTUI_IMAGE_PROTOCOL": "blocks", "TERM": "xterm-kitty",
		}, termimg.Blocks},
		{"override forces kitty", map[string]string{
			"RCTUI_IMAGE_PROTOCOL": "kitty", "TMUX": "/tmp/tmux-0/default",
		}, termimg.Kitty},
		{"override is case insensitive", map[string]string{
			"RCTUI_IMAGE_PROTOCOL": " ITerm2 ",
		}, termimg.ITerm2},
		{"unknown override falls through to detection", map[string]string{
			"RCTUI_IMAGE_PROTOCOL": "sixel", "TERM": "xterm-kitty",
		}, termimg.Kitty},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := termimg.Detect(func(key string) string { return tc.env[key] })
			if got != tc.want {
				t.Errorf("Detect(%v) = %s, want %s", tc.env, got, tc.want)
			}
		})
	}
}

func TestFitPreservesAspectRatio(t *testing.T) {
	cases := []struct {
		name                       string
		pxW, pxH, maxCols, maxRows int
		wantCols, wantRows         int
	}{
		// A cell is twice as tall as it is wide, so a square image is twice as
		// wide in cells as it is tall.
		{"square is width limited", 100, 100, 40, 40, 40, 20},
		{"wide image", 200, 100, 40, 40, 40, 10},
		{"tall image runs out of rows first", 100, 400, 40, 10, 5, 10},
		{"exactly fills the box", 80, 80, 80, 40, 80, 40},
		{"never smaller than one cell", 1000, 1, 40, 40, 40, 1},
		{"unknown dimensions use the whole box", 0, 0, 30, 15, 30, 15},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows := termimg.Fit(tc.pxW, tc.pxH, tc.maxCols, tc.maxRows)
			if cols != tc.wantCols || rows != tc.wantRows {
				t.Errorf("Fit(%d, %d, %d, %d) = %d x %d, want %d x %d",
					tc.pxW, tc.pxH, tc.maxCols, tc.maxRows, cols, rows, tc.wantCols, tc.wantRows)
			}
			if cols > tc.maxCols || rows > tc.maxRows {
				t.Errorf("Fit returned %d x %d, which overflows %d x %d",
					cols, rows, tc.maxCols, tc.maxRows)
			}
		})
	}
}

func TestDrawKittyChunksPayload(t *testing.T) {
	// Noise rather than a gradient: a smooth image compresses down to a single
	// chunk and would not exercise the chaining at all.
	data := encodePNG(t, noise(400, 400))

	var out bytes.Buffer
	cols, rows, err := termimg.Draw(&out, termimg.Kitty, data, 80, 24)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if cols != 48 || rows != 24 {
		t.Errorf("drew a square image at %d x %d cells, want 48 x 24", cols, rows)
	}

	got := out.String()
	if !strings.HasPrefix(got, "\x1b_Ga=T,f=100,q=2,c=48,r=24,m=1;") {
		t.Errorf("first chunk header is wrong: %.60q", got)
	}
	if strings.Count(got, "\x1b_G") < 2 {
		t.Error("payload was not split across multiple escape sequences")
	}
	// Every chunk but the last continues; exactly one ends the transmission.
	if n := strings.Count(got, "m=0;"); n != 1 {
		t.Errorf("found %d terminating chunks, want exactly 1", n)
	}
	if !strings.Contains(got, "\x1b[24B\r") {
		t.Error("cursor was not moved below the image")
	}
}

func TestDrawKittyReencodesNonPNG(t *testing.T) {
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, gradient(64, 64), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}

	var out bytes.Buffer
	if _, _, err := termimg.Draw(&out, termimg.Kitty, jpegBuf.Bytes(), 40, 20); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	// f=100 is kitty's PNG format; a JPEG must have been converted, not passed
	// through, or the terminal would reject it.
	if !strings.Contains(out.String(), "f=100") {
		t.Error("expected the jpeg to be re-encoded as png")
	}
}

func TestDrawITerm2EmbedsOriginalBytes(t *testing.T) {
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, gradient(64, 64), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}

	var out bytes.Buffer
	if _, _, err := termimg.Draw(&out, termimg.ITerm2, jpegBuf.Bytes(), 40, 20); err != nil {
		t.Fatalf("Draw: %v", err)
	}

	got := out.String()
	if !strings.HasPrefix(got, "\x1b]1337;File=inline=1;") {
		t.Errorf("not an iTerm2 inline image: %.40q", got)
	}
	if !strings.Contains(got, "preserveAspectRatio=1") {
		t.Error("iTerm2 was not told to preserve the aspect ratio")
	}
	if !strings.HasSuffix(got, "\a\r\n") {
		t.Error("OSC sequence was not terminated")
	}
}

func TestDrawBlocksPaintsOneCellPerPixelPair(t *testing.T) {
	// Top half red, bottom half blue.
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for x := range 2 {
		img.Set(x, 0, color.RGBA{R: 0xff, A: 0xff})
		img.Set(x, 1, color.RGBA{B: 0xff, A: 0xff})
	}

	var out bytes.Buffer
	cols, rows, err := termimg.Draw(&out, termimg.Blocks, encodePNG(t, img), 4, 1)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if rows != 1 {
		t.Fatalf("drew %d rows, want 1", rows)
	}

	got := out.String()
	// One row of cells, each the upper half-block with red over blue.
	want := strings.Repeat("\x1b[38;2;255;0;0m\x1b[48;2;0;0;255m▀", cols) + "\x1b[0m\r\n"
	if got != want {
		t.Errorf("blocks output\n got %q\nwant %q", got, want)
	}
}

func TestDrawBlocksLeavesTransparentPixelsUnpainted(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 2))
	img.Set(0, 0, color.RGBA{}) // fully transparent
	img.Set(0, 1, color.RGBA{G: 0xff, A: 0xff})

	var out bytes.Buffer
	if _, _, err := termimg.Draw(&out, termimg.Blocks, encodePNG(t, img), 1, 1); err != nil {
		t.Fatalf("Draw: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "▄") {
		t.Errorf("expected a lower half-block for an opaque pixel under a transparent one: %q", got)
	}
	if strings.Contains(got, "48;2;") {
		t.Errorf("a transparent pixel should show the terminal background, not a colour: %q", got)
	}
}

func TestDrawBlocksUnpremultipliesColour(t *testing.T) {
	// Half-transparent pure red. Stored premultiplied, so a naive average would
	// report it as dark red rather than red seen through 50% alpha.
	img := image.NewRGBA(image.Rect(0, 0, 1, 2))
	img.Set(0, 0, color.RGBA{R: 0x80, A: 0x80})
	img.Set(0, 1, color.RGBA{R: 0x80, A: 0x80})

	var out bytes.Buffer
	if _, _, err := termimg.Draw(&out, termimg.Blocks, encodePNG(t, img), 1, 1); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if !strings.Contains(out.String(), "38;2;255;0;0m") {
		t.Errorf("expected full-strength red after unpremultiplying: %q", out.String())
	}
}

func TestDrawRejectsUndecodableImageForBlocks(t *testing.T) {
	_, _, err := termimg.Draw(&bytes.Buffer{}, termimg.Blocks, []byte("RIFF....WEBPVP8 "), 40, 20)
	if !errors.Is(err, termimg.ErrUndecodable) {
		t.Errorf("Draw of an unreadable format = %v, want ErrUndecodable", err)
	}
}

func TestDrawRejectsEmptyInputAndZeroSpace(t *testing.T) {
	data := encodePNG(t, gradient(8, 8))
	if _, _, err := termimg.Draw(&bytes.Buffer{}, termimg.Blocks, nil, 40, 20); err == nil {
		t.Error("expected an error for empty image data")
	}
	if _, _, err := termimg.Draw(&bytes.Buffer{}, termimg.Blocks, data, 0, 20); err == nil {
		t.Error("expected an error when there are no columns to draw in")
	}
	if _, _, err := termimg.Draw(&bytes.Buffer{}, termimg.Blocks, data, 40, 0); err == nil {
		t.Error("expected an error when there are no rows to draw in")
	}
}

func gradient(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 0xff})
		}
	}
	return img
}

// noise builds an image PNG cannot meaningfully compress, using a fixed
// sequence so the test stays deterministic.
func noise(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	state := uint32(1)
	next := func() uint8 {
		state = state*1664525 + 1013904223
		return uint8(state >> 24)
	}
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: next(), G: next(), B: next(), A: 0xff})
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
