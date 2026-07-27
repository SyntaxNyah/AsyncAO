package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Char select's own canvas (#20). These tests pin three things the screen cannot
// work without: the embedded default-theme table (AO2 falls back PER KEY and we
// have no base/themes/ tree to fall back through), the canvas transform (the same
// ThemeFit the courtroom uses, under the app-chrome band), and the rule that the
// canvas and everything laid out on it share ONE transform — which is the whole
// structural fix.

// writeDesignINI drops a courtroom_design.ini for theme `name` under a fresh temp
// root and returns a loaded theme.Theme. No SDL, no App: this is the exact input
// the theme-apply goroutine hands readCharSelectDesign.
func writeDesignINI(t *testing.T, name, body string) *theme.Theme {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, theme.ThemesDirName, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, theme.DesignFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write ini: %v", err)
	}
	th, err := theme.Load(name, []string{root})
	if err != nil {
		t.Fatalf("theme.Load: %v", err)
	}
	return th
}

// TestCharSelectDefaultTableIsStockAO2 pins every value in the embedded table
// against AO2-Client/bin/base/themes/default/courtroom_design.ini. A typo here is
// invisible in the client and wrong on 28 of the corpus's 97 design files, so the
// numbers get an explicit, independent transcription.
func TestCharSelectDefaultTableIsStockAO2(t *testing.T) {
	want := map[string]theme.Rect{
		"char_select":       {X: 0, Y: 0, W: 714, H: 668},
		"char_list":         {X: 25, Y: 36, W: 190, H: 596},
		"back_to_lobby":     {X: 5, Y: 5, W: 91, H: 23},
		"char_password":     {X: 297, Y: 7, W: 120, H: 22},
		"char_buttons":      {X: 226, Y: 36, W: 518, H: 596},
		"char_select_left":  {X: 100, Y: 5, W: 43, H: 24},
		"char_select_right": {X: 146, Y: 5, W: 43, H: 24},
		"spectator":         {X: 317, Y: 640, W: 80, H: 23},
		"char_search":       {X: 420, Y: 7, W: 120, H: 22},
		"char_passworded":   {X: 545, Y: 7, W: 100, H: 22},
		"char_taken":        {X: 635, Y: 7, W: 80, H: 22},
	}
	if len(charSelectDefaultRects) != len(want) {
		t.Fatalf("table has %d keys, stock AO2 declares %d", len(charSelectDefaultRects), len(want))
	}
	for key, w := range want {
		got, ok := charSelectDefaultRects[key]
		if !ok {
			t.Errorf("%s missing from the default table", key)
			continue
		}
		if got != w {
			t.Errorf("%s = %+v, want the stock %+v", key, got, w)
		}
	}
	if charSelectDefaultSpacingX != 7 || charSelectDefaultSpacingY != 7 {
		t.Errorf("default spacing = %d,%d, want the stock 7,7",
			charSelectDefaultSpacingX, charSelectDefaultSpacingY)
	}
	if charSelectButtonPx != 60 {
		t.Errorf("charSelectButtonPx = %d, want AO2's button_width 60", charSelectButtonPx)
	}
}

// TestCharSelectDefaultTableReproducesTheStockGrid checks the table against the ART
// it has to line up with, not just against the INI. A dark-run scan of the stock
// 714x668 charselect_background.png finds 7 runs from x=226 and 9 from y=36, each
// 60 px wide, pitch 67 — so AO2's own formula, fed the table, must yield 7x9 and
// place the last column at 628. This is the check that would have caught issue #20.
func TestCharSelectDefaultTableReproducesTheStockGrid(t *testing.T) {
	r := charSelectDefaultRects["char_buttons"]
	const wantCols, wantRows, wantPitch, wantLastCol = 7, 9, 67, 628
	// AO2-Client charselect.cpp:160-162, verbatim.
	cols := ((r.W - charSelectButtonPx) / (charSelectDefaultSpacingX + charSelectButtonPx)) + 1
	rows := ((r.H - charSelectButtonPx) / (charSelectDefaultSpacingY + charSelectButtonPx)) + 1
	if cols != wantCols || rows != wantRows {
		t.Errorf("stock grid = %dx%d, want %dx%d (the art's own slot frames)", cols, rows, wantCols, wantRows)
	}
	if pitch := charSelectDefaultSpacingX + charSelectButtonPx; pitch != wantPitch {
		t.Errorf("column pitch = %d, want the art's %d", pitch, wantPitch)
	}
	if last := r.X + (wantCols-1)*wantPitch; last != wantLastCol {
		t.Errorf("last column starts at %d, want the art's %d", last, wantLastCol)
	}
	// And the load-bearing oddity the grid stage must not "fix": the rect
	// deliberately overhangs the canvas. Qt clips the paint and leaves the origin
	// alone, so the columns still land on the slot frames.
	canvas := charSelectDefaultRects["char_select"]
	if r.X+r.W <= canvas.W {
		t.Fatalf("char_buttons right edge %d no longer overhangs the %d-wide canvas — "+
			"the no-shift rule in charselectlayout.go was written for exactly this",
			r.X+r.W, canvas.W)
	}
}

