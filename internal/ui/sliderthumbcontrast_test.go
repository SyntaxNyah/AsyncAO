package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// GH #50 ("default color of BLACK SLIDER BALL is SCARY!!!"): Ctx.Slider and
// Ctx.VScrollbar both painted their idle thumb with the raw ColPanelHi
// against a ColPanel track, and every built-in chrome preset put those two
// within 28 luma units of each other — under the kit's own 48-unit
// minInkSkinContrast readability floor (see sliderIdleThumbColor, ui.go).
// These tests drive the REAL draw path (an actual Ctx.Slider/VScrollbar call
// into an offscreen render target, with the painted pixel read back) rather
// than re-checking sliderIdleThumbColor's arithmetic in isolation — a test
// that only re-derived the math would stay green even if a callsite reverted
// to the raw `col := ColPanelHi` this fix replaces.

const (
	// sliderThumbFixtureW/H size the offscreen target the tests paint into —
	// just big enough for one Slider track or the (taller) VScrollbar track
	// below with room either side, so ReadPixels stays cheap per preset.
	// H=160 must clear the VScrollbar track's Y(20)+H(120) below with margin.
	sliderThumbFixtureW = int32(200)
	sliderThumbFixtureH = int32(160)
	// sliderThumbTrackW/H is the Slider track geometry driven below; wide
	// enough that the 10px thumb (ui.go's sliderThumbW) sits well clear of
	// both rect edges wherever `value` places it.
	sliderThumbTrackW = int32(120)
	sliderThumbTrackH = int32(16)
	// testSliderThumbW mirrors Ctx.Slider's own private sliderThumbW (ui.go) —
	// it's unexported (a draw-geometry constant, not part of the contract this
	// file tests), so the pixel-center math below needs its own copy to find
	// the thumb rect it must sample.
	testSliderThumbW = int32(10)
)

// sliderThumbFixture builds a real Ctx plus an offscreen capture target, and
// returns a cleanup that also restores the package-level chrome palette
// globals: applyChromePreset (like chrome_test.go's own fixtures) mutates
// them in place, and this file drives every built-in preset through it.
func sliderThumbFixture(t *testing.T) (*App, *render.CaptureTarget, func()) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	ctx, err := NewCtx(ren)
	if err != nil {
		cleanup()
		t.Skipf("Ctx unavailable: %v", err)
	}
	ct, err := render.NewCaptureTarget(ren, sliderThumbFixtureW, sliderThumbFixtureH)
	if err != nil {
		cleanup()
		t.Skipf("capture target unavailable: %v", err)
	}
	a := &App{ctx: ctx}
	return a, ct, func() {
		ct.Close()
		activeKitColors = defaultKitColors
		applyThemePalette(theme.Palette{})
		cleanup()
	}
}

// sliderIdleTrackRect is the Slider's track rect for the fixture above,
// parked away from the origin so a stray uncleared pixel at (0,0) can't
// masquerade as the sample.
var sliderIdleTrackRect = sdl.Rect{X: 40, Y: 20, W: sliderThumbTrackW, H: sliderThumbTrackH}

// sampleSliderIdleThumb draws one idle (unhovered, undragged) Ctx.Slider at
// half its range and returns the painted color at the thumb's center pixel.
func sampleSliderIdleThumb(t *testing.T, a *App, ct *render.CaptureTarget) sdl.Color {
	t.Helper()
	c := a.ctx
	c.mouseX, c.mouseY = -1000, -1000 // nowhere near the track: forces the idle branch
	c.mouseDown = false
	c.dragID = ""
	const maxVal = int32(100)
	frame, err := ct.Capture(c.Ren, func(sdl.Rect) {
		c.Slider("thumb-contrast-test", sliderIdleTrackRect, maxVal/2, maxVal)
	})
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	// Half of a 120-10=110px span lands the thumb's left edge at X=40+55=95;
	// its center is a few px further right and vertically mid-track.
	cx := sliderIdleTrackRect.X + (sliderIdleTrackRect.W-testSliderThumbW)/2 + testSliderThumbW/2
	cy := sliderIdleTrackRect.Y + sliderIdleTrackRect.H/2
	rc := frame.RGBAAt(int(cx), int(cy))
	return sdl.Color{R: rc.R, G: rc.G, B: rc.B, A: rc.A}
}

// sampleVScrollbarIdleThumb draws one idle Ctx.VScrollbar at a scroll offset
// that keeps the thumb clear of the track's top/bottom edges, and returns the
// painted color at the thumb's center pixel.
func sampleVScrollbarIdleThumb(t *testing.T, a *App, ct *render.CaptureTarget) sdl.Color {
	t.Helper()
	c := a.ctx
	c.mouseX, c.mouseY = -1000, -1000
	c.mouseDown = false
	c.dragID = ""
	track := sdl.Rect{X: 40, Y: 20, W: 16, H: 120}
	// content/visible/scroll are chosen so the thumb (thumbH=120*100/300=40px)
	// lands centered in the track: span=track.H-thumbH=80, and scroll=maxScroll/2
	// (maxScroll=content-visible=200) puts thumbY at track.Y+track.H/2-thumbH/2 —
	// i.e. the track's own center point below is inside the thumb rect, not past
	// its bottom edge.
	const content, visible, scroll = int32(300), int32(100), int32(100)
	frame, err := ct.Capture(c.Ren, func(sdl.Rect) {
		c.VScrollbar("scrollbar-contrast-test", track, scroll, content, visible)
	})
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	cx := track.X + track.W/2
	cy := track.Y + track.H/2 // the thumb spans the track's middle at this scroll offset
	rc := frame.RGBAAt(int(cx), int(cy))
	return sdl.Color{R: rc.R, G: rc.G, B: rc.B, A: rc.A}
}

