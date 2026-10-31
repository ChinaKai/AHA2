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

	tile := brandPixel(pixels, 4, 16)
	if tile[3] != 255 || tile[0] < 30 || tile[0] > 60 || tile[1] < 10 || tile[1] > 40 || tile[2] > 30 {
		t.Fatalf("brand tile BGRA = %v, want opaque navy", tile)
	}

	// assets/aha-logo.svg draws a white double-A and a cyan crossbar. Sample
	// the left stroke and the crossbar, not the dark centre.
	white := brandPixel(pixels, 9, 15)
	if white[3] != 255 || white[0] < 235 || white[1] < 235 || white[2] < 235 {
		t.Fatalf("brand letter BGRA = %v, want opaque white", white)
	}
	cyan := brandPixel(pixels, 16, 17)
	if cyan[3] != 255 || cyan[0] < 235 || cyan[1] < 150 || cyan[1] > 220 || cyan[2] > 100 {
		t.Fatalf("brand crossbar BGRA = %v, want opaque cyan", cyan)
	}
}

func brandPixel(pixels []byte, x, y int) [4]byte {
	offset := (y*brandIconSize + x) * 4
	return [4]byte{pixels[offset], pixels[offset+1], pixels[offset+2], pixels[offset+3]}
}