// TestCharSelectDesignFallsBackPerKey uses AceAttorney2x's REAL key set — the theme
// in issue #20's screenshot declares only char_search, char_passworded and
// char_taken — and pins that the other nine keys still resolve. AO2 gets them from
// the default theme on disk; a streaming client gets them from the table or not at
// all, which is what left the grid drawing at its own metrics over stock art.
func TestCharSelectDesignFallsBackPerKey(t *testing.T) {
	th := writeDesignINI(t, "AceAttorney2x", `courtroom = 0, 0, 944, 600
viewport = 0, 0, 512, 384
char_search = 420, 7, 120, 22
char_passworded = 545, 7, 100, 22
char_taken = 635, 7, 80, 22
`)
	d := readCharSelectDesign(th)
	// Declared keys come from the theme...
	if got := d.r["char_search"]; got != (theme.Rect{X: 420, Y: 7, W: 120, H: 22}) {
		t.Errorf("char_search = %+v, want the theme's own value", got)
	}
	// ...and every silent key from the stock table.
	for _, key := range []string{"char_select", "char_buttons", "char_list", "back_to_lobby",
		"char_password", "char_select_left", "char_select_right", "spectator"} {
		if got, want := d.r[key], charSelectDefaultRects[key]; got != want {
			t.Errorf("%s = %+v, want the inherited stock %+v", key, got, want)
		}
	}
	if d.spacing != [2]int{7, 7} {
		t.Errorf("char_button_spacing = %v, want the inherited stock 7,7", d.spacing)
	}
	w, h := d.canvasSize()
	if w != 714 || h != 668 {
		t.Errorf("canvas = %dx%d, want the inherited stock 714x668", w, h)
	}
}

// TestCharSelectDesignHonoursDeclaredValues is the mirror: a theme that declares the
// keys must win outright. The fixture is the corpus theme `AAI` verbatim — it is the
// case that proves char_list is genuinely optional (AAI ships none and lets the grid
// use the full 663 px width), and its 10x9 page is one of the independent
// reproductions of AO2's formula.
func TestCharSelectDesignHonoursDeclaredValues(t *testing.T) {
	th := writeDesignINI(t, "AAI", `char_select = 0, 0, 714, 668
char_buttons = 25, 36, 663, 596
char_button_spacing = 7, 7
char_select_left = 2, 325, 20, 20
char_select_right = 691, 325, 20, 20
`)
	d := readCharSelectDesign(th)
	if got := d.r["char_buttons"]; got != (theme.Rect{X: 25, Y: 36, W: 663, H: 596}) {
		t.Errorf("char_buttons = %+v, want the theme's own value", got)
	}
	if got := d.r["char_select_left"]; got != (theme.Rect{X: 2, Y: 325, W: 20, H: 20}) {
		t.Errorf("char_select_left = %+v, want the theme's own value", got)
	}
	if d.spacing != [2]int{7, 7} {
		t.Fatalf("char_button_spacing = %v, want the declared 7,7", d.spacing)
	}
	cols := ((d.r["char_buttons"].W - charSelectButtonPx) / (d.spacing[0] + charSelectButtonPx)) + 1
	rows := ((d.r["char_buttons"].H - charSelectButtonPx) / (d.spacing[1] + charSelectButtonPx)) + 1
	if cols != 10 || rows != 9 {
		t.Errorf("AAI page = %dx%d, want 10x9", cols, rows)
	}
	// AAI declares no char_list, so it must still come from the stock table — that is
	// the per-key fallback doing its job on a theme that DOES declare most keys.
	if got, want := d.r["char_list"], charSelectDefaultRects["char_list"]; got != want {
		t.Errorf("char_list = %+v, want the inherited stock %+v", got, want)
	}
}

