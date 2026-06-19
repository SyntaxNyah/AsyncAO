package gifenc

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"testing"
)

func solidPaletted(c color.Color, w, h int) *image.Paletted {
	p := image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9)
	draw.Draw(p, p.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	return p
}

// TestEncodeGIFRoundTrip is the definition-of-done seam for the GIF export: feed
// synthetic frames, encode, decode the bytes back, and assert the frame count,
// bounds, and per-frame delay survive — so the capture side just has to produce
// frames.
func TestEncodeGIFRoundTrip(t *testing.T) {
	frames := []*image.Paletted{
		solidPaletted(color.RGBA{R: 255, A: 255}, 6, 4),
		solidPaletted(color.RGBA{G: 255, A: 255}, 6, 4),
		solidPaletted(color.RGBA{B: 255, A: 255}, 6, 4),
	}
	data, err := EncodeGIF(frames, 8)
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("encoded GIF won't decode: %v", err)
	}
	if len(g.Image) != 3 {
		t.Fatalf("frame count = %d, want 3", len(g.Image))
	}
	if g.LoopCount != 0 {
		t.Errorf("loop count = %d, want 0 (forever)", g.LoopCount)
	}
	for i, d := range g.Delay {
		if d != 8 {
			t.Errorf("frame %d delay = %d cs, want 8", i, d)
		}
	}
	if b := g.Image[0].Bounds(); b.Dx() != 6 || b.Dy() != 4 {
		t.Errorf("frame bounds = %v, want 6x4", b)
	}
}

func TestEncodeGIFEmpty(t *testing.T) {
	if _, err := EncodeGIF(nil, 8); err == nil {
		t.Error("encoding zero frames should error, not produce an empty GIF")
	}
}

// TestQuantizePreservesSizeAndHue checks the RGBA→paletted conversion keeps the
// frame dimensions and lands on a roughly-correct colour (Plan9 has reds).
func TestQuantizePreservesSizeAndHue(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	draw.Draw(src, src.Bounds(), &image.Uniform{C: color.RGBA{R: 230, G: 10, B: 10, A: 255}}, image.Point{}, draw.Src)
	p := Quantize(src)
	if p.Bounds() != src.Bounds() {
		t.Fatalf("quantized bounds = %v, want %v", p.Bounds(), src.Bounds())
	}
	r, g, b, _ := p.At(4, 4).RGBA()
	if r>>8 < 128 || g>>8 > 128 || b>>8 > 128 {
		t.Errorf("quantized red drifted too far: r=%d g=%d b=%d", r>>8, g>>8, b>>8)
	}
}
