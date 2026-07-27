package ui

import (
	"math"
	"reflect"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// The character grid on AO2's arithmetic (#20). These tests pin the numbers that
// issue #20 is about — the column count, the pitch and the origin the cells land on
// — plus the three places our scrolling grid has to work harder than AO2's paged one
// to stay on the artwork's lattice.
//
// stageCharSelect / stockCharSelectDesign live in charselectlayout_test.go.

// charSelectGridTop is the header offset drawCharSelect passes the plan builder, for
// a window with no chrome band. Spelled out so a test that means "the header does not
// intrude" cannot accidentally depend on the constant changing.
func charSelectGridTop(a *App) int32 { return a.topChromeH() + pad + charSelectHeaderH }

// themedStockPlan stages the stock table at 1:1 in a window tall enough that the
// canvas centres clear of the client header, which is the case that must reproduce
// AO2 exactly.
func themedStockPlan(t *testing.T) (*App, *charSelectLayoutCache, charGridPlan) {
	t.Helper()
	const w, h int32 = 1920, 1080
	a := stageCharSelect(t, stockCharSelectDesign())
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	a.charSelLay.valid = false
	lay := a.charSelectLayout(w, h)
	if !lay.valid {
		t.Fatal("canvas did not build")
	}
	if lay.scaleX != 1 || lay.scaleY != 1 {
		t.Fatalf("fixture must be 1:1, got (%v,%v)", lay.scaleX, lay.scaleY)
	}
	return a, lay, a.charSelectGridPlan(w, h, charSelectGridTop(a), lay)
}

// TestCharGridReproducesAO2ColumnMaths is the core of issue #20: fed the stock table
// at 1:1, our grid must produce AO2's 7 columns at pitch 67 starting at x=226 — the
// exact origin and pitch of the black slot frames painted into the stock
// charselect_background.png. Anything else is the two-grids-two-origins screenshot.
func TestCharGridReproducesAO2ColumnMaths(t *testing.T) {
	_, lay, g := themedStockPlan(t)
	if !g.themed {
		t.Fatal("the stock table did not produce a themed grid")
	}
	if g.cell != charSelectButtonPx {
		t.Errorf("cell = %d, want AO2's button_width %d", g.cell, charSelectButtonPx)
	}
	const wantPitch, wantCols int32 = 67, 7
	if g.pitchX != wantPitch || g.pitchY != wantPitch {
		t.Errorf("pitch = (%d,%d), want the art's (%d,%d)", g.pitchX, g.pitchY, wantPitch, wantPitch)
	}
	if g.cols != wantCols {
		t.Errorf("columns = %d, want AO2's %d", g.cols, wantCols)
	}
	// put_button_in_place: column c starts at char_buttons.X + (size+spacing)*c.
	first := g.cellAt(0, 0)
	if want := lay.area.X + 226; first.X != want {
		t.Errorf("column 0 at x=%d, want the design origin %d", first.X, want)
	}
	if want := lay.area.Y + 36; first.Y != want {
		t.Errorf("row 0 at y=%d, want the design origin %d", first.Y, want)
	}
	last := g.cellAt(wantCols-1, 0)
	if want := lay.area.X + 628; last.X != want {
		t.Errorf("column 6 at x=%d, want the art's %d", last.X, want)
	}
	if last.W != charSelectButtonPx || last.H != charSelectButtonPx {
		t.Errorf("cell size = %dx%d, want 60x60", last.W, last.H)
	}
	// Wrapping: cell 7 is row 1, column 0.
	wrap := g.cellAt(wantCols, 0)
	if wrap.X != first.X || wrap.Y != first.Y+wantPitch {
		t.Errorf("cell %d = (%d,%d), want the next row at (%d,%d)",
			wantCols, wrap.X, wrap.Y, first.X, first.Y+wantPitch)
	}
}

// charGridDriftWindows is the scale sweep every cell placement is measured against.
//
// The first six are the sizes an adversarial review computed by hand: 944x644 is not
// hypothetical — it is exactly what the theme-import auto-snap produces for
// AceAttorney2x (a 944x600 courtroom plus the 44 px chrome inset), i.e. issue #20's
// own theme in the shipped default configuration with no setting touched. 800x600,
// 1024x600 and 640x480 are the low end of the real window range; 1152x864 and
// 1920x1080 are the 1:1 cases that were already correct and must stay correct. The
// rest are common desktop sizes and one window sized to the canvas itself.
var charGridDriftWindows = []struct{ w, h int32 }{
	{944, 644}, {800, 600}, {1024, 600}, {640, 480}, {1152, 864}, {1920, 1080},
	{1280, 720}, {1280, 800}, {1366, 768}, {1600, 900}, {1024, 768}, {714, 712},
}

// artPos is where the BACKDROP puts design coordinate `design`: the art is one
// texture blitted into the canvas rect, so SDL stretches it at exactly
// canvasExtent/designExtent. This is the ground truth every cell is measured against.
func artPos(canvasOrigin, canvasExtent, design, designExtent int32) float64 {
	return float64(canvasOrigin) + float64(design)*float64(canvasExtent)/float64(designExtent)
}

// TestCharGridRidesTheCanvasTransformAtEveryScale is issue #20's real acceptance test,
// and the one the first version of this screen could not pass.
//
// The grid's cells and the slot frames painted behind them are produced by two
// different pieces of arithmetic: the frames come from SDL stretching a 714x668
// backdrop into the canvas rect at the fractional ratio area.W/714, the cells from our
// own layout. They agreed only where that ratio was exactly 1. Measured against the
// stock table, cells derived from an accumulated integer pitch drifted 7.79 px across
// and 9.77 px down on a 53 px cell at 944x644 — 15% and 18% of a cell, on issue #20's
// own theme at its own default window size — and 12.19 / 15.10 px at 800x600.
//
// Every column of every row must now land within one pixel of its frame, at every
// size. The check is deliberately over the WHOLE grid, not just the origin: the old
// arithmetic was exact at column 0 and wrong by a sixth of a cell at column 6, so an
// origin-only assertion proves nothing.
func TestCharGridRidesTheCanvasTransformAtEveryScale(t *testing.T) {
	const (
		designW, designH   = 714, 668 // the stock char_select canvas
		designBX, designBY = 226, 36  // char_buttons' origin
		designPitch        = 67       // button_width 60 + char_button_spacing 7
		artCols, artRows   = 7, 9     // the slot frames the stock backdrop actually paints
	)
	for _, win := range charGridDriftWindows {
		a := stageCharSelect(t, stockCharSelectDesign())
		// A real chrome band: one open tab docks the server-tab strip under the menu
		// bar, which is the shipped configuration on this screen.
		a.tabs = []*courtTab{{}}
		a.activeTab = 0
		a.d.Prefs.SetThemeFit(config.ThemeFitNative)
		a.charSelLay.valid = false
		band := a.topChromeH()
		lay := a.charSelectLayout(win.w, win.h)
		if !lay.valid {
			t.Fatalf("%dx%d: canvas did not build", win.w, win.h)
		}
		g := a.charSelectGridPlan(win.w, win.h, band, lay)
		if !g.themed {
			t.Fatalf("%dx%d (band %d): the stock table did not produce a themed grid", win.w, win.h, band)
		}
		if g.cols != artCols {
			t.Errorf("%dx%d: %d columns, but the stock art paints %d slot frames per row — "+
				"the extra column lands on blank panel", win.w, win.h, g.cols, artCols)
		}
		// The LEGACY placement, rebuilt from this very fixture so the regression this
		// test prevents is measured rather than remembered: cell (0,0) at the rect the
		// canvas SCALE produces, then an accumulated integer pitch (int32(60*cs) +
		// int32(7*cs)) multiplied by the column index. Its column count came from the
		// same three truncated scaled integers.
		legacyX0 := lay.area.X + int32(float64(designBX)*lay.scaleX)
		legacyY0 := lay.area.Y + int32(float64(designBY)*lay.scaleY)
		legacyCols := ((int32(518*lay.scaleX) - g.cell) / g.pitchX) + 1
		worstX, worstY, oldX, oldY := 0.0, 0.0, 0.0, 0.0
		for row := int32(0); row < artRows; row++ {
			for col := int32(0); col < g.cols; col++ {
				cell := g.cellAt(row*g.cols+col, 0)
				worstX = math.Max(worstX, math.Abs(float64(cell.X)-
					artPos(lay.area.X, lay.area.W, designBX+col*designPitch, designW)))
				worstY = math.Max(worstY, math.Abs(float64(cell.Y)-
					artPos(lay.area.Y, lay.area.H, designBY+row*designPitch, designH)))
			}
		}
		// Measured over the LEGACY column count, which is the honest comparison: the
		// extra column it invented is where its drift was worst.
		for col := int32(0); col < legacyCols; col++ {
			oldX = math.Max(oldX, math.Abs(float64(legacyX0+col*g.pitchX)-
				artPos(lay.area.X, lay.area.W, designBX+col*designPitch, designW)))
		}
		for row := int32(0); row < artRows; row++ {
			oldY = math.Max(oldY, math.Abs(float64(legacyY0+row*g.pitchY)-
				artPos(lay.area.Y, lay.area.H, designBY+row*designPitch, designH)))
		}
		t.Logf("%4dx%-4d band=%d cell=%2d  drift X/Y: was %5.2f/%5.2f px, now %4.2f/%4.2f px   cols: was %d, now %d (art %d)",
			win.w, win.h, band, g.cell, oldX, oldY, worstX, worstY, legacyCols, g.cols, artCols)
		if worstX > 1 {
			t.Errorf("%dx%d: a cell sits %.2f px off the slot frame painted for it (X)", win.w, win.h, worstX)
		}
		if worstY > 1 {
			t.Errorf("%dx%d: a cell sits %.2f px off the slot frame painted for it (Y)", win.w, win.h, worstY)
		}
	}
}

// TestCharGridColumnCountIsScaleInvariant is the other half of the same defect. AO2
// cannot hit it: Options::themeScalingFactor() is an INTEGER, so its char_buttons rect
// and its button width scale by the same factor and ((W-cell)/(spacing+cell))+1 is
// invariant. Ours is a fraction, and evaluating that divide on three independently
// truncated scaled integers claimed EIGHT columns at 800x600 and 1024x600 against the
// stock art's seven — a whole column of characters drawn on blank panel, which is the
// class of defect #20 reported.
func TestCharGridColumnCountIsScaleInvariant(t *testing.T) {
	const artCols int32 = 7
	// Include the band-22 case (menu bar only, no open tab): 944x644 yields eight
	// columns there too, on a different scale from the band-44 one.
	for _, band := range []bool{false, true} {
		for _, win := range charGridDriftWindows {
			a := stageCharSelect(t, stockCharSelectDesign())
			if band {
				a.tabs = []*courtTab{{}}
				a.activeTab = 0
			}
			a.d.Prefs.SetThemeFit(config.ThemeFitNative)
			a.charSelLay.valid = false
			top := a.topChromeH()
			lay := a.charSelectLayout(win.w, win.h)
			g := a.charSelectGridPlan(win.w, win.h, top, lay)
			if !g.themed {
				continue
			}
			if g.cols != artCols {
				t.Errorf("%dx%d band=%d: %d columns, want the art's %d", win.w, win.h, top, g.cols, artCols)
			}
		}
	}
}

// TestCharGridViewportHoldsAO2sRowCount is the vertical half. AO2 computes 9 rows for
// the stock table; the scrolling viewport must therefore be tall enough for exactly 9
// and no more, or the grid is showing a different page shape from the art behind it.
func TestCharGridViewportHoldsAO2sRowCount(t *testing.T) {
	_, _, g := themedStockPlan(t)
	const wantRows int32 = 9
	fits := func(rows int32) bool { return (rows-1)*g.pitchY+g.cell <= g.view.H }
	if !fits(wantRows) {
		t.Errorf("viewport %d px holds fewer than AO2's %d rows (pitch %d, cell %d)",
			g.view.H, wantRows, g.pitchY, g.cell)
	}
	if fits(wantRows + 1) {
		t.Errorf("viewport %d px holds MORE than AO2's %d rows — the grid outgrew char_buttons",
			g.view.H, wantRows)
	}
}

// TestCharGridOverhangIsClippedNotShifted is the geometry rule from the layout stage,
// now proven where it matters: the stock char_buttons is 518 wide from x=226 on a
// 714-wide canvas, so it overhangs by 30 px. AO2 relies on Qt clipping the paint and
// leaving the origin alone. Our columns therefore keep the design origin and the
// VIEWPORT stops at the canvas edge.
func TestCharGridOverhangIsClippedNotShifted(t *testing.T) {
	_, lay, g := themedStockPlan(t)
	if want := lay.area.X + 226; g.originX != want {
		t.Errorf("grid origin x=%d, want the un-shifted design origin %d", g.originX, want)
	}
	buttons, ok := lay.rect(charSelectButtonsKey)
	if !ok {
		t.Fatal("char_buttons vanished")
	}
	if buttons.X+buttons.W <= lay.area.X+lay.area.W {
		t.Fatal("char_buttons no longer overhangs the canvas — this test's premise is gone")
	}
	if right := g.view.X + g.view.W; right != lay.area.X+lay.area.W {
		t.Errorf("viewport right edge %d, want the canvas edge %d", right, lay.area.X+lay.area.W)
	}
	// The last column still starts inside the canvas — clipping the paint must not
	// have cost us the seventh column AO2 draws.
	if last := g.cellAt(g.cols-1, 0); last.X+last.W > lay.area.X+lay.area.W {
		t.Errorf("column %d runs past the canvas edge at x=%d", g.cols-1, last.X+last.W)
	}
}

// TestCharGridClassicPlanIsThePreThemeScreen pins that a themeless install is
// untouched. The values on the right are the literal expressions drawCharSelect used
// before #20 — if any of them drift, every existing screenshot of this screen is wrong.
func TestCharGridClassicPlanIsThePreThemeScreen(t *testing.T) {
	const w, h int32 = 1152, 864
	a := testTabApp(t)
	lay := a.charSelectLayout(w, h)
	if lay.valid {
		t.Fatal("themeless fixture built a canvas")
	}
	gridTop := charSelectGridTop(a)
	g := a.charSelectGridPlan(w, h, gridTop, lay)
	if g.themed {
		t.Fatal("no canvas, but the plan claims the theme grid")
	}
	if !g.caption {
		t.Error("the classic cell lost its name caption")
	}
	wantCols := (w - 2*pad - scrollBarW - scrollBarGap) / (iconCell + iconGap)
	if g.cols != wantCols {
		t.Errorf("cols = %d, want %d", g.cols, wantCols)
	}
	if g.cell != iconCell || g.pitchX != iconCell+iconGap || g.pitchY != iconCell+iconGap+charCellCaptionH {
		t.Errorf("cell/pitch = %d/(%d,%d), want %d/(%d,%d)",
			g.cell, g.pitchX, g.pitchY, iconCell, iconCell+iconGap, iconCell+iconGap+charCellCaptionH)
	}
	if g.originX != pad || g.originY != gridTop {
		t.Errorf("origin = (%d,%d), want (%d,%d)", g.originX, g.originY, pad, gridTop)
	}
	if want := (sdl.Rect{X: 0, Y: gridTop, W: w, H: h - gridTop - pad}); g.view != want {
		t.Errorf("viewport = %+v, want %+v", g.view, want)
	}
	if want := w - pad - scrollBarW; g.trackX != want {
		t.Errorf("scrollbar lane x=%d, want %d", g.trackX, want)
	}
	// And the placement the old loop did by hand.
	if got, want := g.cellAt(1, 0), (sdl.Rect{X: pad + (iconCell + iconGap), Y: gridTop, W: iconCell, H: iconCell}); got != want {
		t.Errorf("cell 1 = %+v, want %+v", got, want)
	}
	// A window too narrow for one column still yields one, never zero (the old
	// `if cols < 1 { cols = 1 }`).
	if narrow := a.charSelectGridPlan(40, h, gridTop, lay); narrow.cols != 1 {
		t.Errorf("narrow window gave %d columns, want 1", narrow.cols)
	}
}

// TestCharGridSurvivesHostileSpacing is the guard AO2 does not have. Its char-select
// divide has no qMax (unlike evidence.cpp:211), so char_button_spacing = -60 divides
// by zero inside stock AO2 — and would PANIC here. The pitch floor must hold, and the
// grid must still be walkable.
func TestCharGridSurvivesHostileSpacing(t *testing.T) {
	for _, sp := range [][2]int{{-60, -60}, {-1000, -1000}, {0, 0}} {
		d := stockCharSelectDesign()
		d.spacing = sp
		a := stageCharSelect(t, d)
		a.d.Prefs.SetThemeFit(config.ThemeFitNative)
		a.charSelLay.valid = false
		lay := a.charSelectLayout(1920, 1080)
		g := a.charSelectGridPlan(1920, 1080, charSelectGridTop(a), lay) // must not panic
		if g.pitchX < charGridMinPitchPx || g.pitchY < charGridMinPitchPx {
			t.Errorf("spacing %v: pitch (%d,%d) fell through the floor", sp, g.pitchX, g.pitchY)
		}
		if g.cols < 1 {
			t.Errorf("spacing %v: %d columns", sp, g.cols)
		}
		// Flush buttons are a legitimate declaration (twelve corpus themes ship
		// emote_button_spacing = 0, 0), and they must produce a real flush grid.
		if sp == [2]int{0, 0} && g.themed {
			if g.pitchX != g.cell || g.pitchY != g.cell {
				t.Errorf("spacing 0,0: pitch (%d,%d), want the flush cell %d", g.pitchX, g.pitchY, g.cell)
			}
		}
	}
}

// TestCharGridStartsOnAWholeLatticeRow covers the one place AsyncAO's own furniture
// meets the theme's: the client header row (search, tabs, Spectate) is drawn before
// the grid, so in a short window it overlaps char_buttons. The grid must not start
// mid-cell there — it advances to the first WHOLE row of char_buttons' own lattice,
// so every row that IS drawn still lands on the slot frame behind it.
//
// "A WHOLE ROW" IS MEASURED IN DESIGN PIXELS, and it has to be: the artwork's row
// pitch on screen is 67 × area.H/668, a fraction, so consecutive frames are NOT a
// constant number of device pixels apart and no device-pixel divisibility test can
// express membership of that lattice. (An earlier version of this test asked for
// `(originY - char_buttons.Y) % pitchY == 0` against the rounded device pitch, which
// only held while the two happened to coincide — at 1280x620 the rounded pitch is 59
// px and a real row is 59.98.) The design-space check below is the strictly stronger
// statement, and cellAt is pinned to agree with it.
func TestCharGridStartsOnAWholeLatticeRow(t *testing.T) {
	for _, h := range []int32{620, 700, 720, 800, 900, 1080} {
		const w int32 = 1280
		a := stageCharSelect(t, stockCharSelectDesign())
		a.d.Prefs.SetThemeFit(config.ThemeFitNative)
		a.charSelLay.valid = false
		lay := a.charSelectLayout(w, h)
		gridTop := charSelectGridTop(a)
		g := a.charSelectGridPlan(w, h, gridTop, lay)
		if !g.themed {
			continue // too cramped for a themed grid at all: the classic fallback
		}
		if _, ok := lay.rect(charSelectButtonsKey); !ok {
			t.Fatalf("h=%d: char_buttons vanished", h)
		}
		if g.originY < gridTop {
			t.Errorf("h=%d: grid starts at y=%d, under the header row ending at %d", h, g.originY, gridTop)
		}
		if off := g.designY - lay.buttonsDesign.Y; off < 0 || off%g.designPitchY != 0 {
			t.Errorf("h=%d: grid origin is %d design px below char_buttons, not a whole %d px row",
				h, off, g.designPitchY)
		}
		// ...and the origin really is where cell (0,0) is drawn, mapped through the
		// canvas exactly like every other cell.
		if first := g.cellAt(0, 0); first.X != g.originX || first.Y != g.originY {
			t.Errorf("h=%d: cell (0,0) at (%d,%d) but the plan's origin is (%d,%d)",
				h, first.X, first.Y, g.originX, g.originY)
		}
		if g.view.Y != g.originY {
			t.Errorf("h=%d: viewport starts at %d but row 0 at %d — a partial row would show",
				h, g.view.Y, g.originY)
		}
	}
}

// TestCharGridScrolledCellsStayOnTheArtworkLattice is the scrolled half of the drift
// proof, at a scale where the lattice is genuinely fractional (944x644 puts the stock
// canvas at ~0.898, so a row is 59.98 device px and no integer pitch can express it).
//
// The backdrop does NOT scroll — its slot frames are static — so scrolling by m rows
// has to put grid row r exactly where frame row r-m is painted. That is what the
// row-snapped offset is FOR, and it is why the offset is taken from the lattice
// (rowOffset) rather than from a multiple of the rounded device pitch, which would
// accumulate its rounding error one row at a time.
func TestCharGridScrolledCellsStayOnTheArtworkLattice(t *testing.T) {
	const (
		w, h               int32 = 944, 644 // the import auto-snap size for AceAttorney2x
		designW, designH         = 714, 668
		designBX, designBY       = 226, 36
		designPitch              = 67
		artRows                  = 9
	)
	a := stageCharSelect(t, stockCharSelectDesign())
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	a.charSelLay.valid = false
	lay := a.charSelectLayout(w, h)
	g := a.charSelectGridPlan(w, h, a.topChromeH(), lay)
	if !g.themed {
		t.Fatal("fixture did not produce a themed grid")
	}
	if g.pitchY == int32(float64(designPitch)*lay.cellScale()) && lay.cellScale() == 1 {
		t.Fatal("fixture is 1:1 — a fractional lattice is the whole point here")
	}
	worst := 0.0
	for m := int32(0); m < 40; m++ {
		scroll := g.rowOffset(m)
		// The snap must be a fixed point: an offset already on the lattice may not be
		// nudged off it by the next frame's bookkeeping.
		if got := g.snapScroll(scroll, 1<<20); got != scroll {
			t.Errorf("row %d's offset %d was re-snapped to %d", m, scroll, got)
		}
		for r := m; r < m+artRows; r++ {
			cell := g.cellAt(r*g.cols, scroll)
			// Where the artwork paints the frame this cell has scrolled onto.
			art := artPos(lay.area.Y, lay.area.H, designBY+(r-m)*designPitch, designH)
			if d := math.Abs(float64(cell.Y) - art); d > worst {
				worst = d
			}
		}
	}
	t.Logf("scrolled lattice at %dx%d (cell %d, row pitch %.2f px): worst drift %.2f px",
		w, h, g.cell, float64(designPitch)*float64(lay.area.H)/float64(designH), worst)
	// Two truncations of the canvas mapping meet in a scrolled cell (the row's own and
	// the scroll offset's), so the bound is one pixel looser than the resting one — and
	// it is a CONSTANT, not something that grows with how far you have scrolled, which
	// is the property a rounded device pitch could not deliver.
	if worst > 2 {
		t.Errorf("a scrolled cell sits %.2f px off the slot frame it landed on", worst)
	}
}

// TestCharGridScrollSnapsToRows is why scrolling is safe to keep. AO2 pages, so its
// buttons are always on the artwork's lattice; we scroll, and a pixel offset would
// walk every cell off the slot frames the moment the wheel moved — issue #20's
// symptom, back again for anyone who scrolls. Snapping the offset to whole rows keeps
// the lattice exact at every scroll position.
func TestCharGridScrollSnapsToRows(t *testing.T) {
	const pitch, viewH, contentH int32 = 67, 596, 2010 // 30 rows of content in 9 rows of viewport
	maxScroll := contentH - viewH
	for _, raw := range []int32{-500, -1, 0, 1, 33, 34, 66, 67, 100, 500, 1400, 1414, 99999} {
		got := snapScrollToRows(raw, contentH, viewH, pitch)
		if got%pitch != 0 {
			t.Errorf("scroll %d snapped to %d, which is not a whole %d px row", raw, got, pitch)
		}
		if got < 0 {
			t.Errorf("scroll %d snapped to a negative %d", raw, got)
		}
		// The last row must be reachable in full, so the clamp rounds the maximum UP
		// rather than leaving it permanently half-clipped.
		if got > maxScroll+pitch {
			t.Errorf("scroll %d snapped to %d, past the content by more than one row", raw, got)
		}
	}
	if got := snapScrollToRows(99999, contentH, viewH, pitch); got < maxScroll {
		t.Errorf("the bottom of the list is unreachable: max snap %d < %d", got, maxScroll)
	}
	// Idempotent: snapping an already-snapped value must not creep.
	for _, s := range []int32{0, pitch, 5 * pitch} {
		if got := snapScrollToRows(s, contentH, viewH, pitch); got != s {
			t.Errorf("re-snapping %d moved it to %d", s, got)
		}
	}
	// Content that fits needs no scroll at all.
	if got := snapScrollToRows(500, 300, viewH, pitch); got != 0 {
		t.Errorf("content shorter than the viewport scrolled to %d, want 0", got)
	}
	// A degenerate pitch is returned untouched rather than dividing by zero.
	if got := snapScrollToRows(42, contentH, viewH, 0); got != 42 {
		t.Errorf("pitch 0 returned %d, want the input unchanged", got)
	}
}

// TestCharGridCellScalesWithTheCanvas pins B2's trade-off in geometry terms: the
// icon TEXTURE stays baked at 64 px, but the CELL is the theme's, so a downscaled
// canvas must shrink the cell (SDL scales the texture on copy — which is what AO2
// does too, stretching char_icon into the 60x60 button with border-image).
func TestCharGridCellScalesWithTheCanvas(t *testing.T) {
	// A canvas half the design size: 714x668 into 357 px of width.
	const w, h int32 = 357, 1080
	a := stageCharSelect(t, stockCharSelectDesign())
	a.d.Prefs.SetThemeFit(config.ThemeFitLetterbox)
	a.charSelLay.valid = false
	lay := a.charSelectLayout(w, h)
	if !lay.valid {
		t.Fatal("canvas did not build")
	}
	cs := lay.cellScale()
	if cs >= 1 {
		t.Fatalf("fixture failed to downscale: cellScale = %v", cs)
	}
	g := a.charSelectGridPlan(w, h, charSelectGridTop(a), lay)
	if !g.themed {
		t.Skip("canvas too small for a themed grid at this window size")
	}
	if want := int32(float64(charSelectButtonPx) * cs); g.cell != want {
		t.Errorf("cell = %d, want button_width scaled to %d", g.cell, want)
	}
	if want := g.cell + lay.spacingX; g.pitchX != want {
		t.Errorf("pitch x = %d, want the scaled cell + scaled gutter %d", g.pitchX, want)
	}
}

// TestCharGridFallsBackWhenTheThemeHasNoUsableGrid walks the three ways the themed
// path bails. Each one must land on the classic full-window grid — a broken themed
// grid is worse than no themed grid.
func TestCharGridFallsBackWhenTheThemeHasNoUsableGrid(t *testing.T) {
	const w, h int32 = 1920, 1080
	// 1. The theme hides char_buttons off the design space (the 11037 convention).
	hidden := stockCharSelectDesign()
	hidden.r[charSelectButtonsKey] = theme.Rect{X: 11037, Y: 36, W: 518, H: 596}
	// 2. A canvas so large the cell scales below charCellMinPx.
	tiny := stockCharSelectDesign()
	tiny.r[charSelectCanvasKey] = theme.Rect{W: 100000, H: 100000}
	tiny.r[charSelectButtonsKey] = theme.Rect{X: 0, Y: 0, W: 100000, H: 100000}
	// 3. char_buttons entirely above the client header row: no whole row survives.
	high := stockCharSelectDesign()
	high.r[charSelectButtonsKey] = theme.Rect{X: 226, Y: 0, W: 518, H: 60}
	for name, d := range map[string]charSelectDesign{"hidden": hidden, "tiny-cell": tiny, "under-header": high} {
		a := stageCharSelect(t, d)
		a.d.Prefs.SetThemeFit(config.ThemeFitLetterbox)
		a.charSelLay.valid = false
		lay := a.charSelectLayout(w, h)
		g := a.charSelectGridPlan(w, h, charSelectGridTop(a), lay)
		if g.themed {
			t.Errorf("%s: expected the classic fallback, got a themed grid %+v", name, g)
		}
		if g.cell != iconCell || !g.caption {
			t.Errorf("%s: fallback is not the classic plan (cell %d, caption %v)", name, g.cell, g.caption)
		}
	}
}

// TestCharSelectorHaloIsAO2Geometry pins the hover halo against AOCharButton: a 62x62
// selector over a 60x60 button, offset by one pixel on both axes.
func TestCharSelectorHaloIsAO2Geometry(t *testing.T) {
	cell := sdl.Rect{X: 226, Y: 36, W: charSelectButtonPx, H: charSelectButtonPx}
	got := charSelectorRect(cell, charSelectorHaloDesignPx)
	want := sdl.Rect{X: 225, Y: 35, W: 62, H: 62}
	if got != want {
		t.Errorf("selector = %+v, want AO2's %+v", got, want)
	}
	// And the plan never rounds the halo away, however small the canvas.
	_, _, g := themedStockPlan(t)
	if g.haloPx < 1 {
		t.Errorf("halo = %d px, want at least 1", g.haloPx)
	}
}

// TestCharSelectTabsShareOneGridPlan is B4's lockstep, pinned structurally. The
// Characters grid and the Wardrobe grid are the SAME screen and shared iconCell /
// iconGap before the theme canvas existed; if one becomes theme-driven and the other
// keeps its own metrics they desync on the first resize. Both therefore take the plan
// rather than deriving anything.
func TestCharSelectTabsShareOneGridPlan(t *testing.T) {
	plan := reflect.TypeOf(charGridPlan{})
	for name, fn := range map[string]any{
		"drawWardrobeGrid": (*App).drawWardrobeGrid,
		"drawCharCell":     (*App).drawCharCell,
	} {
		ft := reflect.TypeOf(fn)
		found := false
		for i := 0; i < ft.NumIn(); i++ {
			if ft.In(i) == plan {
				found = true
			}
		}
		if !found {
			t.Errorf("%s no longer takes the shared charGridPlan — the two tabs can now desync", name)
		}
	}
}

// cellChromeHarness stages a real Ctx and TextureStore so ONE grid cell can be drawn
// and read back. No texture is ever uploaded: every cell takes the icon-miss branch and
// draws its initials placeholder, which is identical across the variants below, so the
// only thing that can move a pixel is the corner chrome under test.
func cellChromeHarness(t *testing.T) (*App, *sdl.Renderer, func()) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	ctx, err := NewCtx(ren)
	if err != nil {
		cleanup()
		t.Skipf("Ctx unavailable: %v", err)
	}
	store, err := render.NewTextureStore(ren)
	if err != nil {
		cleanup()
		t.Skipf("texture store unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx = ctx
	a.uiScalePct = 100
	a.d.Store = store
	// The pointer parked far away: hover chrome (the "+key" badge, every tooltip) must
	// not be what makes two renders differ.
	ctx.mouseX, ctx.mouseY = -1000, -1000
	a.sess = &courtroom.Session{Chars: []courtroom.CharacterSlot{{Name: "Phoenix"}}}
	a.ensureCharLower()
	a.wardrobeMembers = map[string]bool{}
	a.iniList = []string{"Phoenix"}
	a.iniLower = []string{"phoenix"}
	a.iniFolders = []string{""}
	a.iniWardrobe = []bool{false}
	a.iniHoverIDs = []string{iniHoverIDPrefix + "Phoenix"}
	// The two theme-texture maps the real constructor always creates together
	// (themePage writes into themePages on the first resident lookup).
	a.themeTex = map[string]bool{}
	a.themePages = map[string]*render.TexturePage{}
	return a, ren, func() {
		store.Purge()
		cleanup()
	}
}

// TestSmallCellsSuppressCornerChromeOnBothTabs is the shared-plan lockstep applied to
// what the cells PAINT, not just to the metrics they paint on.
//
// Since #20 the Characters grid and the Wardrobe grid are one charGridPlan, so a cell on
// either tab can be as small as charCellMinPx (20 px). Their fixed-size corner chrome did
// not follow: drawCharCell gated its 18 px ★ at 48 px but drew its 20x18 download badge
// at any size, and drawIniswapCell drew a 17 px ★, a 15 px folder tag and a 16 px key
// badge unconditionally. Measured at the 640x480 window floor with the stock table (a
// 39 px cell — see the sibling test below), that put a badge over the bottom-right
// quadrant of a Characters cell and roughly three-quarters of a Wardrobe cell under
// chrome — each piece with its own hover+click, so a click aimed at the character
// downloaded it, favourited it or armed a keybind instead.
//
// Every piece now goes through cellFitsCornerChrome. The proof is per piece and by
// PIXELS: render the cell twice with only that piece's own input flipped. If the piece is
// drawn the two renders differ; if it is suppressed they are byte-identical. Both tabs
// are measured in one test, at one threshold, so they cannot diverge again.
func TestSmallCellsSuppressCornerChromeOnBothTabs(t *testing.T) {
	a, ren, cleanup := cellChromeHarness(t)
	defer cleanup()

	// A bare plan: not themed (so no theme overlay art is probed) and no caption (so
	// nothing paints outside the cell). The gate reads the CELL, never the plan.
	plan := charGridPlan{}
	shot := func(cell sdl.Rect, draw func()) []byte {
		a.ctx.Fill(sdl.Rect{X: 0, Y: 0, W: 200, H: 120}, ColBackground)
		draw()
		return readRegion(t, ren, cell)
	}
	pieces := []struct {
		name string
		// shot renders the cell with this piece's own input on or off.
		shot func(cell sdl.Rect, on bool) []byte
	}{
		{"Characters ★", func(cell sdl.Rect, on bool) []byte {
			a.wardrobeMembers = map[string]bool{"phoenix": on}
			return shot(cell, func() { a.drawCharCell(&a.sess.Chars[0], cell, 0, false, plan) })
		}},
		{"Characters download badge", func(cell sdl.Rect, on bool) []byte {
			a.wardrobeMembers = map[string]bool{}
			return shot(cell, func() { a.drawCharCell(&a.sess.Chars[0], cell, 0, on, plan) })
		}},
		{"Wardrobe ★", func(cell sdl.Rect, on bool) []byte {
			a.iniWardrobe = []bool{on}
			return shot(cell, func() { a.drawIniswapCell(0, cell, cellClickChar, false) })
		}},
		{"Wardrobe folder tag", func(cell sdl.Rect, on bool) []byte {
			a.iniFolders = []string{""}
			if on {
				a.iniFolders = []string{"Prosecutors"}
			}
			return shot(cell, func() { a.drawIniswapCell(0, cell, cellClickChar, false) })
		}},
		{"Wardrobe key badge", func(cell sdl.Rect, on bool) []byte {
			a.charKeysRev = nil
			if on {
				a.charKeysRev = map[string]string{"Phoenix": "q"}
			}
			return shot(cell, func() { a.drawIniswapCell(0, cell, cellClickChar, false) })
		}},
	}
	for _, p := range pieces {
		for _, size := range []struct {
			name string
			edge int32
			want bool // want the piece drawn
		}{
			{"below the floor", charCellChromeMinPx - 1, false},
			{"at the floor", charCellChromeMinPx, true},
		} {
			cell := sdl.Rect{X: 10, Y: 10, W: size.edge, H: size.edge}
			p.shot(cell, false) // warm the label cache so the first raster is not the difference
			off := p.shot(cell, false)
			on := p.shot(cell, true)
			if drawn := !sameRegion(off, on); drawn != size.want {
				if size.want {
					t.Errorf("%s: a %d px cell no longer paints it — the floor moved and the two tabs "+
						"lose an affordance they should still have", p.name, size.edge)
				} else {
					t.Errorf("%s: still painted on a %d px cell, below the %d px floor — it covers the "+
						"character art and eats the click aimed at it", p.name, size.edge, charCellChromeMinPx)
				}
			}
		}
	}
}

// TestStockTableAtTheWindowFloorSuppressesCornerChrome is the measured configuration the
// gate above exists for: the shipped default theme fit, the stock 714x668 char-select
// table and the shipped chrome band, in the smallest window AsyncAO allows. The themed
// grid survives there (it is well above charCellMinPx), and that is exactly why the
// chrome had to be gated separately — a perfectly usable 39 px cell is far too small to
// carry four badges built for a 60 px one.
func TestStockTableAtTheWindowFloorSuppressesCornerChrome(t *testing.T) {
	const w, h = config.MinWindowW, config.MinWindowH
	a := stageCharSelect(t, stockCharSelectDesign())
	a.tabs = []*courtTab{{}} // menu bar + docked server-tab strip: the shipped band
	a.activeTab = 0
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	a.invalidateThemeCanvases()
	lay := a.charSelectLayout(w, h)
	g := a.charSelectGridPlan(w, h, a.topChromeH(), lay)
	if !g.themed {
		t.Fatalf("the stock table lost its themed grid at %dx%d — this test's premise is gone", w, h)
	}
	cell := g.cellAt(0, 0)
	if cell.W < charCellMinPx {
		t.Fatalf("cell %d px is below the themed-grid floor %d", cell.W, charCellMinPx)
	}
	if cellFitsCornerChrome(cell) {
		t.Errorf("the %d px cell at the %dx%d floor still carries fixed-size corner chrome "+
			"(floor %d px)", cell.W, w, h, charCellChromeMinPx)
	}
	t.Logf("stock table at %dx%d (band %d): cell %d px, corner chrome %v",
		w, h, a.topChromeH(), cell.W, cellFitsCornerChrome(cell))
}

// TestCharHoverIDsAreCachedNotRebuilt pins the per-cell hover id. It is the id
// preview_test.go's ScreenCharSelect coverage depends on ("char:"+name), and it used
// to be a runtime concat inside the draw loop — one allocation per VISIBLE cell per
// frame. The cache must produce the identical string, stay in lockstep with
// charLower, and survive an out-of-range index without panicking a draw.
func TestCharHoverIDsAreCachedNotRebuilt(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{Chars: []courtroom.CharacterSlot{{Name: "Phoenix"}, {Name: "Edgeworth"}}}
	a.ensureCharLower()
	if len(a.charHoverIDs) != len(a.sess.Chars) {
		t.Fatalf("hover ids = %d, want one per character (%d)", len(a.charHoverIDs), len(a.sess.Chars))
	}
	for i := range a.sess.Chars {
		if want := "char:" + a.sess.Chars[i].Name; a.charHoverID(i) != want {
			t.Errorf("id %d = %q, want %q", i, a.charHoverID(i), want)
		}
	}
	if n := testing.AllocsPerRun(200, func() { _ = a.charHoverID(1) }); n != 0 {
		t.Errorf("charHoverID allocates %v per cell, want 0", n)
	}
	// A roster change rebuilds both caches together, never one of them.
	a.charLower = nil
	a.sess.Chars = append(a.sess.Chars, courtroom.CharacterSlot{Name: "Franziska"})
	a.ensureCharLower()
	if len(a.charHoverIDs) != 3 || a.charHoverID(2) != "char:Franziska" {
		t.Errorf("after a roster change: ids %v", a.charHoverIDs)
	}
	// Out of range: a fallback string, not a panic in the middle of a frame.
	if got := a.charHoverID(99); got != "char:" {
		t.Errorf("out-of-range id = %q", got)
	}
}

// TestIniHoverIDsAreCachedNotRebuilt is the Wardrobe tab's twin of the test above, and
// it has to be a twin now that both tabs of char select draw on one plan: the Characters
// grid stopped concatenating "char:"+name per visible cell per frame, while the wardrobe
// cell went on building "iniswap:"+name the same way — on the char-select Wardrobe tab
// AND both wardrobe-panel bodies.
func TestIniHoverIDsAreCachedNotRebuilt(t *testing.T) {
	a := testTabApp(t)
	a.serverKey = "ws://hover.test"
	a.d.Prefs.AddWardrobe(a.serverKey, "Phoenix")
	a.d.Prefs.AddWardrobe(a.serverKey, "Edgeworth")
	a.rebuildIniMenu()
	if len(a.iniList) < 2 {
		t.Fatalf("fixture built a %d-entry wardrobe", len(a.iniList))
	}
	if len(a.iniHoverIDs) != len(a.iniList) {
		t.Fatalf("hover ids = %d, want one per wardrobe entry (%d)", len(a.iniHoverIDs), len(a.iniList))
	}
	for i := range a.iniList {
		if want := iniHoverIDPrefix + a.iniList[i]; a.iniHoverID(i) != want {
			t.Errorf("id %d = %q, want %q", i, a.iniHoverID(i), want)
		}
	}
	if n := testing.AllocsPerRun(200, func() { _ = a.iniHoverID(1) }); n != 0 {
		t.Errorf("iniHoverID allocates %v per cell, want 0", n)
	}
	// A list change rebuilds the cache with it, never leaves it behind.
	a.d.Prefs.AddWardrobe(a.serverKey, "Franziska")
	a.rebuildIniMenu()
	if len(a.iniHoverIDs) != len(a.iniList) {
		t.Errorf("after a wardrobe change: %d ids for %d entries", len(a.iniHoverIDs), len(a.iniList))
	}
	// Out of range: a fallback string, not a panic in the middle of a frame.
	if got := a.iniHoverID(99); got != iniHoverIDPrefix {
		t.Errorf("out-of-range id = %q", got)
	}
}

// TestCharSelectTitleIsCached pins the other per-frame allocation removed here: the
// heading was concatenated with the server name on every frame of a screen that is
// otherwise pure integer layout.
func TestCharSelectTitleIsCached(t *testing.T) {
	a := testTabApp(t)
	if got := a.charSelectTitle(); got != charSelectHeading {
		t.Errorf("no server: title = %q, want %q", got, charSelectHeading)
	}
	a.serverName = "Skrapegropen"
	want := charSelectHeading + " — Skrapegropen"
	if got := a.charSelectTitle(); got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	if n := testing.AllocsPerRun(200, func() { _ = a.charSelectTitle() }); n != 0 {
		t.Errorf("charSelectTitle allocates %v per frame, want 0", n)
	}
	a.serverName = "Somewhere else"
	if got := a.charSelectTitle(); got != charSelectHeading+" — Somewhere else" {
		t.Errorf("a server change did not rebuild the title: %q", got)
	}
}

// TestCharGridPlanIsAllocFree keeps the builder off the heap. It runs once per frame
// on both tabs and cellAt runs per visible cell; neither may allocate. (This is NOT
// the whole-screen gate the spec rules out — drawCharSelect still allocates a char
// icon URL per cell per frame, which is recorded as follow-up work.)
func TestCharGridPlanIsAllocFree(t *testing.T) {
	const w, h int32 = 1920, 1080
	a, lay, g := themedStockPlan(t)
	gridTop := charSelectGridTop(a)
	if n := testing.AllocsPerRun(200, func() { a.charSelectGridPlan(w, h, gridTop, lay) }); n != 0 {
		t.Errorf("charSelectGridPlan allocates %v per call, want 0", n)
	}
	if n := testing.AllocsPerRun(200, func() {
		cell := g.cellAt(17, 67)
		if !g.visible(cell) {
			_ = cell
		}
	}); n != 0 {
		t.Errorf("cellAt/visible allocate %v per cell, want 0", n)
	}
}
