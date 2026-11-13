package ui

import (
	"testing"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// TestIconLabelCentresTheInk pins IconLabel's contract: the visible pixels of the
// symbol end up centred in the rect, however low or high the glyph sits in its
// font's line box. A full stop is the stand-in: it is in the embedded face (no
// system font needed) and its ink sits on the baseline, far below the line's
// middle, which is where a fallback ★ ended up when drawn like text.
func TestIconLabelCentresTheInk(t *testing.T) {
	const (
		canvas    = 64
		rectEdge  = 40
		rectInset = 12
		bytesPP   = 4
		tolerance = 1 // integer centring of an odd/even ink height
	)
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	_ = ren.SetDrawColor(0, 0, 0, 0)
	_ = ren.Clear()
	r := sdl.Rect{X: rectInset, Y: rectInset, W: rectEdge, H: rectEdge}
	c.IconLabel(r, ".", sdl.Color{R: 255, G: 255, B: 255, A: 255})

	pix := make([]byte, canvas*canvas*bytesPP)
	if err := ren.ReadPixels(&sdl.Rect{W: canvas, H: canvas}, uint32(sdl.PIXELFORMAT_ARGB8888), unsafe.Pointer(&pix[0]), canvas*bytesPP); err != nil {
		t.Fatalf("ReadPixels: %v", err)
	}
	top, bottom := -1, -1
	for y := 0; y < canvas; y++ {
		for x := 0; x < canvas; x++ {
			p := (y*canvas + x) * bytesPP
			if pix[p] != 0 || pix[p+1] != 0 || pix[p+2] != 0 { // any lit channel is ink on the black canvas
				if top < 0 {
					top = y
				}
				bottom = y
				break
			}
		}
	}
	if top < 0 {
		t.Fatal("IconLabel drew nothing")
	}
	inkMid2 := top + bottom               // twice the ink's middle row
	rectMid2 := 2*int(r.Y) + int(r.H) - 1 // twice the rect's middle row
	if d := inkMid2 - rectMid2; d > 2*tolerance || d < -2*tolerance {
		t.Errorf("ink rows %d..%d are not centred in rect rows %d..%d", top, bottom, r.Y, r.Y+r.H-1)
	}
}
