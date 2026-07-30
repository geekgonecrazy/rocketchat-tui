package termimg

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"io"
)

// The half-block trick: one character cell shows two pixels stacked, by drawing
// ▀ with the upper pixel as the foreground colour and the lower as the
// background. That doubles vertical resolution and, with a cell being about
// twice as tall as it is wide, makes the pixels roughly square.
const (
	upperHalf = "▀"
	lowerHalf = "▄"
	// alphaCutoff is where a pixel stops being drawn and shows the terminal
	// background instead, so a transparent PNG does not arrive as a black box.
	alphaCutoff = 0x80
)

// drawBlocks paints the image as coloured half-blocks in a cols by rows box.
func drawBlocks(w io.Writer, data []byte, cols, rows int) error {
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUndecodable, err)
	}

	pixels := resample(decoded, cols, rows*2)
	out := bufio.NewWriter(w)

	for y := 0; y < pixels.height; y += 2 {
		for x := 0; x < pixels.width; x++ {
			top := pixels.at(x, y)
			bottom := pixels.at(x, y+1)

			switch {
			case !top.visible && !bottom.visible:
				out.WriteString("\x1b[0m ")
			case top.visible && !bottom.visible:
				fmt.Fprintf(out, "\x1b[0m\x1b[38;2;%d;%d;%dm%s", top.r, top.g, top.b, upperHalf)
			case !top.visible && bottom.visible:
				fmt.Fprintf(out, "\x1b[0m\x1b[38;2;%d;%d;%dm%s", bottom.r, bottom.g, bottom.b, lowerHalf)
			default:
				fmt.Fprintf(out, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm%s",
					top.r, top.g, top.b, bottom.r, bottom.g, bottom.b, upperHalf)
			}
		}
		// Reset before the newline, or the last cell's background colour runs
		// to the right edge of the terminal.
		out.WriteString("\x1b[0m\r\n")
	}
	return out.Flush()
}

// cell is one sampled pixel, already flattened to 8 bits per channel.
type cell struct {
	r, g, b uint8
	visible bool
}

// pixmap is a resampled image: row-major, width by height.
type pixmap struct {
	width, height int
	cells         []cell
}

func (p pixmap) at(x, y int) cell {
	if x < 0 || y < 0 || x >= p.width || y >= p.height {
		return cell{}
	}
	return p.cells[y*p.width+x]
}

// resample scales src to width by height by averaging over the source rectangle
// that maps to each destination pixel.
//
// Averaging rather than point-sampling matters here: the target is a few dozen
// cells wide, so a photo is being reduced by a factor of thirty or more, and
// nearest-neighbour at that ratio throws away almost every pixel and turns fine
// detail — text in a screenshot especially — into noise.
func resample(src image.Image, width, height int) pixmap {
	out := pixmap{width: max(width, 1), height: max(height, 1)}
	out.cells = make([]cell, out.width*out.height)

	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return out
	}

	for y := range out.height {
		y0 := bounds.Min.Y + y*srcH/out.height
		y1 := bounds.Min.Y + (y+1)*srcH/out.height
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := range out.width {
			x0 := bounds.Min.X + x*srcW/out.width
			x1 := bounds.Min.X + (x+1)*srcW/out.width
			if x1 <= x0 {
				x1 = x0 + 1
			}
			out.cells[y*out.width+x] = average(src, x0, y0, x1, y1)
		}
	}
	return out
}

// average is the mean colour of a source rectangle.
//
// It accumulates the premultiplied values RGBA() returns, which is what makes
// the result correct at the edges of a transparent region: a fully transparent
// pixel contributes nothing rather than dragging the average toward whatever
// colour happens to be stored behind its zero alpha.
func average(src image.Image, x0, y0, x1, y1 int) cell {
	var sumR, sumG, sumB, sumA uint64
	count := uint64((x1 - x0) * (y1 - y0))
	if count == 0 {
		return cell{}
	}

	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, a := src.At(x, y).RGBA()
			sumR += uint64(r)
			sumG += uint64(g)
			sumB += uint64(b)
			sumA += uint64(a)
		}
	}

	alpha := sumA / count
	if alpha>>8 < alphaCutoff {
		return cell{}
	}
	// Undo the premultiplication so the colour is the surface's own, not the
	// surface faded toward black by its own transparency.
	return cell{
		r: uint8(sumR / count * 0xff / alpha),
		g: uint8(sumG / count * 0xff / alpha),
		b: uint8(sumB / count * 0xff / alpha),

		visible: true,
	}
}
