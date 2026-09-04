package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// The tray icon is drawn here rather than kept as a file beside the source. It
// is a handful of shapes, a generated file would be a build artefact in the
// repository, and go:embed would need it present in every clean clone.

var (
	iconGreen = color.NRGBA{R: 0x52, G: 0xb7, B: 0x88, A: 0xff}
	iconWhite = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
)

// drawIcon renders a record dot on a rounded square at the requested size,
// supersampled so that the curves survive being shown at 16 pixels.
func drawIcon(size int) *image.NRGBA {
	const sample = 4
	large := size * sample
	canvas := image.NewNRGBA(image.Rect(0, 0, large, large))
	radius := float64(large) * 0.22
	dot := float64(large) * 0.26
	centre := float64(large) / 2
	for y := 0; y < large; y++ {
		for x := 0; x < large; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if !insideRoundedSquare(px, py, float64(large), radius) {
				continue
			}
			shade := iconGreen
			if math.Hypot(px-centre, py-centre) <= dot {
				shade = iconWhite
			}
			canvas.SetNRGBA(x, y, shade)
		}
	}
	return downsample(canvas, size, sample)
}

func insideRoundedSquare(x, y, side, radius float64) bool {
	// Distance to the nearest corner centre, clamped so that the straight edges
	// are simply inside.
	dx := math.Max(math.Max(radius-x, x-(side-radius)), 0)
	dy := math.Max(math.Max(radius-y, y-(side-radius)), 0)
	return math.Hypot(dx, dy) <= radius
}

func downsample(source *image.NRGBA, size, sample int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	area := float64(sample * sample)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < sample; sy++ {
				for sx := 0; sx < sample; sx++ {
					c := source.NRGBAAt(x*sample+sx, y*sample+sy)
					// Weight the colour by coverage, so a transparent pixel does
					// not drag the edge colour towards black.
					alpha := float64(c.A) / 255
					r += float64(c.R) * alpha
					g += float64(c.G) * alpha
					b += float64(c.B) * alpha
					a += float64(c.A)
				}
			}
			coverage := a / 255
			if coverage == 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{R: uint8(r/coverage + 0.5), G: uint8(g/coverage + 0.5), B: uint8(b/coverage + 0.5), A: uint8(a/area + 0.5)})
		}
	}
	return out
}

// iconPNG is the icon macOS and Linux trays accept. It is rendered larger than
// the menu bar needs so that a high-resolution display scales it down rather
// than up.
func iconPNG() []byte {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, drawIcon(64)); err != nil {
		return nil
	}
	return buffer.Bytes()
}

// iconICO is the icon Windows requires. It holds one uncompressed 32-bit image
// rather than an embedded PNG, because the PNG form of an ICO is understood
// only by the newer of the two ways Windows loads icon files.
func iconICO() []byte {
	const size = 32
	source := drawIcon(size)
	pixels := new(bytes.Buffer)
	// A DIB is stored bottom-up, as blue, green, red, alpha.
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			c := source.NRGBAAt(x, y)
			pixels.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	// The AND mask is unused because the alpha channel carries transparency,
	// but the format still requires the rows, padded to four bytes.
	mask := make([]byte, size/8*size)

	header := new(bytes.Buffer)
	binary.Write(header, binary.LittleEndian, uint32(40))    // BITMAPINFOHEADER size
	binary.Write(header, binary.LittleEndian, int32(size))   // width
	binary.Write(header, binary.LittleEndian, int32(size*2)) // height of image plus mask
	binary.Write(header, binary.LittleEndian, uint16(1))     // planes
	binary.Write(header, binary.LittleEndian, uint16(32))    // bits per pixel
	binary.Write(header, binary.LittleEndian, uint32(0))     // uncompressed
	binary.Write(header, binary.LittleEndian, uint32(pixels.Len()+len(mask)))
	header.Write(make([]byte, 16)) // resolution, palette counts: all unused here

	body := append(append(header.Bytes(), pixels.Bytes()...), mask...)
	out := new(bytes.Buffer)
	binary.Write(out, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(out, binary.LittleEndian, uint16(1)) // an icon rather than a cursor
	binary.Write(out, binary.LittleEndian, uint16(1)) // one image
	out.Write([]byte{size, size, 0, 0})
	binary.Write(out, binary.LittleEndian, uint16(1))  // planes
	binary.Write(out, binary.LittleEndian, uint16(32)) // bits per pixel
	binary.Write(out, binary.LittleEndian, uint32(len(body)))
	binary.Write(out, binary.LittleEndian, uint32(6+16)) // offset past both headers
	out.Write(body)
	return out.Bytes()
}
