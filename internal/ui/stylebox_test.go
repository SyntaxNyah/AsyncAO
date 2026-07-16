package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestStyleBoxScrollableWhenOverflowing pins the #C fix: with many saved presets and
// every content-growing effect toggled on, the DRAWN box height stays clamped inside the
// window (it never grows off the bottom of the screen), and the unclamped content height
// exceeds it — so a scroll range exists and the tail controls (including saved styles) are
// reachable by scrolling rather than stranded off-panel. styleBoxRect is pure arithmetic
// over prefs, so it runs headlessly (no renderer) — the full draw can't, per the harness.
func TestStyleBoxScrollableWhenOverflowing(t *testing.T) {
	a := presetTestApp(t)
	const w, h = int32(800), int32(300) // a short window, so a fat box must clamp + scroll

	// Turn on every effect that adds a content section, and stack up saved presets.
	p := config.SpriteStylePref{Tint: true, Grayscale: true, PaintSplit: 40, Outline: true, Glitch: true}
	a.d.Prefs.SetSpriteStyle(p)
	for i := 0; i < 20; i++ {
		a.d.Prefs.AddStylePreset(config.StylePreset{Name: string(rune('A' + i))})
	}

	r, fullH := a.styleBoxRect(w, h)
	if r.H > h-styleBoxWinMargin {
		t.Errorf("box drew off the window: r.H=%d, want <= %d", r.H, h-styleBoxWinMargin)
	}
	if fullH <= r.H {
		t.Errorf("overflowing box has no scroll range: fullH=%d, r.H=%d (content would be clamped away)", fullH, r.H)
	}
}

// TestStyleBoxHeightTracksContent pins that both a saved preset and a toggled effect feed
// the SAME content-height sum styleBoxRect returns — the identity the scroll range depends
// on. A preset or effect that drew but wasn't summed would strand content off the bottom.
func TestStyleBoxHeightTracksContent(t *testing.T) {
	a := presetTestApp(t)
	const w, h = int32(1200), int32(2000) // tall enough that nothing clamps, so fullH == r.H

	base := heightOf(a, w, h)

	a.d.Prefs.AddStylePreset(config.StylePreset{Name: "mood"})
	if withPreset := heightOf(a, w, h); withPreset <= base {
		t.Errorf("adding a saved preset didn't grow the box: %d -> %d", base, withPreset)
	}

	a2 := presetTestApp(t)
	before := heightOf(a2, w, h)
	a2.d.Prefs.SetSpriteStyle(config.SpriteStylePref{Outline: true}) // Outline adds its colour rows
	if after := heightOf(a2, w, h); after <= before {
		t.Errorf("toggling Outline didn't grow the box: %d -> %d", before, after)
	}
}

// heightOf returns the unclamped content height styleBoxRect computes for the current prefs.
func heightOf(a *App, w, h int32) int32 {
	_, fullH := a.styleBoxRect(w, h)
	return fullH
}

// TestHuePaintSliderRoundTrip pins the Hue slider's unit contract: the wheel
// helpers work in h ∈ [0,1] while the slider shows degrees, so a chosen degree
// must survive RGB storage and come back to (about) the same slider position —
// the first draft passed raw degrees into hsvToRGB and every hue collapsed to
// its fractional part.
func TestHuePaintSliderRoundTrip(t *testing.T) {
	for _, deg := range []int32{0, 30, 60, 120, 180, 240, 300, 359} {
		r, g, b := hsvToRGB(float64(deg)/360, 1, 1)
		if r == g && g == b {
			t.Fatalf("hue %d° produced a hueless grey (%d,%d,%d) — degrees fed as [0,1]?", deg, r, g, b)
		}
		back, s, v := rgbToHSV(r, g, b)
		pos := int32(back*360 + 0.5)
		if d := pos - deg; d < -1 || d > 1 {
			t.Errorf("hue %d° round-tripped to %d° (rgb %d,%d,%d)", deg, pos, r, g, b)
		}
		if s < 0.99 || v < 0.99 {
			t.Errorf("hue %d°: paint colours must be full-vividness, got s=%.2f v=%.2f", deg, s, v)
		}
	}
}

