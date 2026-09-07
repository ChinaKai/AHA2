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
		blueR = 0x1d
		blueG = 0x4e
		blueB = 0xd8
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
					if insideBrandLetter(px, py) {
						red += 255
						green += 255
						blue += 255
					} else {
						red += blueR
						green += blueG
						blue += blueB
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
		minimum = 1.25
		maximum = brandIconSize - minimum
		radius  = 6.25
	)
	nearestX := math.Max(minimum+radius, math.Min(x, maximum-radius))
	nearestY := math.Max(minimum+radius, math.Min(y, maximum-radius))
	deltaX := x - nearestX
	deltaY := y - nearestY
	return deltaX*deltaX+deltaY*deltaY <= radius*radius
}

func insideBrandLetter(x, y float64) bool {
	const strokeRadius = 1.65
	return pointSegmentDistanceSquared(x, y, 16, 6.6, 8.7, 25.3) <= strokeRadius*strokeRadius ||
		pointSegmentDistanceSquared(x, y, 16, 6.6, 23.3, 25.3) <= strokeRadius*strokeRadius ||
		pointSegmentDistanceSquared(x, y, 11.5, 18.7, 20.5, 18.7) <= 1.35*1.35
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
