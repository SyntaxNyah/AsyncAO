package render

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestBadgeBlendModeAndDraw pins the floating-reaction badge foundation (#2): a badge texture
// must be BLENDMODE_BLEND (or the alpha fade silently no-ops and the emoji pops in/out at full
// opacity), and drawing it at a partial alpha into a reused dst rect must allocate nothing per
// frame and leave the cached texture restored to full opacity for the next caller.
func TestBadgeBlendModeAndDraw(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	font, fcleanup := newAnimTestFont(t, 24)
	defer fcleanup()

	b, err := RasterizeBadge(ren, font, "A", sdl.Color{R: 255, G: 255, B: 255, A: 255})
	if err != nil {
		t.Fatalf("RasterizeBadge: %v", err)
	}
	if b == nil || b.tex == nil {
		t.Fatal("RasterizeBadge returned nil badge")
	}
	defer b.Destroy()

	if w, h := b.Size(); w <= 0 || h <= 0 {
		t.Fatalf("badge size = %dx%d, want positive", w, h)
	}

	// The crux: blend mode must be BLEND so AlphaMod fades the badge.
	if bm, err := b.tex.GetBlendMode(); err != nil || bm != sdl.BLENDMODE_BLEND {
		t.Fatalf("badge blend mode = %v (err %v), want BLENDMODE_BLEND — the alpha fade would no-op", bm, err)
	}

	// Drawing at a partial alpha must be zero-alloc (the overlay draws every frame). The
	// dst lives outside the measured closure so &dst doesn't heap-escape per call.
	var dst sdl.Rect
	allocs := testing.AllocsPerRun(200, func() {
		dst = sdl.Rect{X: 10, Y: 20, W: 32, H: 32}
		b.Draw(ren, &dst, 128)
	})
	if allocs != 0 {
		t.Errorf("Badge.Draw allocated %.1f objects/op, want 0", allocs)
	}

	// And the alpha was restored to full after the draw (so a cached badge isn't stuck dim).
	if am, err := b.tex.GetAlphaMod(); err != nil || am != 0xFF {
		t.Errorf("badge alpha mod = %d (err %v) after Draw, want 255 (restored)", am, err)
	}
}

// TestBadgeEmptyAndNilSafe: empty text / nil font yield (nil,nil), and the nil badge's
// methods are safe no-ops (the overlay holds a nil entry until a face loads).
func TestBadgeEmptyAndNilSafe(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	if b, err := RasterizeBadge(ren, nil, "x", sdl.Color{}); b != nil || err != nil {
		t.Errorf("nil font: got (%v, %v), want (nil, nil)", b, err)
	}
	var nilBadge *Badge
	if w, h := nilBadge.Size(); w != 0 || h != 0 {
		t.Errorf("nil badge Size = %dx%d, want 0x0", w, h)
	}
	var dst sdl.Rect
	nilBadge.Draw(ren, &dst, 200) // must not panic
	nilBadge.Destroy()            // must not panic
}
