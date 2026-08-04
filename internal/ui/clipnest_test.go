package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// The clip-NESTING contract. Nothing in the tree pinned it before, which is how a
// themed AO2 chatlog came to paint over its own widget border: every scrollable
// list wraps its row loop in pushClip, but labelEmoji's per-glyph raster branch
// pushes an INNER clip that is deliberately the row's full height at the row's y —
// and for a partially scrolled top row that band starts ABOVE the list. pushClip
// used to REPLACE the outer clip with it, so the panel's own scissor was gone for
// the duration of that blit. Only rows containing colour emoji or mixed scripts
// took the raster branch, which is why it looked like a font bug: every plain row
// beside it stopped neatly at the panel edge.
//
// clipIntersect is pushClip's whole arithmetic, split out so this runs headless.
func TestClipIntersectNarrowsNeverWidens(t *testing.T) {
	outer := sdl.Rect{X: 100, Y: 100, W: 200, H: 100}
	for _, tc := range []struct {
		name  string
		inner sdl.Rect
		want  sdl.Rect
	}{
		{"escapes the TOP (the log's partially scrolled first row)",
			sdl.Rect{X: 100, Y: 80, W: 200, H: 40}, sdl.Rect{X: 100, Y: 100, W: 200, H: 20}},
		{"escapes the BOTTOM (a taller emoji raster on the last row)",
			sdl.Rect{X: 100, Y: 180, W: 200, H: 60}, sdl.Rect{X: 100, Y: 180, W: 200, H: 20}},
		{"escapes the LEFT", sdl.Rect{X: 40, Y: 120, W: 100, H: 20}, sdl.Rect{X: 100, Y: 120, W: 40, H: 20}},
		{"escapes the RIGHT", sdl.Rect{X: 250, Y: 120, W: 200, H: 20}, sdl.Rect{X: 250, Y: 120, W: 50, H: 20}},
		{"fully inside is untouched", sdl.Rect{X: 120, Y: 120, W: 50, H: 20}, sdl.Rect{X: 120, Y: 120, W: 50, H: 20}},
		{"wider than the outer on every side collapses to the outer", outer, outer},
	} {
		if got := clipIntersect(tc.inner, outer, true); got != tc.want {
			t.Errorf("%s: clipIntersect(%+v, %+v) = %+v, want %+v", tc.name, tc.inner, outer, got, tc.want)
		}
	}
}

// TestClipIntersectDisjointGoesNowhere pins the SDL trap: SDL_RenderSetClipRect
// treats a w<=0 || h<=0 rect as "turn clipping OFF", i.e. handing it an empty
// intersection does the exact opposite of what the caller asked. A disjoint (or
// degenerate) result must become the off-screen sentinel instead.
func TestClipIntersectDisjointGoesNowhere(t *testing.T) {
	outer := sdl.Rect{X: 100, Y: 100, W: 200, H: 100}
	for _, tc := range []struct {
		name  string
		inner sdl.Rect
	}{
		{"far below", sdl.Rect{X: 100, Y: 400, W: 200, H: 20}},
		{"far right", sdl.Rect{X: 900, Y: 120, W: 50, H: 20}},
		{"touching edges only", sdl.Rect{X: 300, Y: 100, W: 50, H: 100}},
	} {
		got := clipIntersect(tc.inner, outer, true)
		if got != clipNowhere {
			t.Errorf("%s: clipIntersect(%+v, %+v) = %+v, want clipNowhere", tc.name, tc.inner, outer, got)
		}
		if got.W <= 0 || got.H <= 0 {
			t.Errorf("%s: an empty rect would DISABLE clipping in SDL: %+v", tc.name, got)
		}
	}
}

// TestClipIntersectWithoutOuterIsTheRectItself pins the top-level case: the very
// first pushClip of a pass has nothing to intersect with, so a plain rect passes
// through unchanged — and a degenerate one still becomes the sentinel rather than
// silently turning clipping off.
func TestClipIntersectWithoutOuterIsTheRectItself(t *testing.T) {
	r := sdl.Rect{X: 10, Y: 20, W: 30, H: 40}
	if got := clipIntersect(r, sdl.Rect{}, false); got != r {
		t.Errorf("no outer clip: got %+v, want %+v", got, r)
	}
	if got := clipIntersect(sdl.Rect{X: 5, Y: 5}, sdl.Rect{}, false); got != clipNowhere {
		t.Errorf("degenerate rect with no outer clip: got %+v, want clipNowhere", got)
	}
	// The settings-index harvest pass clips to the sentinel at the TOP level; it
	// must survive unchanged, or the pass would draw for real.
	if got := clipIntersect(clipNowhere, sdl.Rect{}, false); got != clipNowhere {
		t.Errorf("the off-screen sentinel must survive a top-level push: %+v", got)
	}
}
