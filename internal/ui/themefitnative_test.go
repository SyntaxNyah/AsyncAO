package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Native (1:1) theme fit — the shipped default. Stock AO2 never scales a theme:
// setFixedSize makes the window BE the canvas (AO2-Client charselect.cpp:83, and
// set_courtroom_size() for the courtroom), so a theme's pixels land on screen
// pixels untouched. Our window is resizable, so Native reproduces that by
// pinning the scale at exactly 1.0 and only ever scaling DOWN.
//
// Both ends of that clamp are real, and the reference corpus proves it: 640×480
// (config.MinWindowW/H) is BELOW the canvas of the largest shipped themes and
// ABOVE the canvas of the smallest, so a plain min(sx, sy) would silently
// upscale the small ones and a plain 1.0 would clip the big ones. The FLOOR
// cases below pin "canvas smaller than the window stays 1:1 with bars"; the
// CEILING cases pin "canvas bigger than the window shrinks uniformly and fits".

// Which axis is expected to bind the Native scale. Naming it (instead of storing
// a float literal) keeps the expectation about BEHAVIOUR — "the tighter axis
// wins" — and lets the test compute the number with the same runtime float64
// division the implementation uses, so the compare needs no epsilon.
type nativeFitAxis int

const (
	fitAxisNone  nativeFitAxis = iota // neither axis binds: scale is exactly 1
	fitAxisWidth                      // width is the tighter axis
	fitAxisHeight
)

// stageNativeFit builds a headless App (no SDL: themeLayout is pure geometry
// over themeRects + prefs) whose design canvas is the given size, on Native fit.
func stageNativeFit(t *testing.T, designW, designH int32) *App {
	t.Helper()
	a := testTabApp(t)
	// courtroom + viewport are the two keys themeLayout() requires before it will
	// validate; viewport is a quarter-size stage so it survives the
	// minThemedElementPx floor at every scale exercised here.
	a.themeRects = map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: int(designW), H: int(designH)},
		"viewport":  {X: 0, Y: 0, W: int(designW) / 4, H: int(designH) / 4},
	}
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	a.themeLay.valid = false
	return a
}