// TestCharSelectSpacingHonoursALegitimateZero pins designPair's contract on THIS key.
// No corpus theme ships `char_button_spacing = 0, 0` today, but twelve ship
// `emote_button_spacing = 0, 0` and mean flush buttons — AO2 applies no positivity
// test to either (get_button_spacing, text_file_functions.cpp:193-216). Substituting
// the stock 7 for a declared 0 would walk the grid a pixel further off the artwork
// with every column, which is the bug class issue #20 is about.
func TestCharSelectSpacingHonoursALegitimateZero(t *testing.T) {
	th := writeDesignINI(t, "flush", "char_button_spacing = 0, 0\n")
	if got := readCharSelectDesign(th).spacing; got != [2]int{0, 0} {
		t.Errorf("char_button_spacing = %v, want the declared 0,0", got)
	}
	// A MISSING key is a different thing entirely and still inherits the stock value.
	bare := writeDesignINI(t, "bare", "char_buttons = 226, 36, 518, 596\n")
	if got := readCharSelectDesign(bare).spacing; got != [2]int{7, 7} {
		t.Errorf("absent char_button_spacing = %v, want the inherited stock 7,7", got)
	}
}

// TestCharSelectCanvasIgnoresRectPosition pins A3's correction: AO2 calls
// setFixedSize(width, height), which takes no position, so char_select's x/y are
// meaningless. Three corpus themes declare `char_select = 0, 121, ...`; reading the
// rect as positioned would push all three down by 121 px.
func TestCharSelectCanvasIgnoresRectPosition(t *testing.T) {
	th := writeDesignINI(t, "WebAO Client", "char_select = 0, 121, 1713, 872\n")
	d := readCharSelectDesign(th)
	w, h := d.canvasSize()
	if w != 1713 || h != 872 {
		t.Fatalf("canvas = %dx%d, want 1713x872 (width/height only)", w, h)
	}
	// And a degenerate declaration falls back rather than collapsing the screen,
	// like AO2's `if (width < 0 || height < 0) setFixedSize(714, 668)`.
	bad := writeDesignINI(t, "broken", "char_select = 0, 0, 0, 0\n")
	if w, h := readCharSelectDesign(bad).canvasSize(); w != 714 || h != 668 {
		t.Errorf("degenerate canvas = %dx%d, want the stock 714x668 fallback", w, h)
	}
}

// stageCharSelect builds a headless App on the theme canvas: a themed courtroom
// (the gate charSelectThemed reuses) plus the char-select design under test.
func stageCharSelect(t *testing.T, d charSelectDesign) *App {
	t.Helper()
	a := testTabApp(t)
	a.themeRects = map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: 944, H: 600},
		"viewport":  {X: 0, Y: 0, W: 256, H: 192},
	}
	a.charSelDesign = d
	a.charSelLay.valid = false
	return a
}

// stockCharSelectDesign is the table with nothing declared — AceAttorney2x's
// effective geometry, and the case 28 of 97 corpus design files land on.
func stockCharSelectDesign() charSelectDesign {
	return readCharSelectDesign(nil)
}

// TestCharSelectStaysClassicWithoutATheme pins that a themeless install is
// untouched: no canvas, no letterbox, the pre-#20 full-window screen. The canvas is
// a THEME feature — inventing a 714x668 box for someone with no theme art to align
// to would be a regression, not a fix.
func TestCharSelectStaysClassicWithoutATheme(t *testing.T) {
	a := testTabApp(t)
	a.charSelDesign = stockCharSelectDesign() // design present, but no themed courtroom
	if lay := a.charSelectLayout(1152, 864); lay.valid {
		t.Error("char select claimed a theme canvas with no themed courtroom geometry")
	}
	// Themed geometry, but the user turned the theme layout off: same answer, because
	// "use the theme's layout" is one concept and one preference for both canvases.
	// Deliberately WITHOUT a manual invalidation — the toggle changes no window, band,
	// fit or theme, so if it is not in the cache key the screen stays letterboxed.
	a = stageCharSelect(t, stockCharSelectDesign())
	if !a.charSelectLayout(1152, 864).valid {
		t.Fatal("fixture did not build a canvas to begin with")
	}
	a.d.Prefs.SetThemeLayout(false)
	if lay := a.charSelectLayout(1152, 864); lay.valid {
		t.Error("char select kept the theme canvas after the theme layout was switched off")
	}
	a.d.Prefs.SetThemeLayout(true)
	if lay := a.charSelectLayout(1152, 864); !lay.valid {
		t.Error("switching the theme layout back on did not restore the canvas")
	}
	// And with no design table at all (a theme whose INI never loaded).
	a = stageCharSelect(t, charSelectDesign{})
	if lay := a.charSelectLayout(1152, 864); lay.valid {
		t.Error("char select claimed a canvas with no resolved design table")
	}
}

