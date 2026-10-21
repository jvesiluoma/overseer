package app

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

// Icon draws the tray icon (a monitor with a second screen peeking out) as an .ico file with one
// PNG image. active tints it, so a glance at the tray shows that someone is connected.
func Icon(active bool) []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	ink := color.NRGBA{0xe8, 0xea, 0xee, 0xff}
	fill := color.NRGBA{0x2b, 0x31, 0x3b, 0xff}
	if active {
		fill = color.NRGBA{0x25, 0x63, 0xeb, 0xff}
	}
	rect := func(x0, y0, x1, y1 int, c color.NRGBA) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	// Second screen behind, offset to the right.
	rect(12, 3, 31, 18, ink)
	rect(14, 5, 29, 16, fill)
	// Main screen.
	rect(1, 8, 22, 24, ink)
	rect(3, 10, 20, 22, fill)
	// Stand.
	rect(10, 24, 13, 28, ink)
	rect(6, 28, 17, 30, ink)

	var pngData bytes.Buffer
	_ = png.Encode(&pngData, img)
	var ico bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&ico, le, [3]uint16{0, 1, 1}) // reserved, type icon, one image
	ico.Write([]byte{size, size, 0, 0})            // width, height, palette, reserved
	_ = binary.Write(&ico, le, [2]uint16{1, 32})   // planes, bits per pixel
	_ = binary.Write(&ico, le, [2]uint32{uint32(pngData.Len()), 6 + 16})
	ico.Write(pngData.Bytes())
	return ico.Bytes()
}
