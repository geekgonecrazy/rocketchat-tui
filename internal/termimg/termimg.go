package termimg

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"

	// Registered for their decoders: DecodeConfig needs them to read the
	// dimensions we lay the image out from.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// ErrUndecodable means the bytes are not an image format we can read. WebP and
// AVIF land here: the standard library has no decoder and this package takes no
// dependencies to add one.
var ErrUndecodable = errors.New("termimg: unsupported image format")

// cellAspect is how many times taller a character cell is than it is wide.
//
// The true ratio depends on the font and is only knowable by asking the
// terminal for its pixel size, which not every terminal answers. Two is close
// enough for every common terminal font that the error is invisible, and it
// costs no round trip on startup.
const cellAspect = 2

// kittyChunk is the payload size kitty's protocol allows per escape sequence.
const kittyChunk = 4096

// Draw writes an image to w, scaled to fit within maxCols by maxRows character
// cells, and reports the cell box it actually occupies so the caller can place
// what comes after it.
//
// The cursor is left at the start of the line below the image.
func Draw(w io.Writer, protocol Protocol, data []byte, maxCols, maxRows int) (cols, rows int, err error) {
	if len(data) == 0 {
		return 0, 0, errors.New("termimg: no image data")
	}
	if maxCols < 1 || maxRows < 1 {
		return 0, 0, errors.New("termimg: no room to draw")
	}

	cols, rows = maxCols, maxRows
	if config, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		cols, rows = Fit(config.Width, config.Height, maxCols, maxRows)
	} else if protocol == Blocks {
		// The native protocols can be handed bytes we cannot read ourselves and
		// may still manage; painting cells requires actual pixels.
		return 0, 0, ErrUndecodable
	}

	switch protocol {
	case Kitty:
		err = drawKitty(w, data, cols, rows)
	case ITerm2:
		err = drawITerm2(w, data, cols, rows)
	default:
		err = drawBlocks(w, data, cols, rows)
	}
	if err != nil {
		return 0, 0, err
	}
	return cols, rows, nil
}

// Fit scales a pxW by pxH image into the largest cell box that fits within
// maxCols by maxRows without distorting it.
func Fit(pxW, pxH, maxCols, maxRows int) (cols, rows int) {
	if pxW <= 0 || pxH <= 0 {
		return maxCols, maxRows
	}

	cols = maxCols
	rows = (cols*pxH + (pxW*cellAspect)/2) / (pxW * cellAspect)
	if rows > maxRows {
		rows = maxRows
		cols = (rows*cellAspect*pxW + pxH/2) / pxH
	}
	return max(cols, 1), max(rows, 1)
}

// drawKitty emits the kitty graphics protocol: base64 PNG split across escape
// sequences, chained with m=1 until the final one.
func drawKitty(w io.Writer, data []byte, cols, rows int) error {
	png, err := asPNG(data)
	if err != nil {
		return err
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(png))

	var buf bytes.Buffer
	for first := true; len(encoded) > 0; first = false {
		chunk := encoded
		if len(chunk) > kittyChunk {
			chunk = chunk[:kittyChunk]
		}
		encoded = encoded[len(chunk):]

		buf.WriteString("\x1b_G")
		if first {
			// a=T places the image at the cursor immediately; f=100 says the
			// payload is PNG; q=2 silences the terminal's acknowledgements,
			// which would otherwise arrive on stdin and be read as keystrokes.
			fmt.Fprintf(&buf, "a=T,f=100,q=2,c=%d,r=%d,", cols, rows)
		}
		if len(encoded) > 0 {
			buf.WriteString("m=1")
		} else {
			buf.WriteString("m=0")
		}
		buf.WriteByte(';')
		buf.Write(chunk)
		buf.WriteString("\x1b\\")
	}

	// a=T leaves the cursor at the image's top-left corner, so step past it.
	fmt.Fprintf(&buf, "\x1b[%dB\r", rows)
	_, err = w.Write(buf.Bytes())
	return err
}

// drawITerm2 emits an iTerm2 inline image: the file, verbatim and base64'd, in
// one OSC 1337 sequence.
func drawITerm2(w io.Writer, data []byte, cols, rows int) error {
	var buf bytes.Buffer
	// Bare integers on width/height mean character cells. The terminal fits the
	// image inside that box itself, so it is authoritative about aspect ratio
	// and our cell-shape assumption only affects the box we ask for.
	fmt.Fprintf(&buf, "\x1b]1337;File=inline=1;size=%d;width=%d;height=%d;preserveAspectRatio=1:",
		len(data), cols, rows)
	buf.WriteString(base64.StdEncoding.EncodeToString(data))
	buf.WriteString("\a\r\n")
	_, err := w.Write(buf.Bytes())
	return err
}

// asPNG returns the image as PNG bytes, which is the only compressed format
// the kitty protocol accepts. Already-PNG data passes straight through.
func asPNG(data []byte) ([]byte, error) {
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return data, nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUndecodable, err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, decoded); err != nil {
		return nil, fmt.Errorf("termimg: re-encode as png: %w", err)
	}
	return buf.Bytes(), nil
}