// TestCharSelectCanvasHonoursThemeFit pins locked decision 5: char select uses the
// SAME fit mode as the courtroom, through the same arithmetic. Native is the shipped
// default and must be exactly 1:1 whenever the canvas fits.
func TestCharSelectCanvasHonoursThemeFit(t *testing.T) {
	const w, h int32 = 1152, 864
	a := stageCharSelect(t, stockCharSelectDesign())
	for _, tc := range []struct {
		name string
		mode int
		want func(band int32) (float64, float64)
	}{
		{"native is 1:1", config.ThemeFitNative, func(int32) (float64, float64) { return 1, 1 }},
		{"letterbox is uniform", config.ThemeFitLetterbox, func(band int32) (float64, float64) {
			s := float64(h-band) / 668
			if sx := float64(w) / 714; sx < s {
				s = sx
			}
			return s, s
		}},
		{"stretch is per-axis", config.ThemeFitStretch, func(band int32) (float64, float64) {
			return float64(w) / 714, float64(h-band) / 668
		}},
		{"crop is uniform and upscales", config.ThemeFitCrop, func(band int32) (float64, float64) {
			s := float64(h-band) / 668
			if sx := float64(w) / 714; sx > s {
				s = sx
			}
			return s, s
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.d.Prefs.SetThemeFit(tc.mode)
			a.charSelLay.valid = false
			lay := a.charSelectLayout(w, h)
			if !lay.valid {
				t.Fatal("canvas did not build")
			}
			wantX, wantY := tc.want(a.topChromeH())
			if lay.scaleX != wantX || lay.scaleY != wantY {
				t.Errorf("scale = (%v,%v), want (%v,%v)", lay.scaleX, lay.scaleY, wantX, wantY)
			}
			if lay.area.W != int32(714*wantX) || lay.area.H != int32(668*wantY) {
				t.Errorf("canvas = %dx%d, want %dx%d", lay.area.W, lay.area.H, int32(714*wantX), int32(668*wantY))
			}
		})
	}
}

// TestCharSelectCanvasNeverPaintsIntoTheChromeBand walks ALL FIVE fit modes,
// including the two that deliberately overflow. The invariant is not "the canvas is
// below the band" — Crop and Custom can legitimately push art up there, where the
// menu bar and the tab strip paint over it — it is that the region this screen may
// paint and hit-test in never reaches into somebody else's chrome.
func TestCharSelectCanvasNeverPaintsIntoTheChromeBand(t *testing.T) {
	modes := []struct {
		name string
		mode int
	}{
		{"native", config.ThemeFitNative},
		{"letterbox", config.ThemeFitLetterbox},
		{"stretch", config.ThemeFitStretch},
		{"crop", config.ThemeFitCrop},
		{"custom", config.ThemeFitCustom},
	}
	windows := []struct{ w, h int32 }{
		{config.MinWindowW, config.MinWindowH},
		{1152, 864},
		{1920, 1080},
		{714, 668}, // the canvas exactly
	}
	for _, m := range modes {
		for _, win := range windows {
			a := stageCharSelect(t, stockCharSelectDesign())
			// A real band: one open tab docks the server-tab strip under the menu bar.
			a.tabs = []*courtTab{{}}
			a.activeTab = 0
			a.d.Prefs.SetThemeFit(m.mode)
			if m.mode == config.ThemeFitCustom {
				a.d.Prefs.SetThemeFitZoom(160) // zoom past the window, then pan up
				a.d.Prefs.SetThemeFitPan(-20, -30)
			}
			a.charSelLay.valid = false
			band := a.topChromeH()
			if band <= 0 {
				t.Fatal("fixture produced no chrome band — the test would prove nothing")
			}
			lay := a.charSelectLayout(win.w, win.h)
			if !lay.valid {
				t.Fatalf("%s @ %dx%d: canvas did not build", m.name, win.w, win.h)
			}
			if lay.clip.Y < band {
				t.Errorf("%s @ %dx%d: paint region starts at y=%d, inside the %d px chrome band",
					m.name, win.w, win.h, lay.clip.Y, band)
			}
			if lay.clip.H < 0 || lay.clip.W < 0 {
				t.Errorf("%s @ %dx%d: inverted paint region %+v", m.name, win.w, win.h, lay.clip)
			}
			// And never past the window's other three edges either — Crop and Custom
			// both overflow them by design.
			if lay.clip.X < 0 || lay.clip.X+lay.clip.W > win.w || lay.clip.Y+lay.clip.H > win.h {
				t.Errorf("%s @ %dx%d: paint region %+v escapes the window", m.name, win.w, win.h, lay.clip)
			}
			// In the three modes that cannot overflow, the WHOLE canvas is below the
			// band and inside the window — the property the courtroom band pins too.
			if m.mode == config.ThemeFitNative || m.mode == config.ThemeFitLetterbox || m.mode == config.ThemeFitStretch {
				if lay.area.Y < band {
					t.Errorf("%s @ %dx%d: canvas top y=%d is above the %d px band",
						m.name, win.w, win.h, lay.area.Y, band)
				}
				if lay.area.X < 0 || lay.area.X+lay.area.W > win.w || lay.area.Y+lay.area.H > win.h {
					t.Errorf("%s @ %dx%d: canvas %+v escapes the window", m.name, win.w, win.h, lay.area)
				}
				if lay.clip != lay.area {
					t.Errorf("%s @ %dx%d: clip %+v should equal the canvas %+v when nothing overflows",
						m.name, win.w, win.h, lay.clip, lay.area)
				}
			}
		}
	}
}

