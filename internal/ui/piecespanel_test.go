package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// The Hide-UI-pieces panel became a floating window.
//
// It used to be nailed to the bottom-right corner: dragging did nothing, and the
// layout editor did not offer it either, so a panel that landed awkwardly — or
// underneath something else — could not be shifted at all. That is a poor place
// for the one surface whose job is rescuing a layout.

const (
	piecesTestW = 1280
	piecesTestH = 720
)

// TestPiecesPanelKeepsItsOldHomeUntilMoved is the compatibility half, and the
// reason the corner maths was kept rather than replaced by a centred default. A
// panel becoming draggable must not RELOCATE for people who were happy with it:
// nothing has been dragged and nothing is saved, so it must appear exactly where
// it always did.
func TestPiecesPanelKeepsItsOldHomeUntilMoved(t *testing.T) {
	a := testTabApp(t)
	got := a.toolboxPiecesRect(piecesTestW, piecesTestH)

	if got.W != toolboxPiecesW {
		t.Errorf("width = %d, want the unchanged %d", got.W, toolboxPiecesW)
	}
	// Bottom-right: hard against the right margin, sitting above the toolbox strip.
	if wantX := piecesTestW - got.W - compactToolboxMargin; got.X != wantX {
		t.Errorf("x = %d, want %d — the panel moved for a user who never touched it", got.X, wantX)
	}
	wantY := piecesTestH - compactGripH - compactToolboxMargin - toolboxPiecesTopGap - got.H
	if got.Y != wantY {
		t.Errorf("y = %d, want %d", got.Y, wantY)
	}
}

// TestPiecesPanelFollowsWhereItWasDragged is the feature itself: once placed, the
// panel is wherever the user put it.
func TestPiecesPanelFollowsWhereItWasDragged(t *testing.T) {
	a := testTabApp(t)
	before := a.toolboxPiecesRect(piecesTestW, piecesTestH)

	// Simulate a completed drag to the top-left.
	a.piecesWin.x, a.piecesWin.y = 40, 60
	a.piecesWin.placed = true

	got := a.toolboxPiecesRect(piecesTestW, piecesTestH)
	if got.X == before.X && got.Y == before.Y {
		t.Fatal("the panel ignored its placement — dragging it would do nothing, which is the bug")
	}
	if got.X != 40 || got.Y != 60 {
		t.Errorf("panel at %d,%d, want 40,60", got.X, got.Y)
	}
}

// TestPiecesPanelStaysOnScreen: a floating window that can be dragged can be
// dragged somewhere unhelpful, and this one is the route back from a broken
// layout — so it must never be the thing that strands you. floatWin.rect clamps;
// this pins that the panel actually goes through it.
func TestPiecesPanelStaysOnScreen(t *testing.T) {
	a := testTabApp(t)
	for _, pos := range []struct{ x, y int32 }{
		{-5000, -5000},
		{9000, 9000},
		{0, -400},
	} {
		a.piecesWin.x, a.piecesWin.y = pos.x, pos.y
		a.piecesWin.placed = true
		got := a.toolboxPiecesRect(piecesTestW, piecesTestH)
		if got.X+got.W <= 0 || got.X >= piecesTestW || got.Y+got.H <= 0 || got.Y >= piecesTestH {
			t.Errorf("dropped at %d,%d the panel resolved fully off-screen: %+v", pos.x, pos.y, got)
		}
	}
}

// TestPiecesPanelIsInTheFloatingRegistry ties it to the machinery that gives it
// position persistence and the de-overlap cascade. Being in the table is what
// makes the panel behave like every other floating window rather than like a
// special case that has to be remembered.
func TestPiecesPanelIsInTheFloatingRegistry(t *testing.T) {
	a := testTabApp(t)
	var found *panelSlot
	for i := range panelSlotTable {
		if panelSlotTable[i].slot == slotPanelPieces {
			found = &panelSlotTable[i]
			break
		}
	}
	if found == nil {
		t.Fatal("the pieces panel is not in panelSlotTable — no saved position, no de-overlap")
	}
	if found.fw(a) != &a.piecesWin {
		t.Error("the registry row points at the wrong floatWin")
	}
	// Its visibility predicate must agree with the draw's own early return, or the
	// de-overlap census would shove other panels away from one that is not shown.
	a.toolboxPinned, a.toolboxPieces = false, false
	if found.open(a) {
		t.Error("the panel reports visible while closed — siblings would dodge a panel that is not there")
	}
	a.toolboxPinned, a.toolboxPieces = true, true
	if !found.open(a) {
		t.Error("the panel reports hidden while open")
	}
}

// TestPiecesDragHandleClearsTheFilterBox: the title strip is the handle, and it
// must stop above the filter field — otherwise grabbing the panel to move it
// would swallow clicks meant for typing in it.
func TestPiecesDragHandleClearsTheFilterBox(t *testing.T) {
	a := testTabApp(t)
	panel := a.toolboxPiecesRect(piecesTestW, piecesTestH)
	handle := sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W, H: toolboxFilterY}
	filter := sdl.Rect{X: panel.X + pad, Y: panel.Y + toolboxFilterY, W: panel.W - 2*pad, H: toolboxFilterH}

	if handle.Y+handle.H > filter.Y {
		t.Errorf("the drag handle (%+v) overlaps the filter box (%+v) — dragging would eat clicks meant for typing",
			handle, filter)
	}
	if handle.H <= 0 {
		t.Error("the drag handle has no height; the panel could not be grabbed at all")
	}
}
