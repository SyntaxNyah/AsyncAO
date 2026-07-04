package courtroom

import "testing"

// TestStyleTailRoundTrip pins the presence-flagged tail (two-tone paint + glitch
// options): each group round-trips alone, together, and alongside every earlier
// extension (restyle, custom path, coloured outline) — so the offsets stay right
// however much of the frame is populated.
func TestStyleTailRoundTrip(t *testing.T) {
	for name, s := range map[string]SpriteStyle{
		"paint2 alone": {Tint: true, Grayscale: true, R: 255, PaintSplit: 30, Paint2R: 40, Paint2G: 80, Paint2B: 255},
		"glitch mode":  {Glitch: true, GlitchMode: GlitchTorn},
		"glitch colours": {Glitch: true,
			GlitchAR: 60, GlitchAG: 220, GlitchAB: 90, GlitchBR: 235, GlitchBG: 50, GlitchBB: 190},
		"glitch mode+colours": {Glitch: true, GlitchMode: GlitchEcho, GlitchAR: 1, GlitchBB: 2},
		"both groups": {Tint: true, Grayscale: true, R: 200, PaintSplit: 55, Paint2B: 128,
			Glitch: true, GlitchMode: GlitchStatic, GlitchAR: 9},
		"kitchen sink": {Tint: true, Grayscale: true, R: 10, G: 20, B: 30,
			PaintSplit: maxPaintSplit, Paint2R: 1, Paint2G: 2, Paint2B: 3,
			Glitch: true, GlitchMode: GlitchHeavy, GlitchBR: 77,
			Outline: true, OutlineR: 255, OutlineG: 80, OutlineB: 40,
			Path: [maxPathPoints]uint8{0x11, 0x22, 0x33}, PathLen: 3},
	} {
		if got, _ := DecodeSpriteStyle("x" + s.EncodeMarker()); got != s {
			t.Errorf("%s round-trip: got %+v, want %+v", name, got, s)
		}
	}
}

// TestStyleTailOutlineDefaultColour pins the offset rule that keeps v1.53.x clients
// correct: with Outline ON and a tail following, the outline-colour region is ALWAYS
// written — even at the default 0,0,0 — because those clients read the three bytes
// after the restyle as the outline colour unconditionally. 0,0,0 decodes to the
// default white on every build, and the tail still round-trips here.
func TestStyleTailOutlineDefaultColour(t *testing.T) {
	s := SpriteStyle{Outline: true, Glitch: true, GlitchMode: GlitchHeavy}
	b := s.payloadBytes()
	// Frame: 9 base + flags2 + pathLen(0) + restyle(0) + outline colour (3, forced) +
	// tailFlags + glitch group (7).
	if want := spriteStyleBytesV2 + 1 + 1 + 3 + 1 + 7; len(b) != want {
		t.Fatalf("payload = %d bytes, want %d (outline colour region must be present before the tail)", len(b), want)
	}
	// The three bytes where a v1.53.x decoder looks for the outline colour must be the
	// real (default) colour, not the tail's first bytes.
	colOff := spriteStyleBytesV2 + 2 // flags2, pathLen, restyle, then colour
	if b[colOff] != 0 || b[colOff+1] != 0 || b[colOff+2] != 0 {
		t.Errorf("outline colour region = %v, want 0,0,0 (default white)", b[colOff:colOff+3])
	}
	if got := styleFromBytes(b); got != s {
		t.Errorf("round-trip: got %+v, want %+v", got, s)
	}

	// Without a tail, the default colour still writes NOTHING extra (the v1.53.x frame).
	plain := SpriteStyle{Outline: true}
	if got := len(plain.payloadBytes()); got != spriteStyleBytesV2 {
		t.Errorf("default-outline payload = %d bytes, want %d", got, spriteStyleBytesV2)
	}
}

// TestStyleTailGating pins that the tail fields ride ONLY their parent effect: a glitch
// mode/colour without Glitch, or a split outside 1..maxPaintSplit, encodes the plain
// frame (no tail bytes to blip other clients' typewriters for nothing).
func TestStyleTailGating(t *testing.T) {
	noGlitch := SpriteStyle{Glow: true, GlitchMode: GlitchTorn, GlitchAR: 9}
	if got := len(noGlitch.payloadBytes()); got != spriteStyleBytes {
		t.Errorf("glitch fields without Glitch: payload = %d bytes, want %d", got, spriteStyleBytes)
	}
	noSplit := SpriteStyle{Tint: true, Grayscale: true, Paint2R: 200} // Paint2 without a split does nothing
	if got := len(noSplit.payloadBytes()); got != spriteStyleBytes {
		t.Errorf("paint2 without split: payload = %d bytes, want %d", got, spriteStyleBytes)
	}
}

// TestStyleTailDegradation pins the benign failure modes: an out-of-range split or an
// unknown (newer) glitch mode is dropped while the rest of the style — including the
// other tail group — still decodes; a truncated tail never panics and keeps the base.
func TestStyleTailDegradation(t *testing.T) {
	base := SpriteStyle{Tint: true, R: 7}
	b := base.payloadBytes()
	b = append(b, 0, 0, 0)                             // flags2, pathLen, restyle
	b = append(b, tailFlagPaint2|tailFlagGlitchX)      // both groups claimed
	b = append(b, maxPaintSplit+1, 10, 20, 30)         // split out of range → dropped
	b = append(b, GlitchModeCount+5, 1, 2, 3, 4, 5, 6) // unknown mode → Classic, colours kept
	d := styleFromBytes(b)
	if d.PaintSplit != 0 || d.Paint2R != 0 {
		t.Errorf("over-range split leaked: split=%d Paint2R=%d", d.PaintSplit, d.Paint2R)
	}
	if d.GlitchMode != 0 {
		t.Errorf("unknown glitch mode leaked: %d", d.GlitchMode)
	}
	if d.GlitchAR != 1 || d.GlitchBB != 6 {
		t.Errorf("glitch colours lost around the unknown mode: %+v", d)
	}
	if !d.Tint || d.R != 7 {
		t.Errorf("base style lost: %+v", d)
	}

	// Truncated tail: flags claim a group whose bytes are missing.
	tb := base.payloadBytes()
	tb = append(tb, 0, 0, 0, tailFlagPaint2, 40) // only 1 of the 4 paint bytes
	if d := styleFromBytes(tb); d.PaintSplit != 0 || !d.Tint {
		t.Errorf("truncated tail: got split=%d Tint=%v, want 0/true", d.PaintSplit, d.Tint)
	}
}

// TestStyleTailOldFrames pins that yesterday's frames decode unchanged by the new
// tail parsing: a coloured-outline frame (the exact v1.53.x layout) keeps its colour
// and gains no tail fields, and a restyle-only frame stays clean.
func TestStyleTailOldFrames(t *testing.T) {
	old := SpriteStyle{Outline: true, OutlineR: 200, OutlineG: 100, OutlineB: 50}
	b := old.payloadBytes() // same bytes v1.53.x produced for this style
	d := styleFromBytes(b)
	if d != old {
		t.Errorf("v1.53.x coloured-outline frame: got %+v, want %+v", d, old)
	}
	if d.PaintSplit != 0 || d.GlitchMode != 0 {
		t.Errorf("old frame grew tail fields: %+v", d)
	}
}