// TestCharSelectRectsRideTheCanvasTransform is the structural half of the #20 fix:
// every widget rect is the canvas rect's own transform applied to the design
// position, so the backdrop and everything on it move together in every mode —
// Stretch included, where the axes scale differently.
func TestCharSelectRectsRideTheCanvasTransform(t *testing.T) {
	const w, h int32 = 1400, 900
	for _, mode := range []int{config.ThemeFitNative, config.ThemeFitLetterbox, config.ThemeFitStretch, config.ThemeFitCrop} {
		a := stageCharSelect(t, stockCharSelectDesign())
		a.d.Prefs.SetThemeFit(mode)
		a.charSelLay.valid = false
		lay := a.charSelectLayout(w, h)
		if !lay.valid {
			t.Fatalf("mode %d: canvas did not build", mode)
		}
		for key, design := range charSelectDefaultRects {
			if key == charSelectCanvasKey {
				if _, ok := lay.rect(key); ok {
					t.Errorf("mode %d: the canvas key leaked into the widget map", mode)
				}
				continue
			}
			got, ok := lay.rect(key)
			if !ok {
				t.Errorf("mode %d: %s vanished from the layout", mode, key)
				continue
			}
			want := sdl.Rect{
				X: lay.area.X + int32(float64(design.X)*lay.scaleX),
				Y: lay.area.Y + int32(float64(design.Y)*lay.scaleY),
				W: int32(float64(design.W) * lay.scaleX),
				H: int32(float64(design.H) * lay.scaleY),
			}
			if got != want {
				t.Errorf("mode %d: %s = %+v, want the canvas transform's %+v", mode, key, got, want)
			}
		}
	}
}

// TestCharSelectOverhangingRectIsNotShifted pins the deliberate divergence from the
// courtroom. clampRectInto SHIFTS an overhanging widget back inside the canvas;
// applied here it would move char_buttons 30 px left of the slot frames painted
// behind it — issue #20's exact symptom, recreated by the cure. spectator is the
// companion case: the courtroom design space DROPS it (Y=640 > courtroom.H=579),
// and on its own canvas it must survive.
func TestCharSelectOverhangingRectIsNotShifted(t *testing.T) {
	a := stageCharSelect(t, stockCharSelectDesign())
	lay := a.charSelectLayout(1152, 864) // Native: scale 1, so design px == window px
	if !lay.valid {
		t.Fatal("canvas did not build")
	}
	if lay.scaleX != 1 || lay.scaleY != 1 {
		t.Fatalf("fixture must be 1:1 for this comparison, got (%v,%v)", lay.scaleX, lay.scaleY)
	}
	buttons, ok := lay.rect(charSelectButtonsKey)
	if !ok {
		t.Fatal("char_buttons vanished")
	}
	if want := lay.area.X + 226; buttons.X != want {
		t.Errorf("char_buttons X = %d, want the design origin %d — a shift here desyncs the grid from the art",
			buttons.X, want)
	}
	if buttons.W != 518 {
		t.Errorf("char_buttons W = %d, want the design 518 (AO2 clips the paint, it does not resize)", buttons.W)
	}
	if buttons.X+buttons.W <= lay.area.X+lay.area.W {
		t.Error("char_buttons no longer overhangs the canvas — this test's premise is gone")
	}
	spec, ok := lay.rect(charSelectSpectatorKey)
	if !ok {
		t.Fatal("spectator vanished — the courtroom design space drops it, its own canvas must not")
	}
	if want := lay.area.Y + 640; spec.Y != want {
		t.Errorf("spectator Y = %d, want %d", spec.Y, want)
	}
}

