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
