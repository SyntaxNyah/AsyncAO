package render

import (
	"bytes"
	"testing"
	"unsafe"

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

	b, err := RasterizeBadge(ren, font, "A", sdl.Color{R: 255, G: 255, B: 255, A: 255}, DefaultDevScale)
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
	// dst lives outside the measured closure so &dst doesn't heap-escape per call. 100%
	// renderPct against a DefaultDevScale badge takes the pre-#77 plain-Copy branch —
	// deviceExact is false at the identity scale by design (see Badge.Draw/deviceExactAt).
	var dst sdl.Rect
	allocs := testing.AllocsPerRun(200, func() {
		dst = sdl.Rect{X: 10, Y: 20, W: 32, H: 32}
		b.Draw(ren, &dst, 128, DefaultDevScale, nil)
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
	if b, err := RasterizeBadge(ren, nil, "x", sdl.Color{}, DefaultDevScale); b != nil || err != nil {
		t.Errorf("nil font: got (%v, %v), want (nil, nil)", b, err)
	}
	var nilBadge *Badge
	if w, h := nilBadge.Size(); w != 0 || h != 0 {
		t.Errorf("nil badge Size = %dx%d, want 0x0", w, h)
	}
	if w, h := nilBadge.RawSize(); w != 0 || h != 0 {
		t.Errorf("nil badge RawSize = %dx%d, want 0x0", w, h)
	}
	var dst sdl.Rect
	nilBadge.Draw(ren, &dst, 200, 150, nil) // must not panic, including the device-exact branch's own path
	nilBadge.Destroy()                      // must not panic
}

// TestBadgeDeviceExactDiffersFromResampleAtFractionalScale is the fails-at-a-fractional-
// scale / passes-at-100% regression pin for the #2 half of the blur report: it drives the
// SAME production entry point (Badge.Draw) twice on the SAME texture — once with
// renderPct telling it the truth (the device-exact branch fires) and once lying that no
// scale is known (renderPct<=0, so deviceExactAt is false and the plain resampling Copy
// runs, which is exactly what ran before ensureReactBadge/Draw learned about device scale
// at all) — and reads the real backbuffer both times.
//
//   - At 100% (the identity scale) neither call can take the exact branch (deviceExactAt
//     excludes DefaultDevScale by design — "not worth it," same as MessageRaster), so the
//     two paint IDENTICALLY: a bug here would have to be a DIFFERENT bug, not this one.
//   - At a fractional scale the exact branch paints the texture's own device pixels 1:1
//     while the lied-to call resamples a logical-sized copy back up through SetScale —
//     two different pixel operations on the same anti-aliased glyph, which cannot land on
//     the same bytes. Reverting Draw's device-exact branch (or ensureReactBadge's
//     emojiDeviceFont/textDevPct wiring that feeds it a real devScale) collapses both
//     calls onto the same code path again and this test goes back to passing where it
//     must fail.
func TestBadgeDeviceExactDiffersFromResampleAtFractionalScale(t *testing.T) {
	const canvasW, canvasH = 100, 100
	paint := func(t *testing.T, pct, badgeDevScale, calledRenderPct int32) []byte {
		ren, _, cleanup := newSoftwareBackbuffer(t, canvasW, canvasH)
		defer cleanup()
		font, fcleanup := newAnimTestFont(t, 24)
		defer fcleanup()
		b, err := RasterizeBadge(ren, font, "M", sdl.Color{R: 255, G: 255, B: 255, A: 255}, badgeDevScale)
		if err != nil || b == nil {
			t.Fatalf("RasterizeBadge: %v", err)
		}
		defer b.Destroy()
		logW, logH := b.Size()

		_ = ren.SetDrawColor(0, 0, 0, 255)
		_ = ren.Clear()
		_ = ren.SetScale(float32(pct)/100, float32(pct)/100) // the frame's ambient scale, set once, as main.go does
		dst := sdl.Rect{X: 0, Y: 0, W: logW, H: logH}
		b.Draw(ren, &dst, 255, calledRenderPct, nil)
		_ = ren.SetScale(1, 1)

		pix := make([]byte, canvasW*canvasH*4)
		if err := ren.ReadPixels(&sdl.Rect{X: 0, Y: 0, W: canvasW, H: canvasH}, uint32(sdl.PIXELFORMAT_ARGB8888), unsafe.Pointer(&pix[0]), canvasW*4); err != nil {
			t.Skipf("ReadPixels: %v", err)
		}
		return pix
	}

	for _, pct := range []int32{100, 175} {
		t.Run(map[int32]string{100: "100pct", 175: "175pct"}[pct], func(t *testing.T) {
			exact := paint(t, pct, pct, pct) // renderPct == devScale == the ambient scale: the real, correct call
			lied := paint(t, pct, pct, 0)    // same badge, same ambient scale, renderPct withheld
			differ := !bytes.Equal(exact, lied)
			if pct == render100Identity && differ {
				t.Error("at 100% the device-exact and resampled branches painted DIFFERENT pixels — deviceExactAt should exclude the identity scale for both calls, so nothing should differ here")
			}
			if pct != render100Identity && !differ {
				t.Error("at a fractional scale the device-exact and resampled branches painted IDENTICAL pixels — Badge.Draw's device-exact branch (or the devScale it was told) isn't actually doing anything different from the plain resample; the #2 blur fix isn't firing")
			}
		})
	}
}

// render100Identity names the scale at which deviceExactAt never fires (the whole point of
// the identity-scale exclusion: nothing needs folding at 1:1). A bare literal 100 here
// would read as an arbitrary test fixture rather than the same constant Badge.Draw's own
// gate compares against.
const render100Identity = DefaultDevScale

// newSoftwareBackbuffer is TestDrawScaledCoversSameExtentAsDraw's harness, factored out
// so a second pixel-reading test doesn't hand-roll its own SDL bring-up. RENDERER_SOFTWARE
// so ReadPixels works (the dummy driver newHeadlessRenderer uses does not back a real
// framebuffer).
func newSoftwareBackbuffer(t *testing.T, w, h int32) (ren *sdl.Renderer, win *sdl.Window, cleanup func()) {
	t.Helper()
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL video unavailable: %v", err)
	}
	win, err := sdl.CreateWindow("x", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED, w, h, sdl.WINDOW_HIDDEN)
	if err != nil {
		sdl.Quit()
		t.Skipf("window: %v", err)
	}
	ren, err = sdl.CreateRenderer(win, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		win.Destroy()
		sdl.Quit()
		t.Skipf("renderer: %v", err)
	}
	return ren, win, func() {
		ren.Destroy()
		win.Destroy()
		sdl.Quit()
	}
}