// TestHuePaintIsExistingWire pins the compatibility claim behind the Hue-paint
// mode: it is nothing but Tint+Grayscale, two v1 wire fields, so the marker an
// old client decodes carries the full effect (no new bytes, no degradation).
func TestHuePaintIsExistingWire(t *testing.T) {
	a := testTabApp(t)
	p := a.d.Prefs.SpriteStyle()
	p.Tint, p.Grayscale = true, true
	p.R, p.G, p.B = hsvToRGB(200.0/360, 1, 1)
	a.d.Prefs.SetSpriteStyle(p)

	s := a.mySpriteStyle()
	if !s.Tint || !s.Grayscale {
		t.Fatal("hue paint must materialize as Tint+Grayscale on the courtroom style")
	}
	if s.Restyle != 0 || s.Outline || s.Sepia || s.Posterize {
		t.Error("hue paint must not touch any v2/extension field")
	}
}

// TestTwoTonePrefGating pins styleFromPref's two-tone normalization: the split +
// second colour reach the courtroom style ONLY while the hue-paint composition is on
// and a split is set — a stale pref (paint turned off, or split cleared with a colour
// left behind) must not fatten the wire frame or fire a change marker that renders
// identically.
func TestTwoTonePrefGating(t *testing.T) {
	on := config.SpriteStylePref{Tint: true, Grayscale: true, R: 255, PaintSplit: 40, Paint2B: 200}
	if s := styleFromPref(on); s.PaintSplit != 40 || s.Paint2B != 200 {
		t.Errorf("two-tone lost on the active path: %+v", s)
	}
	for name, p := range map[string]config.SpriteStylePref{
		"paint off":     {Tint: true, R: 255, PaintSplit: 40, Paint2B: 200},          // no Grayscale → no hue paint
		"no split":      {Tint: true, Grayscale: true, R: 255, Paint2B: 200},         // colour B without a split
		"gray only":     {Grayscale: true, PaintSplit: 40, Paint2B: 200},             // no tint → no hue paint
		"restyle owner": {Tint: true, Grayscale: false, PaintSplit: 40, Paint2R: 10}, // paint exited by a restyle pick
	} {
		if s := styleFromPref(p); s.PaintSplit != 0 || s.Paint2R != 0 || s.Paint2B != 0 {
			t.Errorf("%s: two-tone leaked onto the wire style: %+v", name, s)
		}
	}
}

// TestGlitchPrefGating pins styleFromPref's glitch normalization: the mode + fringe
// colour pair reach the courtroom style only while Glitch itself is on, and an
// out-of-range stored mode falls back to Classic (matching the wire decoder).
func TestGlitchPrefGating(t *testing.T) {
	on := config.SpriteStylePref{Glitch: true, GlitchMode: courtroom.GlitchTorn, GlitchAR: 9, GlitchBB: 8}
	if s := styleFromPref(on); !s.Glitch || s.GlitchMode != courtroom.GlitchTorn || s.GlitchAR != 9 || s.GlitchBB != 8 {
		t.Errorf("glitch options lost on the active path: %+v", s)
	}
	off := config.SpriteStylePref{GlitchMode: courtroom.GlitchTorn, GlitchAR: 9, GlitchBB: 8}
	if s := styleFromPref(off); s.GlitchMode != 0 || s.GlitchAR != 0 || s.GlitchBB != 0 {
		t.Errorf("glitch options leaked with Glitch off: %+v", s)
	}
	bad := config.SpriteStylePref{Glitch: true, GlitchMode: 200, GlitchAR: 9}
	if s := styleFromPref(bad); s.GlitchMode != 0 || s.GlitchAR != 9 {
		t.Errorf("out-of-range mode: got mode=%d AR=%d, want 0/9", s.GlitchMode, s.GlitchAR)
	}
}
