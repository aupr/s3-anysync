package main

import (
	"image/color"
	"testing"
)

func TestIconShape(t *testing.T) {
	size := 256.0
	img := render(int(size))
	if b := img.Bounds(); b.Dx() != 256 || b.Dy() != 256 {
		t.Fatalf("size %v", b)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatal("corner must be transparent (rounded square)")
	}
	c := color.NRGBAModel.Convert(img.At(128, 40)).(color.NRGBA)
	if c.A != 255 || c.B < 150 {
		t.Fatalf("top centre should be opaque blue background, got %v", c)
	}
	w := color.NRGBAModel.Convert(img.At(int(size*0.36), int(size*0.62))).(color.NRGBA)
	if w.R < 230 || w.G < 230 || w.B < 230 {
		t.Fatalf("cloud area should be white, got %v", w)
	}
}
