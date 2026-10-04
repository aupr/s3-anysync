// Command genicon draws the placeholder s3-anysync icon (a white cloud with
// two sync arrows on a blue rounded square) as a PNG.
//
//	go run ./tools/genicon -size 1024 -o cmd/gui/build/appicon.png
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

func main() {
	size := flag.Int("size", 1024, "edge length in pixels")
	out := flag.String("o", "appicon.png", "output PNG")
	flag.Parse()
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(f, render(*size)); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
}

type rgba struct{ r, g, b, a float64 }

// shade returns the colour at a point given in unit coordinates (0..1).
func shade(x, y float64) rgba {
	// Rounded square background, vertical blue gradient.
	const r = 0.18
	dx := math.Max(math.Max(r-x, x-(1-r)), 0)
	dy := math.Max(math.Max(r-y, y-(1-r)), 0)
	if dx*dx+dy*dy > r*r {
		return rgba{}
	}
	top, bottom := rgba{0.15, 0.39, 0.92, 1}, rgba{0.12, 0.25, 0.69, 1}
	c := rgba{top.r + (bottom.r-top.r)*y, top.g + (bottom.g-top.g)*y, top.b + (bottom.b-top.b)*y, 1}

	inCircle := func(cx, cy, rad float64) bool { return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad }
	cloud := inCircle(0.35, 0.59, 0.11) || inCircle(0.47, 0.49, 0.15) || inCircle(0.64, 0.46, 0.17) ||
		inCircle(0.77, 0.59, 0.11) || (x >= 0.35 && x <= 0.77 && y >= 0.48 && y <= 0.70)
	if !cloud {
		return c
	}
	white := rgba{1, 1, 1, 1}
	// Two arcs around the cloud's centre with arrow heads: the sync symbol.
	const cx, cy, rad, thick = 0.56, 0.585, 0.085, 0.026
	d := math.Hypot(x-cx, y-cy)
	ang := math.Atan2(y-cy, x-cx) * 180 / math.Pi // -180..180, 0 = right, 90 = down
	onRing := math.Abs(d-rad) <= thick/2
	arcA := ang >= -150 && ang <= -20 // upper arc
	arcB := ang >= 30 && ang <= 160   // lower arc
	if onRing && (arcA || arcB) {
		return c
	}
	if inTriangle(x, y, headAt(cx, cy, rad, -20, 1)) || inTriangle(x, y, headAt(cx, cy, rad, 160, 1)) {
		return c
	}
	return white
}

type tri [3][2]float64

// headAt returns an arrow head at angle deg on the ring, pointing along the
// direction of increasing angle.
func headAt(cx, cy, rad, deg, dir float64) tri {
	a := deg * math.Pi / 180
	px, py := cx+rad*math.Cos(a), cy+rad*math.Sin(a)
	tx, ty := -math.Sin(a)*dir, math.Cos(a)*dir // tangent
	nx, ny := math.Cos(a), math.Sin(a)          // normal
	const l, w = 0.05, 0.035
	return tri{{px + tx*l, py + ty*l}, {px + nx*w, py + ny*w}, {px - nx*w, py - ny*w}}
}

func inTriangle(x, y float64, t tri) bool {
	sign := func(a, b [2]float64) float64 { return (x-b[0])*(a[1]-b[1]) - (a[0]-b[0])*(y-b[1]) }
	d1, d2, d3 := sign(t[0], t[1]), sign(t[1], t[2]), sign(t[2], t[0])
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

func render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var acc rgba
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c := shade((float64(px)+(float64(sx)+0.5)/ss)/float64(size), (float64(py)+(float64(sy)+0.5)/ss)/float64(size))
					acc.r += c.r * c.a
					acc.g += c.g * c.a
					acc.b += c.b * c.a
					acc.a += c.a
				}
			}
			n := float64(ss * ss)
			if acc.a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(acc.r / acc.a * 255)), G: uint8(math.Round(acc.g / acc.a * 255)),
				B: uint8(math.Round(acc.b / acc.a * 255)), A: uint8(math.Round(acc.a / n * 255)),
			})
		}
	}
	return img
}
