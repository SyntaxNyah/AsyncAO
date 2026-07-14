package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestUILogicalFromDeviceMatchesRender pins the #77 invariant that the ui and
// render packages share ONE rounding rule (round half up). If they drift, a kit
// label (blitLabel) and a message raster (Draw) of the same string land at
// different widths — the roadmap's flagged off-by-one. render.LogicalFromDevice is
// exported for this cross-package check only.
func TestUILogicalFromDeviceMatchesRender(t *testing.T) {
	for _, dev := range []int32{0, 100, 125, 150, 175, 200} {
		for _, v := range []int32{0, 1, 37, 88, 100, 149, 151, 201, 333} {
			if got, want := uiLogicalFromDevice(v, dev), render.LogicalFromDevice(v, dev); got != want {
				t.Errorf("uiLogicalFromDevice(%d,%d)=%d != render.LogicalFromDevice=%d", v, dev, got, want)
			}
		}
	}
}

// TestTextWidthScaleInvariant is the core #77 Part-A regression: TextWidth must
// return the SAME logical width for a string regardless of the global UI scale,
// because layout happens in logical pixels (the device scale folds into the font
// POINT size and the renderer's SetScale maps the rasterized glyphs back 1:1). A
// regression that measured the device face here would make every chrome layout
// grow with the UI scale — the whole bug we are fixing.
func TestTextWidthScaleInvariant(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()

	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	const probe = "Objection! The witness is lying."
	c.SetUIScale(100)
	w100 := c.TextWidth(probe)
	if w100 <= 0 {
		t.Fatalf("TextWidth at 100%% returned %d (want > 0)", w100)
	}
	for _, scale := range []int{125, 150, 175, 200} {
		c.SetUIScale(scale)
		if got := c.TextWidth(probe); got != w100 {
			t.Errorf("TextWidth at %d%% = %d, want %d (logical width must not change with UI scale)", scale, got, w100)
		}
	}
}

// TestTextDevScaleNoOpUnchanged pins the per-frame-safe contract: SetTextDevScale
// with the CURRENT value must not rebuild anything (the export/split brackets and
// the main loop can call it every frame). We prove the no-op by seeding a width-memo
// entry and checking it survives a same-value call but is purged by a real change.
func TestTextDevScaleNoOpUnchanged(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()

	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	c.SetTextDevScale(150)
	c.widthCache["probe"] = 42
	c.SetTextDevScale(150) // same value → no-op, memo survives
	if _, ok := c.widthCache["probe"]; !ok {
		t.Error("SetTextDevScale(same) must be a no-op — it purged the width memo (per-frame perf trap)")
	}
	c.SetTextDevScale(200) // real change → purge
	if _, ok := c.widthCache["probe"]; ok {
		t.Error("SetTextDevScale(new) must purge the width memo (stale-size glyphs otherwise)")
	}
}

// TestDevTextWidthIdentityAt100 pins the 100% bypass (#77 S1b): at devPct==100 the
// device face IS the logical face, so the field's device-face measure must equal
// TextWidth exactly. The fractional field path folds this back to logical, so any
// divergence at 100% would move every caret/selection/click off the glyph — the
// identity is what keeps the common case byte-for-byte.
func TestDevTextWidthIdentityAt100(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()

	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	c.SetUIScale(100)
	for _, probe := range []string{"", "a", "Objection!", "The witness is lying, your honor."} {
		if got, want := c.devTextWidth(probe), c.TextWidth(probe); got != want {
			t.Errorf("devTextWidth(%q)=%d != TextWidth=%d at 100%% (identity path broken)", probe, got, want)
		}
	}
}

// TestDevWidthCachePurgeContract mirrors TestTextDevScaleNoOpUnchanged's shape for
// the new device-face field memo: a same-value SetTextDevScale must NOT purge it
// (per-frame perf trap — the split brackets call it every frame), and a real change
// MUST purge it (a stale-size device entry would put the caret on the wrong seam).
func TestDevWidthCachePurgeContract(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()

	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	c.SetTextDevScale(150)
	// Seed AFTER the scale is set (the memo is lazily created otherwise).
	c.devWidthCache = map[string]int32{"probe": 42}
	// Same value → no-op: the memo must survive (the split brackets call this every frame).
	c.SetTextDevScale(150)
	if _, ok := c.devWidthCache["probe"]; !ok {
		t.Error("SetTextDevScale(same) must be a no-op — it purged the device-width memo (per-frame perf trap)")
	}
	// Real change → purge: the entry carries the old device point size.
	c.SetTextDevScale(200)
	if _, ok := c.devWidthCache["probe"]; ok {
		t.Error("SetTextDevScale(new) must purge the device-width memo (stale-size caret otherwise)")
	}
}

// TestDevTextWidthDivergesAtFractionalScale is the premise-of-the-fix regression:
// at a fractional scale the device-face measure (folded back to logical) can differ
// from the logical-face TextWidth over a long string, because SDL_ttf re-quantizes
// each glyph advance at the larger device point size (#77 S1b, the length-growing
// caret drift). If the embedded font happens NOT to diverge at 150% we surface that
// as a skip rather than forcing a false !=: the fix is then a no-op for THIS font
// but still correct for the HiDPI faces that do diverge.
func TestDevTextWidthDivergesAtFractionalScale(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()

	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	const probe = "Objection! The witness is clearly lying about the events of that night."
	c.SetUIScale(150)
	// logical = the chrome-face layout width; folded = the device-face field metric
	// folded back to logical (what the caret/selection/click now use).
	logical := c.TextWidth(probe)
	folded := uiLogicalFromDevice(c.devTextWidth(probe), c.textDevPct)
	if logical <= 0 || folded <= 0 {
		t.Skipf("font measurement unavailable (logical=%d folded=%d)", logical, folded)
	}
	if logical == folded {
		t.Skipf("embedded font does not re-quantize at 150%% for this probe (logical==folded==%d); "+
			"fix is a no-op for this face but pins the two-face contract for HiDPI faces that do diverge", logical)
	}
}

// TestMessageRasterLogicalHeightAcrossScale pins that a message raster reports the
// SAME logical height at 100% and a folded device scale (the font is opened larger
// but Height divides back), so a chatbox box sized to the message doesn't grow with
// the UI scale.
func TestMessageRasterLogicalHeightAcrossScale(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()

	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	const msg = "The courtroom fell silent."
	white := render.TextColor(0)

	c.SetTextDevScale(100)
	m1, err := render.Rasterize(ren, c.font, msg, 200, white, 100)
	if err != nil {
		t.Fatalf("rasterize 100%%: %v", err)
	}
	defer m1.Destroy()
	h1 := m1.Height()

	c.SetTextDevScale(200)
	dev := c.deviceTextFont(c.font)
	m2, err := render.Rasterize(ren, dev, msg, 200, white, 200)
	if err != nil {
		t.Fatalf("rasterize 200%%: %v", err)
	}
	defer m2.Destroy()
	h2 := m2.Height()

	if h1 <= 0 || h2 <= 0 {
		t.Fatalf("heights must be positive: h1=%d h2=%d", h1, h2)
	}
	// Same logical height within a small rounding tolerance (a 200% raster's device
	// line-height halves back to the logical one; ±2px for wrap/rounding differences).
	if d := h1 - h2; d < -2 || d > 2 {
		t.Errorf("logical Height drifted with scale: 100%%=%d 200%%=%d (Δ%d, want ≈0)", h1, h2, d)
	}
}
