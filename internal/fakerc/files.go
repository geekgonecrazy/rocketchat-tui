package fakerc

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// FilePNG is the body every /file-upload/ request answers with: a small but
// genuine PNG, so a test exercises real decoding rather than a placeholder
// string that would never survive an image renderer.
var FilePNG = buildPNG()

func buildPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{R: uint8(x * 32), G: uint8(y * 32), B: 0x40, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic("fakerc: encode test png: " + err.Error())
	}
	return buf.Bytes()
}
