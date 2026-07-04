package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

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