// TestThemeFitNativeFloorAndCeiling pins the downscale-only clamp at both ends.
func TestThemeFitNativeFloorAndCeiling(t *testing.T) {
	cases := []struct {
		name             string
		designW, designH int32
		winW, winH       int32
		wantAxis         nativeFitAxis
	}{
		{
			// FLOOR — the corpus's `Viewport` theme (256×256) at the smallest window
			// AsyncAO allows. AO2 would have made the window 256×256; we cannot go
			// below 640×480, so the canvas stays 1:1 and letterboxes permanently.
			// That is the CORRECT reproduction, not a defect: upscaling is the one
			// thing stock AO2 never does.
			name:    "tiny canvas at the minimum window stays 1:1",
			designW: 256, designH: 256,
			winW: config.MinWindowW, winH: config.MinWindowH,
			wantAxis: fitAxisNone,
		},
		{
			// FLOOR — the stock AO2 default theme (714×579) at the shipped default
			// window (1152×864): both axes have room, so 1:1 with bars all round.
			name:    "stock 714x579 canvas in the default window stays 1:1",
			designW: 714, designH: 579,
			winW: config.DefaultWindowW, winH: config.DefaultWindowH,
			wantAxis: fitAxisNone,
		},
		{
			// FLOOR EDGE — the window is EXACTLY the canvas, which is what the theme
			// author designed for (the corpus names directories after that size).
			// Scale 1.0, zero bars: pixel-for-pixel stock AO2.
			name:    "window exactly the canvas has no bars",
			designW: 714, designH: 579,
			winW: 714, winH: 579,
			wantAxis: fitAxisNone,
		},
		{
			// CEILING — `microsoft surface 4k webao` (3240×2014), the only corpus
			// theme whose canvas exceeds a normal window, at the minimum window.
			// Width is the tighter axis here (640/3240 = 0.198 < 480/2014 = 0.238),
			// so it binds and the height gets the bars.
			name:    "4k canvas at the minimum window shrinks uniformly",
			designW: 3240, designH: 2014,
			winW: config.MinWindowW, winH: config.MinWindowH,
			wantAxis: fitAxisWidth,
		},
		{
			// CEILING — the same oversized canvas at a 1080p window: still below 1.0
			// on both axes, and now HEIGHT is the tighter one (1080/2014 = 0.536 <
			// 1920/3240 = 0.593), so the same theme binds on the other axis. Both
			// rows together prove the clamp takes min(sx, sy), not a fixed axis.
			name:    "4k canvas at 1080p shrinks uniformly",
			designW: 3240, designH: 2014,
			winW: 1920, winH: 1080,
			wantAxis: fitAxisHeight,
		},
		{
			// MIXED — one axis has room, the other does not. A naive "scale 1.0 unless
			// BOTH axes are short" would clip the tall axis right off the window.
			name:    "canvas taller than the window shrinks on both axes",
			designW: 391, designH: 814, // the corpus's `Mobile` theme
			winW: 1152, winH: 480,
			wantAxis: fitAxisHeight,
		},
		{
			// MIXED, the other way round — a wide canvas in a tall window.
			name:    "canvas wider than the window shrinks on both axes",
			designW: 1918, designH: 400, // AOHDUltra's width against a short design
			winW: 800, winH: 900,
			wantAxis: fitAxisWidth,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := stageNativeFit(t, tc.designW, tc.designH)
			lay := a.themeLayout(tc.winW, tc.winH)
			if !lay.valid {
				t.Fatal("themeLayout did not validate — the fixture's rects never built a cache")
			}
			if lay.scaleX != lay.scaleY {
				t.Fatalf("Native must be uniform: scaleX=%v scaleY=%v", lay.scaleX, lay.scaleY)
			}
			want := 1.0
			switch tc.wantAxis {
			case fitAxisWidth:
				want = float64(tc.winW) / float64(tc.designW)
			case fitAxisHeight:
				want = float64(tc.winH) / float64(tc.designH)
			}
			if lay.scaleX != want {
				t.Errorf("scale = %v, want %v (axis %d)", lay.scaleX, want, tc.wantAxis)
			}
			if lay.scaleX > 1 {
				t.Errorf("Native must never upscale: scale = %v", lay.scaleX)
			}
			if tc.wantAxis == fitAxisNone {
				// The bars are the whole leftover, split evenly — that is what "centred
				// canvas" means, and it is what makes a small theme sit in the middle
				// of a big window instead of pinned to a corner.
				wantOffX := (tc.winW - tc.designW) / 2
				wantOffY := (tc.winH - tc.designH) / 2
				if lay.offX != wantOffX || lay.offY != wantOffY {
					t.Errorf("centring = (%d,%d), want (%d,%d)", lay.offX, lay.offY, wantOffX, wantOffY)
				}
			}
			// Nothing is ever clipped: the scaled canvas fits inside the window with
			// non-negative bars on both axes. This is the property the min(1, …) clamp
			// exists for, so assert it directly rather than trusting the scale alone.
			cw := int32(float64(tc.designW) * lay.scaleX)
			ch := int32(float64(tc.designH) * lay.scaleY)
			if lay.offX < 0 || lay.offY < 0 || lay.offX+cw > tc.winW || lay.offY+ch > tc.winH {
				t.Errorf("canvas %dx%d at (%d,%d) escapes the %dx%d window", cw, ch, lay.offX, lay.offY, tc.winW, tc.winH)
			}
		})
	}
}

// TestThemeFitNativeIsTheDefault pins that a stock install lands on Native
// WITHOUT touching the setting — the binding product rule is that an imported
// theme looks right with zero settings tweaking, so the default itself has to be
// the correct-looking mode.
func TestThemeFitNativeIsTheDefault(t *testing.T) {
	a := testTabApp(t)
	if got := a.d.Prefs.ThemeFitMode(); got != config.ThemeFitNative {
		t.Fatalf("stock ThemeFitMode = %d, want Native(%d)", got, config.ThemeFitNative)
	}
	a.themeRects = map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: 714, H: 579},
		"viewport":  {X: 0, Y: 0, W: 256, H: 192},
	}
	lay := a.themeLayout(1280, 720)
	if !lay.valid {
		t.Fatal("themeLayout did not validate")
	}
	if lay.scaleX != 1 || lay.scaleY != 1 {
		t.Errorf("a stock install must draw a 714x579 theme 1:1 in a 1280x720 window, got scale (%v,%v)", lay.scaleX, lay.scaleY)
	}
	// The design rect must survive the transform at its authored size — that IS
	// what 1:1 means, and it is the whole point of the default move.
	vp, ok := lay.rect("viewport")
	if !ok {
		t.Fatal("viewport rect vanished from the Native layout")
	}
	if vp.W != 256 || vp.H != 192 {
		t.Errorf("viewport = %dx%d, want the authored 256x192", vp.W, vp.H)
	}
}

