package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func TestIconPNGDecodes(t *testing.T) {
	image, err := png.Decode(bytes.NewReader(iconPNG()))
	if err != nil {
		t.Fatalf("decode the tray icon: %v", err)
	}
	bounds := image.Bounds()
	if bounds.Dx() != 64 || bounds.Dy() != 64 {
		t.Fatalf("icon is %dx%d, want 64x64", bounds.Dx(), bounds.Dy())
	}
	// The middle is the white record dot and a corner is outside the rounded
	// square, so a blank or square icon fails here.
	if _, _, _, alpha := image.At(0, 0).RGBA(); alpha != 0 {
		t.Fatal("the corner is opaque, so the rounded shape was not drawn")
	}
	r, g, b, alpha := image.At(32, 32).RGBA()
	if alpha == 0 || r>>8 < 0xf0 || g>>8 < 0xf0 || b>>8 < 0xf0 {
		t.Fatalf("centre pixel is %d,%d,%d alpha %d, want the white dot", r>>8, g>>8, b>>8, alpha>>8)
	}
}

// Windows loads the icon from a file, so its header has to be right: a wrong
// length or offset gives a tray with no icon and no error to explain it.
func TestIconICOHeader(t *testing.T) {
	data := iconICO()
	if len(data) < 22 {
		t.Fatalf("icon is %d bytes, too short for its headers", len(data))
	}
	var directory struct {
		Reserved, Type, Count uint16
	}
	if err := binary.Read(bytes.NewReader(data[:6]), binary.LittleEndian, &directory); err != nil {
		t.Fatal(err)
	}
	if directory.Reserved != 0 || directory.Type != 1 || directory.Count != 1 {
		t.Fatalf("directory = %+v, want reserved 0, type 1, one image", directory)
	}
	var entry struct {
		Width, Height, Colours, Reserved uint8
		Planes, BitsPerPixel             uint16
		Bytes, Offset                    uint32
	}
	if err := binary.Read(bytes.NewReader(data[6:22]), binary.LittleEndian, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Width != 32 || entry.Height != 32 || entry.BitsPerPixel != 32 {
		t.Fatalf("entry = %+v, want a 32x32 image at 32 bits per pixel", entry)
	}
	if int(entry.Offset)+int(entry.Bytes) != len(data) {
		t.Fatalf("image runs to byte %d of a %d byte file", int(entry.Offset)+int(entry.Bytes), len(data))
	}
	// The image is the header, the pixels, and the mask, in that order.
	const headerSize, pixels, mask = 40, 32 * 32 * 4, 32 / 8 * 32
	if entry.Bytes != headerSize+pixels+mask {
		t.Fatalf("image is %d bytes, want %d", entry.Bytes, headerSize+pixels+mask)
	}
}
