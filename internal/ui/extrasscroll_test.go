package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// Issue #30, "AsyncAO Extras window lacks a scroll container, resulting in buttons
// escaping its container": the widget grid ran its rows straight down from the
// volume sliders with no clip and no scroll, so the only thing keeping the cells
// inside the box was extrasMinH — and that minimum was derived when the widget
// table held 13 entries. It holds 23 now, which is 12 rows and about 500px of grid
// against a 452px default box.

// extrasVolBottom is the Y the four volume-slider rows finish at for a box at r —
// the same arithmetic drawExtrasMainBox hands extrasGridLayout.
func extrasVolBottom(r sdl.Rect) int32 { return r.Y + extrasTitleH + 4 + 4*extrasVolRowH }

// containsRect reports whether inner sits entirely within outer.
func containsRect(outer, inner sdl.Rect) bool {
	return inner.X >= outer.X && inner.Y >= outer.Y &&
		inner.X+inner.W <= outer.X+outer.W && inner.Y+inner.H <= outer.Y+outer.H
}

// TestExtrasGridStaysInsideTheBox is the issue itself, over the box sizes that
// matter: the whole widget table at the minimum height, at the default, and at a
// tall box. Every cell that draws must sit inside the viewport, and the viewport
// must sit inside the box — at BOTH ends of the scroll range.
func TestExtrasGridStaysInsideTheBox(t *testing.T) {
	// The real count: what the box actually has to lay out.
	const shown = int32(23)
	for _, boxH := range []int32{extrasMinH, extrasBoxH, 900} {
		r := sdl.Rect{X: 40, Y: 60, W: extrasBoxW, H: boxH}
		g := extrasGridLayout(r, extrasVolBottom(r), shown)

		if !containsRect(r, g.view) {
			t.Errorf("boxH=%d: the grid viewport %+v escapes the box %+v", boxH, g.view, r)
		}
		if g.view.Y+g.view.H > r.Y+r.H-extrasHintH {
			t.Errorf("boxH=%d: the viewport runs into the hint line's row", boxH)
		}
		for _, scroll := range []int32{0, g.maxScroll} {
			for slot := int32(0); slot < shown; slot++ {
				cell := g.cellRect(slot, scroll)
				// Cells scrolled out of view are skipped by the draw; only the ones
				// that DO draw have anything to guarantee.
				if cell.Y >= g.view.Y+g.view.H || cell.Y+cell.H <= g.view.Y {
					continue
				}
				// A row straddling the viewport edge is normal in a scroll container
				// — the CLIP is what keeps its pixels in. So the vertical guarantee
				// is that the drawn part is non-empty and inside the box; the
				// HORIZONTAL one is absolute, because nothing clips a column that
				// runs off the side and the scrollbar lives in that margin.
				drawn := clipRectTo(cell, g.view)
				if drawn == (sdl.Rect{}) {
					t.Errorf("boxH=%d scroll=%d: cell %d %+v is drawn but clips to nothing", boxH, scroll, slot, cell)
				}
				if !containsRect(r, drawn) {
					t.Errorf("boxH=%d scroll=%d: cell %d draws at %+v, outside the box %+v", boxH, scroll, slot, drawn, r)
				}
				if cell.X < g.view.X || cell.X+cell.W > g.view.X+g.view.W-g.barW {
					t.Errorf("boxH=%d scroll=%d: cell %d %+v runs outside the viewport's columns (view %+v, bar %d)",
						boxH, scroll, slot, cell, g.view, g.barW)
				}
			}
		}
	}
}

// TestExtrasGridScrollReachesTheLastWidget pins that the scroll range is enough to
// SEE the last widget. A container that clips without scrolling far enough is the
// same bug wearing a different hat: Disconnect would simply be unreachable.
func TestExtrasGridScrollReachesTheLastWidget(t *testing.T) {
	const shown = int32(23)
	r := sdl.Rect{X: 0, Y: 0, W: extrasBoxW, H: extrasMinH} // the tightest box there is
	g := extrasGridLayout(r, extrasVolBottom(r), shown)
	if g.maxScroll <= 0 {
		t.Fatalf("fixture: %d widgets in a %dpx box must overflow, got maxScroll=%d", shown, extrasMinH, g.maxScroll)
	}
	last := g.cellRect(shown-1, g.maxScroll)
	if last.Y+last.H > g.view.Y+g.view.H {
		t.Errorf("scrolled fully down, the last widget %+v still sits below the viewport %+v — it can never be clicked",
			last, g.view)
	}
}