// TestSliderIdleThumbClearsReadabilityFloor is the encapsulation seam for GH
// #50: it drives EVERY entry in the real chromePresets table (not a
// hand-written color list) through the real Ctx.Slider draw path and demands
// the painted idle-thumb pixel clear minInkSkinContrast against the live
// ColPanel — so a 7th preset that regresses this, or a callsite that reverts
// to the raw ColPanelHi, both fail the suite.
func TestSliderIdleThumbClearsReadabilityFloor(t *testing.T) {
	a, ct, cleanup := sliderThumbFixture(t)
	defer cleanup()

	for i := range chromePresets {
		p := &chromePresets[i]
		t.Run(p.key, func(t *testing.T) {
			a.applyChromePreset(p.key)
			trackLuma := colLuma(ColPanel)
			got := sampleSliderIdleThumb(t, a, ct)
			if d := absInt(colLuma(got) - trackLuma); d < minInkSkinContrast {
				t.Errorf("%s: idle slider thumb %+v vs track %+v (ColPanel) contrast %d < floor %d",
					p.key, got, ColPanel, d, minInkSkinContrast)
			}
		})
	}
}

// TestVScrollbarIdleThumbClearsReadabilityFloor is TestSliderIdleThumbClears-
// ReadabilityFloor's sibling for Ctx.VScrollbar: the dossier for #50 found
// the identical `col := ColPanelHi` / hover-flips-to-ColAccent pattern there,
// a second, unnamed instance of the same class of bug.
func TestVScrollbarIdleThumbClearsReadabilityFloor(t *testing.T) {
	a, ct, cleanup := sliderThumbFixture(t)
	defer cleanup()

	for i := range chromePresets {
		p := &chromePresets[i]
		t.Run(p.key, func(t *testing.T) {
			a.applyChromePreset(p.key)
			trackLuma := colLuma(ColPanel)
			got := sampleVScrollbarIdleThumb(t, a, ct)
			if d := absInt(colLuma(got) - trackLuma); d < minInkSkinContrast {
				t.Errorf("%s: idle scrollbar thumb %+v vs track %+v (ColPanel) contrast %d < floor %d",
					p.key, got, ColPanel, d, minInkSkinContrast)
			}
		})
	}
}

// TestSliderIdleThumbFloorSurvivesADerivedPanelHi covers the OTHER route to
// a near-black ColPanelHi: an AO2 theme that sets Panel but not PanelHi,
// which applyThemePalette derives as Panel×130% (ui.go) — 130% of a
// near-black stays near-black, reproducing #50 without touching any
// chromePreset table entry. Exercises applyThemePalette directly (the AO2
// theme-overlay path), proving the fix isn't scoped to only the six
// hand-authored presets.
func TestSliderIdleThumbFloorSurvivesADerivedPanelHi(t *testing.T) {
	a, ct, cleanup := sliderThumbFixture(t)
	defer cleanup()

	activeKitColors = defaultKitColors
	nearBlack := theme.RGB{R: 10, G: 10, B: 12}
	applyThemePalette(theme.Palette{Panel: &nearBlack})
	a.themePalette = theme.Palette{Panel: &nearBlack}

	trackLuma := colLuma(ColPanel)
	got := sampleSliderIdleThumb(t, a, ct)
	if d := absInt(colLuma(got) - trackLuma); d < minInkSkinContrast {
		t.Errorf("derived near-black PanelHi: idle slider thumb %+v vs track %+v contrast %d < floor %d",
			got, ColPanel, d, minInkSkinContrast)
	}
}

// TestSliderHoverStillFlipsToAccent pins that the #50 fix touched ONLY the
// idle branch: hovering or dragging the thumb must still switch it to the
// live ColAccent, unchanged from before this fix (the existing interaction
// feedback the fix must not regress).
func TestSliderHoverStillFlipsToAccent(t *testing.T) {
	a, ct, cleanup := sliderThumbFixture(t)
	defer cleanup()
	a.applyChromePreset("dark")

	c := a.ctx
	// Park the pointer square inside the track (and thus inside the thumb's
	// grab zone) so Slider's hover check is true.
	c.mouseX, c.mouseY = sliderIdleTrackRect.X+sliderIdleTrackRect.W/2, sliderIdleTrackRect.Y+sliderIdleTrackRect.H/2
	c.mouseDown = false
	c.dragID = ""
	const maxVal = int32(100)
	frame, err := ct.Capture(c.Ren, func(sdl.Rect) {
		c.Slider("thumb-hover-test", sliderIdleTrackRect, maxVal/2, maxVal)
	})
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	cx := sliderIdleTrackRect.X + (sliderIdleTrackRect.W-testSliderThumbW)/2 + testSliderThumbW/2
	cy := sliderIdleTrackRect.Y + sliderIdleTrackRect.H/2
	rc := frame.RGBAAt(int(cx), int(cy))
	got := sdl.Color{R: rc.R, G: rc.G, B: rc.B, A: rc.A}
	if got != ColAccent {
		t.Errorf("hovering the thumb must still paint ColAccent %+v, got %+v", ColAccent, got)
	}
}
