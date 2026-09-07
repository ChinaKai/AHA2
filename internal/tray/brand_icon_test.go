package tray

import (
	"encoding/binary"
	"testing"
)

func TestBrandIconResource(t *testing.T) {
	resource := brandIconResource()
	maskStride := ((brandIconSize + 31) / 32) * 4
	wantLength := 40 + brandIconSize*brandIconSize*4 + maskStride*brandIconSize
	if len(resource) != wantLength {
		t.Fatalf("resource length = %d, want %d", len(resource), wantLength)
	}
	if got := binary.LittleEndian.Uint32(resource[0:4]); got != 40 {
		t.Fatalf("header size = %d, want 40", got)
	}
	if got := binary.LittleEndian.Uint32(resource[4:8]); got != brandIconSize {
		t.Fatalf("width = %d, want %d", got, brandIconSize)
	}
	if got := binary.LittleEndian.Uint32(resource[8:12]); got != brandIconSize*2 {
		t.Fatalf("combined height = %d, want %d", got, brandIconSize*2)
	}
	if got := binary.LittleEndian.Uint16(resource[12:14]); got != 1 {
		t.Fatalf("planes = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(resource[14:16]); got != 32 {
		t.Fatalf("bit depth = %d, want 32", got)
	}
}

func TestBrandIconArtwork(t *testing.T) {
	pixels := renderBrandIcon(brandIconSize)
	corner := brandPixel(pixels, 0, 0)
	if corner[3] != 0 {
		t.Fatalf("corner alpha = %d, want transparent", corner[3])
	}

	blue := brandPixel(pixels, 4, 16)
	if blue[3] != 255 || blue[0] < 190 || blue[1] < 50 || blue[2] > 80 {
		t.Fatalf("brand field BGRA = %v, want opaque blue", blue)
	}

	white := brandPixel(pixels, 16, 8)
	if white[3] != 255 || white[0] < 235 || white[1] < 235 || white[2] < 235 {
		t.Fatalf("brand letter BGRA = %v, want opaque white", white)
	}
}

func brandPixel(pixels []byte, x, y int) [4]byte {
	offset := (y*brandIconSize + x) * 4
	return [4]byte{pixels[offset], pixels[offset+1], pixels[offset+2], pixels[offset+3]}
}
