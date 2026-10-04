package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

var iconColors = map[iconKind]color.RGBA{
	iconIdle:      {0x2e, 0x9e, 0x5b, 0xff}, // green
	iconSyncing:   {0x2f, 0x6f, 0xd6, 0xff}, // blue
	iconAttention: {0xe0, 0x8e, 0x1b, 0xff}, // orange
	iconError:     {0xd0, 0x31, 0x31, 0xff}, // red
}

// iconPNG draws a 32×32 filled circle in the state's colour.
func iconPNG(k iconKind) []byte {
	const size = 32
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