// TestCharSelectHiddenKeysAreDropped pins AO2's 11037 convention on this canvas
// too: a theme parks an element off the design space to hide it.
func TestCharSelectHiddenKeysAreDropped(t *testing.T) {
	d := stockCharSelectDesign()
	d.r[charSelectListKey] = theme.Rect{X: 11037, Y: 36, W: 190, H: 596}
	d.r[charSelectSpectatorKey] = theme.Rect{X: 317, Y: 11037, W: 80, H: 23}
	a := stageCharSelect(t, d)
	lay := a.charSelectLayout(1152, 864)
	if _, ok := lay.rect(charSelectListKey); ok {
		t.Error("an X-hidden char_list survived")
	}
	if _, ok := lay.rect(charSelectSpectatorKey); ok {
		t.Error("a Y-hidden spectator survived")
	}
	if _, ok := lay.rect(charSelectButtonsKey); !ok {
		t.Error("hiding two keys took char_buttons with them")
	}
}

// TestCharSelectSpacingScalesWithTheCell pins that the gutter rides cellScale — the
// single uniform factor AO2 applies to button_width and char_button_spacing alike —
// rather than the per-axis scale, which would square-peg the grid in Stretch.
func TestCharSelectSpacingScalesWithTheCell(t *testing.T) {
	a := stageCharSelect(t, stockCharSelectDesign())
	a.d.Prefs.SetThemeFit(config.ThemeFitStretch) // the only mode where the axes disagree
	a.charSelLay.valid = false
	lay := a.charSelectLayout(1428, 668) // exactly 2x wide, 1x tall
	if lay.scaleX == lay.scaleY {
		t.Fatalf("fixture failed to make the axes disagree: (%v,%v)", lay.scaleX, lay.scaleY)
	}
	cs := lay.cellScale()
	if cs != lay.scaleY || cs > lay.scaleX {
		t.Errorf("cellScale = %v, want min(%v, %v)", cs, lay.scaleX, lay.scaleY)
	}
	if want := int32(7 * cs); lay.spacingX != want || lay.spacingY != want {
		t.Errorf("spacing = (%d,%d), want (%d,%d) — the uniform cell factor, not the per-axis scale",
			lay.spacingX, lay.spacingY, want, want)
	}
}

// TestCharSelectLayoutCacheKeys pins that every input the geometry depends on is IN
// the cache key. A missed key is not a slow path, it is a stale screen: the canvas
// keeps drawing at the old size until something unrelated happens to invalidate it.
func TestCharSelectLayoutCacheKeys(t *testing.T) {
	a := stageCharSelect(t, stockCharSelectDesign())
	base := *a.charSelectLayout(1152, 864)
	if !base.valid {
		t.Fatal("canvas did not build")
	}
	if got := a.charSelectLayout(1152, 864); got.area != base.area {
		t.Error("a repeat call at the same size rebuilt differently")
	}
	if got := a.charSelectLayout(1000, 864); got.area == base.area {
		t.Error("a window resize did not rebuild the canvas")
	}
	a.charSelectLayout(1152, 864)
	// The band: opening a tab docks the strip and steals 22 px of height.
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	if got := a.charSelectLayout(1152, 864); got.area == base.area {
		t.Error("the chrome band appearing did not rebuild the canvas")
	}
	a.tabs = nil
	a.charSelectLayout(1152, 864)
	a.d.Prefs.SetThemeFit(config.ThemeFitStretch)
	if got := a.charSelectLayout(1152, 864); got.area == base.area {
		t.Error("a fit-mode change did not rebuild the canvas")
	}
	// A theme swap invalidates by hand (pollThemeApply), because the design table is
	// not part of the key — it changes only there.
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	d := stockCharSelectDesign()
	d.r[charSelectCanvasKey] = theme.Rect{W: 944, H: 600}
	a.charSelDesign = d
	a.charSelLay.valid = false
	if got := a.charSelectLayout(1152, 864); got.area.W != 944 || got.area.H != 600 {
		t.Errorf("after a theme swap the canvas is %dx%d, want 944x600", got.area.W, got.area.H)
	}
}