// TestThemeFitRowMappingIsExplicit pins that the Settings dropdown maps rows to
// mode CONSTANTS through the table rather than by list position. The old code
// fed the dropdown index straight to SetThemeFit, which was only ever correct
// while the rows happened to sit in constant order — and Native (mode 4, because
// the values are persisted and therefore append-only) has to be listed FIRST as
// the default, which breaks exactly that assumption.
func TestThemeFitRowMappingIsExplicit(t *testing.T) {
	if len(themeFitLabels) != len(themeFitOptions) {
		t.Fatalf("label list has %d rows, table has %d", len(themeFitLabels), len(themeFitOptions))
	}
	if themeFitOptions[0].mode != config.ThemeFitNative {
		t.Errorf("row 0 = mode %d, want Native(%d) — the default is listed first", themeFitOptions[0].mode, config.ThemeFitNative)
	}
	seen := make(map[int]bool, len(themeFitOptions))
	for i, o := range themeFitOptions {
		if seen[o.mode] {
			t.Errorf("mode %d listed twice", o.mode)
		}
		seen[o.mode] = true
		if got := themeFitRow(o.mode); got != i {
			t.Errorf("themeFitRow(%d) = %d, want %d", o.mode, got, i)
		}
		if got := themeFitModeAt(i); got != o.mode {
			t.Errorf("themeFitModeAt(%d) = %d, want %d", i, got, o.mode)
		}
	}
	// Every mode the prefs can hold must have a row, or picking it in Settings
	// would silently jump the user to some other mode.
	for _, mode := range []int{config.ThemeFitStretch, config.ThemeFitLetterbox, config.ThemeFitCrop, config.ThemeFitCustom, config.ThemeFitNative} {
		if !seen[mode] {
			t.Errorf("mode %d has no dropdown row", mode)
		}
	}
	// Out-of-range rows fall back to the default row, never to a negative index or
	// a panic (a prefs file from a newer build, downgraded, can produce one).
	if got := themeFitModeAt(-1); got != config.ThemeFitNative {
		t.Errorf("themeFitModeAt(-1) = %d, want the default Native(%d)", got, config.ThemeFitNative)
	}
	if got := themeFitModeAt(len(themeFitOptions)); got != config.ThemeFitNative {
		t.Errorf("themeFitModeAt(past end) = %d, want the default Native(%d)", got, config.ThemeFitNative)
	}
	if got := themeFitRow(9999); got != 0 {
		t.Errorf("themeFitRow(unknown) = %d, want row 0", got)
	}
}

// TestThemeFitChangeInvalidatesLayout pins that switching fit modes rebuilds the
// geometry cache. themeLayout keys its cache on (window, fit), so a mode change
// that did not reach the cache would leave the courtroom drawn at the OLD fit
// until the next resize — which is how a fit toggle "does nothing" in the field.
func TestThemeFitChangeInvalidatesLayout(t *testing.T) {
	a := stageNativeFit(t, 714, 579)
	// Deliberately vars, not consts: the expectations below must be computed with
	// the same RUNTIME float64 division the implementation performs, not folded by
	// the compiler at arbitrary precision.
	var w, h int32 = 1280, 720
	if s := a.themeLayout(w, h).scaleX; s != 1 {
		t.Fatalf("Native scale = %v, want 1", s)
	}
	a.d.Prefs.SetThemeFit(config.ThemeFitStretch)
	lay := a.themeLayout(w, h)
	if lay.scaleX != float64(w)/714 || lay.scaleY != float64(h)/579 {
		t.Errorf("after the mode change scale = (%v,%v), want the Stretch per-axis fill", lay.scaleX, lay.scaleY)
	}
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	if s := a.themeLayout(w, h).scaleX; s != 1 {
		t.Errorf("switching back to Native left scale at %v, want 1", s)
	}
}
