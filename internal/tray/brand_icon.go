package tray

import (
	"encoding/binary"
	"math"
)

const (
	brandIconSize       = 32
	brandIconSampleRate = 4
)

// brandIconResource returns an RT_ICON-compatible 32-bit DIB. Keeping the
// artwork in code makes the tray icon available in every Windows build without
// a runtime asset beside the executable.
func brandIconResource() []byte {
	pixels := renderBrandIcon(brandIconSize)
	maskStride := ((brandIconSize + 31) / 32) * 4
	resource := make([]byte, 40+brandIconSize*brandIconSize*4+maskStride*brandIconSize)

	binary.LittleEndian.PutUint32(resource[0:4], 40)
	binary.LittleEndian.PutUint32(resource[4:8], brandIconSize)
	binary.LittleEndian.PutUint32(resource[8:12], brandIconSize*2) // color + mask
	binary.LittleEndian.PutUint16(resource[12:14], 1)
	binary.LittleEndian.PutUint16(resource[14:16], 32)
	binary.LittleEndian.PutUint32(resource[20:24], brandIconSize*brandIconSize*4)

	colorOffset := 40
	maskOffset := colorOffset + brandIconSize*brandIconSize*4
	for y := 0; y < brandIconSize; y++ {
		sourceRow := y * brandIconSize * 4
		targetRow := colorOffset + (brandIconSize-1-y)*brandIconSize*4
		copy(resource[targetRow:targetRow+brandIconSize*4], pixels[sourceRow:sourceRow+brandIconSize*4])
		for x := 0; x < brandIconSize; x++ {
			if pixels[sourceRow+x*4+3] == 0 {
				maskRow := maskOffset + (brandIconSize-1-y)*maskStride
				resource[maskRow+x/8] |= 0x80 >> uint(x%8)
			}
		}
	}
	return resource
}

// renderBrandIcon renders the AHA2 "A" mark as premultiplied BGRA pixels.
// Supersampling keeps the letter and rounded corners legible at 16px trays.
func renderBrandIcon(size int) []byte {
	if size <= 0 {
		return nil
	}
	const (
		tileR = 15
		tileG = 23
		tileB = 42
		inkR  = 248
		inkG  = 250
		inkB  = 252
		cyanR = 56
		cyanG = 189
		cyanB = 248
	)
	samples := brandIconSampleRate * brandIconSampleRate
	pixels := make([]byte, size*size*4)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var alpha, red, green, blue int
			for sampleY := 0; sampleY < brandIconSampleRate; sampleY++ {
				for sampleX := 0; sampleX < brandIconSampleRate; sampleX++ {
					px := (float64(x) + (float64(sampleX)+0.5)/brandIconSampleRate) * brandIconSize / float64(size)
					py := (float64(y) + (float64(sampleY)+0.5)/brandIconSampleRate) * brandIconSize / float64(size)
					if !insideRoundedSquare(px, py) {
						continue
					}
					alpha += 255
					switch {
					case insideBrandCrossbar(px, py):
						red += cyanR
						green += cyanG
						blue += cyanB
					case insideBrandLetter(px, py):
						red += inkR
						green += inkG
						blue += inkB
					default:
						red += tileR
						green += tileG
						blue += tileB
					}
				}
			}
			offset := (y*size + x) * 4
			pixels[offset] = byte(blue / samples)
			pixels[offset+1] = byte(green / samples)
			pixels[offset+2] = byte(red / samples)
			pixels[offset+3] = byte(alpha / samples)
		}
	}
	return pixels
}

func insideRoundedSquare(x, y float64) bool {
	const (
		halfSize = brandIconSize / 2.0
		radius   = brandIconSize * 14.0 / 64.0
	)
	// Signed distance to the rounded tile in assets/aha-logo.svg.
	dx := math.Abs(x-halfSize) - (halfSize - radius)
	dy := math.Abs(y-halfSize) - (halfSize - radius)
	outsideX := math.Max(dx, 0)
	outsideY := math.Max(dy, 0)
	outside := math.Hypot(outsideX, outsideY)
	inside := math.Min(math.Max(dx, dy), 0)
	return outside+inside-radius <= 0
}

// insideBrandLetter follows the stroked double-A path in assets/aha-logo.svg.
// The path is M12 48 22 17 32 48 42 17 52 48 in a 64px viewBox, stroked at
// 5.2px with round caps and joins.
func insideBrandLetter(x, y float64) bool {
	const (
		scale        = brandIconSize / 64.0
		strokeRadius = 5.2 * scale / 2
	)
	points := [][4]float64{
		{12, 48, 22, 17},
		{22, 17, 32, 48},
		{32, 48, 42, 17},
		{42, 17, 52, 48},
	}
	for _, line := range points {
		if pointSegmentDistanceSquared(x, y, line[0]*scale, line[1]*scale, line[2]*scale, line[3]*scale) <= strokeRadius*strokeRadius {
			return true
		}
	}
	return false
}

func insideBrandCrossbar(x, y float64) bool {
	const (
		scale        = brandIconSize / 64.0
		strokeRadius = 4.8 * scale / 2
	)
	return pointSegmentDistanceSquared(x, y, 16*scale, 35.5*scale, 48*scale, 35.5*scale) <= strokeRadius*strokeRadius
}

func pointSegmentDistanceSquared(px, py, ax, ay, bx, by float64) float64 {
	deltaX := bx - ax
	deltaY := by - ay
	lengthSquared := deltaX*deltaX + deltaY*deltaY
	if lengthSquared == 0 {
		deltaX = px - ax
		deltaY = py - ay
		return deltaX*deltaX + deltaY*deltaY
	}
	position := ((px-ax)*deltaX + (py-ay)*deltaY) / lengthSquared
	position = math.Max(0, math.Min(1, position))
	deltaX = px - (ax + position*deltaX)
	deltaY = py - (ay + position*deltaY)
	return deltaX*deltaX + deltaY*deltaY
}