// TestCharSelectCanvasFollowsCustomZoomAndPan is the regression test for the one input
// that moves this canvas from OUTSIDE its cache key.
//
// charSelectLayoutCache keys on (window, chrome band, fit mode, themed). ThemeZoom and
// ThemePan are in none of those — they are read inside fitDesignCanvas, and only under
// ThemeFitCustom — so the five sites that move them (the three Settings sliders and the
// fit preview's wheel-zoom and drag-pan) have to invalidate by hand. All five cleared
// the COURTROOM cache alone, so under Custom the courtroom re-fitted while char select
// kept the old canvas, the old widget rects and the old grid until an unrelated rebuild
// happened along: Settings → drag the pan → re-pick a character showed the two screens
// at different zooms. They go through invalidateThemeCanvases now, and the sweep below
// pins that nothing can clear one cache without the other again.
func TestCharSelectCanvasFollowsCustomZoomAndPan(t *testing.T) {
	const w, h int32 = 1152, 864
	a := stageCharSelect(t, stockCharSelectDesign())
	a.d.Prefs.SetThemeFit(config.ThemeFitCustom)
	a.d.Prefs.SetThemeFitZoom(100)
	a.d.Prefs.SetThemeFitPan(0, 0)
	a.invalidateThemeCanvases()
	base := *a.charSelectLayout(w, h)
	if !base.valid {
		t.Fatal("the Custom canvas did not build")
	}
	baseButtons, ok := base.rect(charSelectButtonsKey)
	if !ok {
		t.Fatal("char_buttons vanished on the Custom canvas")
	}

	// PREMISE: zoom/pan really are outside the key, which is why the invalidator has to
	// exist at all. If this ever stops holding, the test above it can be simplified —
	// but silently leaving it here would stop proving anything.
	a.d.Prefs.SetThemeFitPan(25, -15)
	if stale := a.charSelectLayout(w, h); stale.area != base.area {
		t.Fatal("zoom/pan are in the char-select cache key now — re-check invalidateThemeCanvases' rationale")
	}

	// The pan, invalidated the way every mutation site now does it. COPIED, not aliased:
	// charSelectLayout hands back a pointer INTO App and rebuilds in place, so two
	// "different" results would compare equal and this test would pass on a stale cache.
	a.invalidateThemeCanvases()
	panned := *a.charSelectLayout(w, h)
	if panned.area == base.area {
		t.Errorf("a pan left the canvas at %+v — char select is still drawing the old placement", panned.area)
	}
	// The widgets ride the canvas, so they must have moved by the same delta: a canvas
	// that moved while its rects did not would be a worse bug than a stale one.
	pannedButtons, ok := panned.rect(charSelectButtonsKey)
	if !ok {
		t.Fatal("char_buttons vanished after the pan")
	}
	if dx, dy := pannedButtons.X-baseButtons.X, pannedButtons.Y-baseButtons.Y; dx != panned.area.X-base.area.X || dy != panned.area.Y-base.area.Y {
		t.Errorf("char_buttons moved by (%d,%d) but the canvas moved by (%d,%d)",
			dx, dy, panned.area.X-base.area.X, panned.area.Y-base.area.Y)
	}

	// The zoom, same route.
	a.d.Prefs.SetThemeFitZoom(160)
	a.invalidateThemeCanvases()
	zoomed := *a.charSelectLayout(w, h)
	if zoomed.area.W <= panned.area.W || zoomed.area.H <= panned.area.H {
		t.Errorf("zooming to 160%% gave a %dx%d canvas, no bigger than the %dx%d one before it",
			zoomed.area.W, zoomed.area.H, panned.area.W, panned.area.H)
	}
	// And the grid follows, which is the half the user actually sees.
	g := a.charSelectGridPlan(w, h, a.topChromeH(), &zoomed)
	if g.themed && g.canvas != zoomed.area {
		t.Errorf("the grid is laid out on canvas %+v, not the current %+v", g.canvas, zoomed.area)
	}
}

// TestThemeCanvasInvalidationGoesThroughOneDoor is the structural half, and the reason
// the fix is a shared function rather than a second assignment at five sites: there were
// fifteen places that dropped the courtroom cache, and being right at fourteen of them is
// how char select ended up stale. Only invalidateThemeCanvases may touch either flag.
//
// It scans this package's own sources, which is the only way to catch a SIXTEENTH site
// added later — the thing the shape of this bug guarantees will happen.
func TestThemeCanvasInvalidationGoesThroughOneDoor(t *testing.T) {
	const owner = "theme_layout.go" // where invalidateThemeCanvases lives
	assigns := []string{"themeLay.valid = false", "charSelLay.valid = false"}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	scanned, found := 0, map[string]int{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, a := range assigns {
			n := strings.Count(string(src), a)
			found[a] += n
			if n > 0 && name != owner {
				t.Errorf("%s assigns %q directly — call a.invalidateThemeCanvases() instead, "+
					"or the other canvas goes stale (that is exactly how #20's screen kept an old zoom)", name, a)
			}
		}
	}
	if scanned < 10 {
		t.Fatalf("only scanned %d package sources — the sweep is not running where it thinks it is", scanned)
	}
	for _, a := range assigns {
		if found[a] != 1 {
			t.Errorf("%q appears %d times in the package, want exactly one (inside %s)", a, found[a], owner)
		}
	}
}

