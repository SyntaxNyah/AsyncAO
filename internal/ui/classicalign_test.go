package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestAlignRectMoveSnapsToNeighbourEdge pins the Inkscape-style magnet: a
// moved box whose left edge lands within alignSnapPx of a neighbour's left
// edge snaps flush to it and reports a vertical guide there.
func TestAlignRectMoveSnapsToNeighbourEdge(t *testing.T) {
	other := sdl.Rect{X: 100, Y: 300, W: 200, H: 80}
	r := sdl.Rect{X: 104, Y: 50, W: 60, H: 40} // 4 px off other's left edge
	got, guides := alignRect(r, []sdl.Rect{other}, 1280, 720, true, 0, nil)
	if got.X != 100 {
		t.Errorf("left edge must snap to the neighbour's (100), got %d", got.X)
	}
	if got.W != 60 || got.H != 40 {
		t.Errorf("a move snap must not resize: got %dx%d", got.W, got.H)
	}
	if len(guides) == 0 || !guides[0].vertical || guides[0].pos != 100 {
		t.Errorf("want a vertical guide at 100, got %+v", guides)
	}
}

// TestAlignRectMoveSnapsToWindowCentre pins the screen-centre target (the
// "anchor to the centre of the entire screen" ask): a box whose centre is
// near w/2 centres exactly.
func TestAlignRectMoveSnapsToWindowCentre(t *testing.T) {
	r := sdl.Rect{X: 615, Y: 50, W: 60, H: 40} // centre 645; w/2 = 640, off by 5
	got, guides := alignRect(r, nil, 1280, 720, true, 0, nil)
	if cx := got.X + got.W/2; cx != 640 {
		t.Errorf("centre must snap to w/2=640, got %d", cx)
	}
	if len(guides) == 0 || !guides[0].vertical || guides[0].pos != 640 {
		t.Errorf("want a vertical guide at 640, got %+v", guides)
	}
}

// TestAlignRectResizeMovesOnlyGrippedEdge pins resize semantics: dragging the
// right edge near a neighbour's edge snaps WIDTH (left edge fixed), and
// far-off edges don't snap at all.
func TestAlignRectResizeMovesOnlyGrippedEdge(t *testing.T) {
	other := sdl.Rect{X: 500, Y: 300, W: 100, H: 80}
	r := sdl.Rect{X: 100, Y: 50, W: 396, H: 40} // right edge 496; other's left 500
	got, guides := alignRect(r, []sdl.Rect{other}, 1280, 720, false, edgeR, nil)
	if got.X != 100 {
		t.Errorf("the un-gripped left edge must stay put, got X=%d", got.X)
	}
	if got.X+got.W != 500 {
		t.Errorf("right edge must snap flush to 500, got %d", got.X+got.W)
	}
	if len(guides) != 1 || guides[0].pos != 500 {
		t.Errorf("want one vertical guide at 500, got %+v", guides)
	}
	// Nothing within tolerance → untouched, no guides.
	far := sdl.Rect{X: 100, Y: 50, W: 300, H: 40}
	got, guides = alignRect(far, []sdl.Rect{other}, 1280, 720, false, edgeR, guides[:0])
	if got != far || len(guides) != 0 {
		t.Errorf("out-of-tolerance rect must pass through unchanged, got %+v guides %+v", got, guides)
	}
}

// TestNextLayoutGridSize pins the Grid chip cycle, including recovery from a
// legacy/hand-edited value that isn't in the cycle.
func TestNextLayoutGridSize(t *testing.T) {
	if got := nextLayoutGridSize(8); got != 16 {
		t.Errorf("8 → %d, want 16", got)
	}
	if got := nextLayoutGridSize(32); got != 4 {
		t.Errorf("32 must wrap to 4, got %d", got)
	}
	if got := nextLayoutGridSize(13); got != layoutGridSizes[0] {
		t.Errorf("unknown value must restart the cycle, got %d", got)
	}
}