// TestExtrasGridScrollbarOnlyWhenItOverflows pins the other half: a box that fits
// its content must not reserve a scrollbar column, or the cells lose width for a
// bar that never draws.
func TestExtrasGridScrollbarOnlyWhenItOverflows(t *testing.T) {
	r := sdl.Rect{X: 0, Y: 0, W: extrasBoxW, H: 900}
	roomy := extrasGridLayout(r, extrasVolBottom(r), 4) // two rows in a 900px box
	if roomy.barW != 0 || roomy.maxScroll != 0 {
		t.Errorf("content that fits must reserve no scrollbar and no scroll range, got barW=%d maxScroll=%d",
			roomy.barW, roomy.maxScroll)
	}
	tight := extrasGridLayout(r, extrasVolBottom(r), 40)
	if tight.barW <= 0 || tight.maxScroll <= 0 {
		t.Errorf("overflowing content must reserve a scrollbar and a scroll range, got barW=%d maxScroll=%d",
			tight.barW, tight.maxScroll)
	}
	if tight.cellW >= roomy.cellW {
		t.Error("the scrollbar must take its width from the cells, not overlap them")
	}
}

// TestExtrasGridSurvivesADegenerateBox pins the floors. A box squeezed by a small
// window (extrasBoxRect clamps to the window, which can go below the furniture's
// own height) must produce an empty viewport, never a negative one — a negative
// height inverts every containment check downstream and a negative cell width
// would send column 1 to the left of column 0.
func TestExtrasGridSurvivesADegenerateBox(t *testing.T) {
	for _, r := range []sdl.Rect{
		{X: 0, Y: 0, W: extrasBoxW, H: 40}, // shorter than the title + volume rows
		{X: 0, Y: 0, W: 12, H: extrasMinH}, // narrower than the insets
		{},                                 // nothing at all
	} {
		g := extrasGridLayout(r, extrasVolBottom(r), 23)
		if g.view.H < 0 || g.view.W < 0 {
			t.Errorf("box %+v produced a negative viewport %+v", r, g.view)
		}
		if g.cellW < 0 {
			t.Errorf("box %+v produced a negative cell width %d", r, g.cellW)
		}
		if g.maxScroll < 0 {
			t.Errorf("box %+v produced a negative scroll range %d", r, g.maxScroll)
		}
	}
}

// TestExtrasMinHeightFitsItsFurniture pins the re-derived floor. extrasMinH no
// longer has to fit every widget — that is what the scroll container is for — but
// it must still leave a usable grid after the title bar, the four volume rows and
// the hint line, or the box's own resize floor produces a zero-row grid.
func TestExtrasMinHeightFitsItsFurniture(t *testing.T) {
	r := sdl.Rect{X: 0, Y: 0, W: extrasMinW, H: extrasMinH}
	g := extrasGridLayout(r, extrasVolBottom(r), 23)
	if rows := (g.view.H + extrasCellGap) / (extrasCellH + extrasCellGap); rows < 2 {
		t.Errorf("at extrasMinH=%d the grid viewport is %dpx — only %d rows; the resize floor must leave at least two",
			extrasMinH, g.view.H, rows)
	}
	if extrasBoxH < extrasMinH {
		t.Errorf("default height %d is below the minimum %d", extrasBoxH, extrasMinH)
	}
}

// TestClipRectTo pins the intersection helper the tear-off hit test relies on: it
// hit-tests with a raw pointIn that pushClip does not reach, so a cell scrolled
// half under the volume sliders must only be grabbable where it is drawn.
func TestClipRectTo(t *testing.T) {
	clip := sdl.Rect{X: 100, Y: 100, W: 200, H: 200}
	for _, tc := range []struct {
		name string
		r    sdl.Rect
		want sdl.Rect
	}{
		{"fully inside", sdl.Rect{X: 120, Y: 120, W: 50, H: 50}, sdl.Rect{X: 120, Y: 120, W: 50, H: 50}},
		{"clipped at the top", sdl.Rect{X: 120, Y: 80, W: 50, H: 50}, sdl.Rect{X: 120, Y: 100, W: 50, H: 30}},
		{"clipped at the bottom", sdl.Rect{X: 120, Y: 280, W: 50, H: 50}, sdl.Rect{X: 120, Y: 280, W: 50, H: 20}},
		{"entirely above", sdl.Rect{X: 120, Y: 10, W: 50, H: 50}, sdl.Rect{}},
		{"entirely below", sdl.Rect{X: 120, Y: 400, W: 50, H: 50}, sdl.Rect{}},
		{"touching the edge only", sdl.Rect{X: 120, Y: 50, W: 50, H: 50}, sdl.Rect{}},
	} {
		if got := clipRectTo(tc.r, clip); got != tc.want {
			t.Errorf("%s: clipRectTo(%+v) = %+v, want %+v", tc.name, tc.r, got, tc.want)
		}
	}
}
