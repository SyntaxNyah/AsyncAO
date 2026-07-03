package ui

import "testing"

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