// TestCharSelectLayoutSteadyStateIsAllocFree pins the render-loop cost. The screen
// calls charSelectLayout every frame; a cache hit must be a handful of comparisons,
// never the map rebuild.
func TestCharSelectLayoutSteadyStateIsAllocFree(t *testing.T) {
	a := stageCharSelect(t, stockCharSelectDesign())
	const w, h int32 = 1152, 864
	if !a.charSelectLayout(w, h).valid {
		t.Fatal("canvas did not build")
	}
	if n := testing.AllocsPerRun(200, func() { a.charSelectLayout(w, h) }); n != 0 {
		t.Errorf("cached charSelectLayout allocates %v per call, want 0", n)
	}
	// The classic (no canvas) path runs every frame for themeless users and takes the
	// early return — it must not allocate either.
	b := testTabApp(t)
	if b.charSelectLayout(w, h).valid {
		t.Fatal("themeless fixture built a canvas")
	}
	if n := testing.AllocsPerRun(200, func() { b.charSelectLayout(w, h) }); n != 0 {
		t.Errorf("themeless charSelectLayout allocates %v per call, want 0", n)
	}
}

// TestCharSelectKeysStayOutOfTheCourtroomLayout is the blocking constraint from the
// spec, pinned so it cannot be undone by a well-meaning "unify the rect maps" pass.
// In a.themeRects these keys would become draggable ghost boxes in the courtroom
// layout editor AND be mangled by the courtroom design space — spectator (Y=640) is
// silently discarded under the stock courtroom = 0,0,714,579, and char_select's
// height would clamp 668 to 579.
func TestCharSelectKeysStayOutOfTheCourtroomLayout(t *testing.T) {
	for _, key := range themeLayoutKeys {
		if _, clash := charSelectDefaultRects[key]; clash {
			t.Errorf("%q is in BOTH themeLayoutKeys and the char-select table", key)
		}
		if key == charSelectSpacingKey {
			t.Errorf("%q is a char-select key and must not be a courtroom layout key", key)
		}
	}
	// And the courtroom layout genuinely does mangle them, which is why: build a
	// courtroom layout from the stock design and confirm spectator would be dropped.
	a := testTabApp(t)
	a.themeRects = map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: 714, H: 579},
		"viewport":  {X: 0, Y: 0, W: 256, H: 192},
		"spectator": charSelectDefaultRects[charSelectSpectatorKey],
	}
	if _, ok := a.themeLayout(1152, 864).rect(charSelectSpectatorKey); ok {
		t.Error("the courtroom design space kept spectator — the separation rationale changed, re-check it")
	}
}

// TestCharSelectSharesTheCourtroomFitMaths pins that the two canvases go through
// ONE implementation. Given the same design size, band and mode, char select's
// canvas must be the courtroom's canvas exactly — a private copy of the arithmetic
// is how a backdrop and a grid drift apart by a pixel.
func TestCharSelectSharesTheCourtroomFitMaths(t *testing.T) {
	const w, h int32 = 1301, 907 // deliberately odd: rounding differences show up here
	for _, mode := range []int{config.ThemeFitNative, config.ThemeFitLetterbox, config.ThemeFitStretch, config.ThemeFitCrop} {
		d := stockCharSelectDesign()
		d.r[charSelectCanvasKey] = theme.Rect{W: 944, H: 600} // same size as the courtroom below
		a := stageCharSelect(t, d)
		a.d.Prefs.SetThemeFit(mode)
		a.themeLay.valid, a.charSelLay.valid = false, false
		court := a.themeWindowLayout(w, h)
		if !court.valid {
			t.Fatalf("mode %d: courtroom layout did not build", mode)
		}
		cs := a.charSelectLayout(w, h)
		if !cs.valid {
			t.Fatalf("mode %d: char-select canvas did not build", mode)
		}
		if cs.area != court.area {
			t.Errorf("mode %d: char-select canvas %+v != courtroom canvas %+v for the same design size",
				mode, cs.area, court.area)
		}
		if cs.scaleX != court.scaleX || cs.scaleY != court.scaleY {
			t.Errorf("mode %d: scale (%v,%v) != courtroom (%v,%v)",
				mode, cs.scaleX, cs.scaleY, court.scaleX, court.scaleY)
		}
	}
}
