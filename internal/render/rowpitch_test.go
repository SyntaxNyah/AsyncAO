package render

// The Qt row-pitch override, driven rather than mirrored.
//
// These build the two raster shapes directly (same package) instead of going
// through Rasterize / RasterizeAnimated, because the override is pure geometry
// and a real raster would drag SDL, a renderer and a face in to prove arithmetic
// about one int32. What is under test is UseRowPitch itself and the re-stride it
// performs — both are called for real; nothing here re-implements them.

import "testing"

// TestUseRowPitchOnlyEverRaises is the safety contract the whole mechanism rests
// on. render.UseRowPitch is fed a number from a SECOND metric source (Qt's, via a
// theme font's OS/2 table); if it could lower the advance, a face whose Qt pitch
// is the smaller of the two — Arial is, by 2 px at 40 px — would start drawing
// its rows TIGHTER than the glyph box SDL_ttf measured, which is the exact defect
// FontLineSpacing's floor exists to prevent.
func TestUseRowPitchOnlyEverRaises(t *testing.T) {
	m := &MessageRaster{lineH: 24, devScale: DefaultDevScale}
	m.UseRowPitch(35)
	if got := m.RowPitch(); got != 35 {
		t.Errorf("a larger pitch must be adopted: RowPitch = %d, want 35", got)
	}
	for _, lower := range []int32{34, 24, 1, 0, -5} {
		m.UseRowPitch(lower)
		if got := m.RowPitch(); got != 35 {
			t.Fatalf("UseRowPitch(%d) lowered the advance to %d — it must never tighten", lower, got)
		}
	}
	// Zero is the "no Qt opinion" value every consumer passes for an unthemed
	// message, and it has to be a no-op on a fresh raster too.
	fresh := &MessageRaster{lineH: 19, devScale: DefaultDevScale}
	fresh.UseRowPitch(0)
	if got := fresh.RowPitch(); got != 19 {
		t.Errorf("UseRowPitch(0) changed a fresh raster's advance to %d, want 19", got)
	}
}

// TestUseRowPitchMovesTheDrawnStepAndTheGeometryTogether is the seam test: the
// override has to move Draw's step and the geometry the UI measures with (LineH,
// Height) as ONE thing, or a selection highlight and an auto-grown chatbox go on
// using the pre-override pitch and stripe.
func TestUseRowPitchMovesTheDrawnStepAndTheGeometryTogether(t *testing.T) {
	const rows = 3
	m := &MessageRaster{lineH: 24, devScale: DefaultDevScale, lines: make([]rasterLine, rows)}
	before := m.Height()
	m.UseRowPitch(35)
	if got, want := m.LineH(), int32(35); got != want {
		t.Errorf("LineH = %d, want %d", got, want)
	}
	if got, want := m.linePitch(false), int32(35); got != want {
		t.Errorf("the draw step = %d, want %d — Draw and LineH must not diverge", got, want)
	}
	if got, want := m.Height(), int32(rows*35); got != want {
		t.Errorf("Height = %d, want %d (was %d)", got, want, before)
	}
}

// TestAnimatedUseRowPitchRestridesTheRows pins the difference between the two
// shapes: AnimatedText bakes a per-glyph y at layout time, so raising the field
// alone would leave every row past the first on the old step.
func TestAnimatedUseRowPitchRestridesTheRows(t *testing.T) {
	const old = 10
	at := &AnimatedText{lineH: old, total: 4, runes: []animRune{
		{y: 0 * old}, {y: 1 * old}, {y: 2 * old}, {y: 3 * old},
	}}
	at.UseRowPitch(16)
	if got := at.RowPitch(); got != 16 {
		t.Fatalf("RowPitch = %d, want 16", got)
	}
	for i := range at.runes {
		if want := int32(i * 16); at.runes[i].y != want {
			t.Errorf("row %d sits at y=%d, want %d — the re-stride missed it", i, at.runes[i].y, want)
		}
	}
	// Height is derived from the laid-out rows plus one advance, so it has to
	// follow without a second correction.
	if got, want := at.Height(), int32(3*16+16); got != want {
		t.Errorf("Height = %d, want %d", got, want)
	}
	// And the same never-tighten rule.
	at.UseRowPitch(4)
	if got := at.RowPitch(); got != 16 {
		t.Errorf("UseRowPitch(4) tightened the animated advance to %d", got)
	}
	if at.runes[3].y != 48 {
		t.Errorf("a refused override still moved the rows: y=%d, want 48", at.runes[3].y)
	}
}

// TestUseRowPitchIsNilSafe: both raster shapes are nil on the paths where a build
// failed (ensureChatRaster's fallback), and the call sites apply the override
// unconditionally rather than re-testing for nil at every one of them.
func TestUseRowPitchIsNilSafe(t *testing.T) {
	var m *MessageRaster
	var at *AnimatedText
	m.UseRowPitch(35)
	at.UseRowPitch(35)
	if m.RowPitch() != 0 || at.RowPitch() != 0 {
		t.Error("a nil raster must report a zero pitch, not panic or invent one")
	}
}
