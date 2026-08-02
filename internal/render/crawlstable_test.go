package render

import (
	"image"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestDeviceExactGate pins when the device-exact blit engages. It must NOT fire at
// 100% (where the scaled projection is already the identity, so the pre-fix path
// stays byte-for-byte), and it must NOT fire when the renderer's scale and the
// raster's font device scale disagree — that mismatch is how the offscreen passes
// (pinned-tab client, GIF/comic export) declare "I set my own scale", and blitting
// device-sized glyphs 1:1 into their logical-sized target would draw the message
// scale× too big.
func TestDeviceExactGate(t *testing.T) {
	cases := []struct {
		devScale, renderPct int32
		want                bool
	}{
		{100, 100, false}, // 100%: identity projection, nothing to fix
		{200, 200, true},  // 4K at double scale
		{125, 125, true},  // fractional scales resample too — they get it as well
		{150, 150, true},
		{175, 175, true},
		{75, 75, true},    // BELOW 100% the raster is scaled UP, same defect
		{95, 95, true},    // UIScaleStepPercent is 5, so every step in 75..200 is reachable
		{200, 100, false}, // pinned-tab pass: renderer bracketed to 1:1
		{100, 200, false}, // export bracket: textDevPct pinned to 100, renderer not
		{200, 150, false}, // any disagreement at all
		{0, 200, false},   // headless MessageRaster{} literal
		{200, 0, false},   // caller doesn't know the scale (plain Draw)
	}
	for _, c := range cases {
		m := &MessageRaster{devScale: c.devScale}
		if got := m.deviceExact(c.renderPct); got != c.want {
			t.Errorf("deviceExact(devScale=%d, renderPct=%d) = %v, want %v", c.devScale, c.renderPct, got, c.want)
		}
	}
}

// TestProjIsIdentityWhenExact pins that the device-exact projection leaves DEVICE
// measurements alone (so dst.W == src.W and SDL cannot resample) while the scaled
// projection still divides them down exactly as before.
func TestProjIsIdentityWhenExact(t *testing.T) {
	m := &MessageRaster{devScale: 200}
	for _, v := range []int32{0, 1, 7, 101, 4096} {
		if got := m.proj(v, true); got != v {
			t.Errorf("proj(%d, exact) = %d, want %d (a device-exact dst must equal its src)", v, got, v)
		}
		if got, want := m.proj(v, false), logicalFromDevice(v, 200); got != want {
			t.Errorf("proj(%d, scaled) = %d, want %d (the pre-fix projection must be untouched)", v, got, want)
		}
	}
}

// TestLinePitchMatchesLineH pins the invariant that made the device-exact path
// dangerous: Height() documents that it MUST equal Draw's per-line step, LineH()
// is the same number, and the selection highlight steps rows by LineH(). So the
// device-exact vertical step has to be the DEVICE PROJECTION of that logical
// pitch, never the raw device lineH — the two differ by up to a pixel per line and
// the error accumulates down a message, which a one-off origin rounding does not.
//
// It sweeps every reachable UI scale (75..200 in steps of 5) against odd and even
// line heights, since the disagreement only shows on some parities.
func TestLinePitchMatchesLineH(t *testing.T) {
	for _, lineH := range []int32{11, 16, 17, 20, 27, 28, 33} {
		for pct := int32(75); pct <= 200; pct += 5 {
			m := &MessageRaster{lineH: lineH, devScale: pct}
			// The scaled path is the logical pitch, unchanged from before the fix.
			if got, want := m.linePitch(false), m.LineH(); got != want {
				t.Fatalf("scaled linePitch(lineH=%d, %d%%) = %d, want LineH()=%d", lineH, pct, got, want)
			}
			// The exact path must land on the device pixels that same logical pitch
			// occupies — i.e. agree with what every other consumer believes.
			if got, want := m.linePitch(true), m.deviceFromLogical(m.LineH()); got != want {
				t.Fatalf("exact linePitch(lineH=%d, %d%%) = %d, want %d (device projection of LineH)", lineH, pct, got, want)
			}
			// And a message must be exactly as tall as its lines stack.
			m.lines = make([]rasterLine, 4)
			if got, want := m.Height(), 4*m.linePitch(false); got != want {
				t.Fatalf("Height(lineH=%d, %d%%) = %d but 4 lines step to %d", lineH, pct, got, want)
			}
		}
	}
}

// TestCrawlPrefixIsPixelStable is the regression test for the reported artifact:
// at a scaled UI, letters ALREADY on screen shifted by a pixel or two every few
// letters while the message typed out.
//
// It renders the same message at every reveal count and asserts the pixels of the
// common prefix never move. The pre-fix path fails this: dst.W was rounded to a
// whole logical pixel independently of the device-px src, so whenever the revealed
// width was odd at 200% SDL stretched the entire revealed run by one device pixel
// and every glyph in it landed somewhere new.
func TestCrawlPrefixIsPixelStable(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	const (
		uiPct   int32 = 200 // a 4K desktop at double scale — where the artifact shows
		ptSize        = 14
		capW    int32 = 512
		capH    int32 = 64
		originX       = 4 // logical draw origin, constant for the whole crawl
		originY       = 4
	)
	// The raster's fonts are opened at pt×(devScale/100) — the #77 crisp-scaling
	// model — so the device face here is what the live client would build at 200%.
	font, closeFont := newAnimTestFont(t, ptSize*int(uiPct)/int(DefaultDevScale))
	defer closeFont()

	// Mixed letter widths and a space, so consecutive reveals land on both odd and
	// even device widths (a fixed-advance string could miss the parity flip).
	const msg = "Objection! That is hearsay."
	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	m, err := Rasterize(ren, font, msg, 4000 /* wide: keep it one line */, white, uiPct)
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	defer m.Destroy()
	if m.Lines() != 1 {
		t.Fatalf("fixture wrapped to %d lines, want 1 (widen wrapW)", m.Lines())
	}

	cap, err := NewCaptureTarget(ren, capW, capH)
	if err != nil {
		t.Skipf("capture target unavailable: %v", err)
	}
	defer cap.Close()

	scale := float32(uiPct) / float32(DefaultDevScale)
	shoot := func(visible int) *image.RGBA {
		t.Helper()
		img, err := cap.Capture(ren, func(sdl.Rect) {
			_ = ren.SetScale(scale, scale)
			m.DrawScaled(ren, visible, originX, originY, uiPct)
			_ = ren.SetScale(1, 1)
		})
		if err != nil {
			t.Fatalf("capture at %d runes: %v", visible, err)
		}
		return img
	}

	total := m.TotalRunes()
	if total < 5 {
		t.Fatalf("fixture rasterized %d runes, want the whole message", total)
	}
	// Anti-vacuum guard: a harness that renders NOTHING would pass every comparison
	// below. Demand real ink first. (Measured on the fix: ~2300 lit pixels, and the
	// pre-fix scaled path disturbed already-drawn text on 14 of the 26 reveal steps.)
	if ink := litPixels(shoot(total)); ink < 200 {
		t.Fatalf("capture holds only %d lit pixels — the harness drew no text, so the stability check below is vacuous", ink)
	}
	prev := shoot(1)
	moved := 0
	for n := 2; n <= total; n++ {
		cur := shoot(n)
		// Everything left of the PREVIOUS reveal edge was already on screen and must
		// be untouched by the new glyph. Compare in device px: the edge is a device
		// advance and the capture target is unscaled.
		edge := int(m.prefixWidthDev(n-1)) + originX*int(uiPct/DefaultDevScale)
		if edge > int(capW) {
			break // the message ran past the capture target; earlier reveals proved the point
		}
		for y := 0; y < int(capH); y++ {
			row := y * cur.Stride
			for x := 0; x < edge*4; x++ {
				if cur.Pix[row+x] != prev.Pix[row+x] {
					moved++
					if moved <= 3 {
						t.Errorf("revealing rune %d moved an already-drawn pixel at (%d,%d): %d → %d",
							n, x/4, y, prev.Pix[row+x], cur.Pix[row+x])
					}
					y = int(capH) // stop scanning this pair
					break
				}
			}
		}
		prev = cur
	}
	if moved > 0 {
		t.Errorf("%d reveal steps disturbed already-drawn text — the crawl is not pixel-stable at %d%%", moved, uiPct)
	}
}

// TestDrawScaledAllocatesNothing pins the zero-allocation draw loop across the new
// device-exact path. Two traps it guards: SetScale must be called with plain
// float32 args (go-sdl2's GetScale takes the address of its named returns, so a
// read-back to restore the scale would heap-allocate every frame), and the src/dst
// rects must keep going through the persistent scratch fields — a local rect whose
// address reaches cgo escapes and allocates on every blit.
func TestDrawScaledAllocatesNothing(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	const uiPct int32 = 200
	font, closeFont := newAnimTestFont(t, 14*int(uiPct)/int(DefaultDevScale))
	defer closeFont()

	m, err := Rasterize(ren, font, "Objection! That is hearsay.", 4000,
		sdl.Color{R: 255, G: 255, B: 255, A: 255}, uiPct)
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	defer m.Destroy()

	for _, tc := range []struct {
		name      string
		renderPct int32
	}{
		{"device-exact", uiPct},     // the new path (SetScale bracket + 1:1 blits)
		{"scaled", DefaultDevScale}, // the untouched pre-fix path
	} {
		half := m.TotalRunes() / 2
		if allocs := testing.AllocsPerRun(200, func() {
			m.DrawScaled(ren, half, 4, 4, tc.renderPct)
		}); allocs != 0 {
			t.Errorf("%s DrawScaled allocates %v per call, want 0", tc.name, allocs)
		}
	}
}

// litPixels counts pixels with a meaningful red channel — the capture clears to
// black, so anything lit is drawn text.
func litPixels(img *image.RGBA) int {
	n := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 40 {
			n++
		}
	}
	return n
}
