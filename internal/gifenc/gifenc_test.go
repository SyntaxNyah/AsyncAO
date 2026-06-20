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
	data, err := EncodeGIF(frames, 8, true)
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
	// loop=false must encode "play once" (LoopCount -1), not forever.
	once, err := EncodeGIF(frames, 8, false)
	if err != nil {
		t.Fatal(err)
	}
	if g2, err := gif.DecodeAll(bytes.NewReader(once)); err != nil || g2.LoopCount != -1 {
		t.Errorf("loop=false: LoopCount=%d err=%v, want -1 (play once)", g2.LoopCount, err)
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
	if _, err := EncodeGIF(nil, 8, true); err == nil {
		t.Error("encoding zero frames should error, not produce an empty GIF")
	}
}

// quantBlock builds a quantized (transparent-slot) frame: a static gray field
// with a red block at x — the moving-block fixture for the inter-frame diff.
func quantBlock(w, h, x int) *image.Paletted {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: color.RGBA{R: 40, G: 40, B: 48, A: 255}}, image.Point{}, draw.Src)
	draw.Draw(rgba, image.Rect(x, 20, x+8, 28), &image.Uniform{C: color.RGBA{R: 220, G: 30, B: 30, A: 255}}, image.Point{}, draw.Src)
	return Quantize(rgba)
}

// encodeWhole encodes every frame at full bounds (no diff) — the naive baseline
// the inter-frame diff has to beat on size.
func encodeWhole(frames []*image.Paletted, delayCs int) ([]byte, error) {
	g := &gif.GIF{LoopCount: 0, Image: frames, Delay: make([]int, len(frames))}
	for i := range g.Delay {
		g.Delay[i] = delayCs
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sameRGBA compares a composited RGBA canvas to the original paletted frame,
// pixel by pixel (ignoring alpha — the canvas is opaque, the original is too).
func sameRGBA(canvas *image.RGBA, want *image.Paletted) (int, int, bool) {
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			ar, ag, ab, _ := canvas.At(x, y).RGBA()
			br, bg, bb, _ := want.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb {
				return x, y, false
			}
		}
	}
	return 0, 0, true
}

// TestEncodeGIFDiffCompositesAndShrinks is the load-bearing test for the size +
// animation fix: a mostly-static scene with a small moving block must (1) encode
// far smaller than whole frames, and (2) decode+composite (applying GIF disposal
// and per-frame transparency) back to the EXACT originals — so the diff that
// shrinks the file can't silently corrupt the picture.
func TestEncodeGIFDiffCompositesAndShrinks(t *testing.T) {
	const w, h, n = 64, 48, 8
	originals := make([]*image.Paletted, n)
	for i := 0; i < n; i++ {
		originals[i] = quantBlock(w, h, 4+i*4) // block slides right: every frame differs
	}

	data, err := EncodeGIF(originals, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	naive, err := encodeWhole(originals, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= len(naive) {
		t.Errorf("diffed GIF = %d bytes, not smaller than naive whole-frame %d", len(data), len(naive))
	}

	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("encoded GIF won't decode: %v", err)
	}
	if len(g.Image) != n {
		t.Fatalf("emitted %d frames, want %d (every frame differs, none folded)", len(g.Image), n)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, frame := range g.Image {
		// DisposalNone: the canvas carries over; transparent pixels (alpha 0)
		// leave the prior pixel — exactly how a viewer plays it back.
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		if x, y, ok := sameRGBA(canvas, originals[i]); !ok {
			t.Fatalf("composited frame %d differs from original at (%d,%d)", i, x, y)
		}
	}
}

// TestEncodeGIFFoldsIdenticalFrames pins the duration-preserving fold: a frame
// identical to its predecessor emits nothing and adds its delay to the previous
// frame, so [A, A, B] becomes two frames with A shown twice as long — the total
// runtime is unchanged but the file is smaller.
func TestEncodeGIFFoldsIdenticalFrames(t *testing.T) {
	a := quantBlock(32, 24, 4)
	aCopy := quantBlock(32, 24, 4) // byte-identical to a
	b := quantBlock(32, 24, 12)

	data, err := EncodeGIF([]*image.Paletted{a, aCopy, b}, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Image) != 2 {
		t.Fatalf("emitted %d frames, want 2 (the identical second frame folds)", len(g.Image))
	}
	if g.Delay[0] != 16 {
		t.Errorf("folded keyframe delay = %d cs, want 16 (8+8)", g.Delay[0])
	}
	if total := g.Delay[0] + g.Delay[1]; total != 24 {
		t.Errorf("total duration = %d cs, want 24 (3 frames × 8) — folding must preserve runtime", total)
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
