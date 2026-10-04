package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"runtime"
)

var iconColors = map[iconKind]color.RGBA{
	iconIdle:      {0x2e, 0x9e, 0x5b, 0xff}, // green
	iconSyncing:   {0x2f, 0x6f, 0xd6, 0xff}, // blue
	iconAttention: {0xe0, 0x8e, 0x1b, 0xff}, // orange
	iconError:     {0xd0, 0x31, 0x31, 0xff}, // red
}

// iconPNG draws a 32×32 filled circle in the state's colour.
func iconPNG(k iconKind) []byte {
	const size = iconSize
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	c := iconColors[k]
	r := float64(size)/2 - 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-float64(size)/2+0.5, float64(y)-float64(size)/2+0.5
			if dx*dx+dy*dy <= r*r {
				img.SetRGBA(x, y, c)
			}
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

const iconSize = 32

// iconICO wraps a PNG in a single-image ICO container (Windows' systray
// loads icons with LoadImage, which needs ICO, not PNG).
func iconICO(png []byte, size int) []byte {
	dim := byte(size) // 0 means 256
	if size >= 256 {
		dim = 0
	}
	b := make([]byte, 0, 22+len(png))
	b = append(b, 0, 0, 1, 0, 1, 0)             // ICONDIR: reserved, type=icon, count=1
	b = append(b, dim, dim, 0, 0)               // width, height, colorCount, reserved
	b = binary.LittleEndian.AppendUint16(b, 1)  // planes
	b = binary.LittleEndian.AppendUint16(b, 32) // bitCount
	b = binary.LittleEndian.AppendUint32(b, uint32(len(png)))
	b = binary.LittleEndian.AppendUint32(b, 22) // image offset
	return append(b, png...)
}

// iconBytesFor returns the icon in the format the OS tray expects.
func iconBytesFor(goos string, k iconKind) []byte {
	p := iconPNG(k)
	if goos == "windows" {
		return iconICO(p, iconSize)
	}
	return p
}

// iconBytes returns the icon for the running OS.
func iconBytes(k iconKind) []byte { return iconBytesFor(runtime.GOOS, k) }
