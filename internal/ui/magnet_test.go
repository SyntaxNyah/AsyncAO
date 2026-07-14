package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestSnapToSiblingsNoOpBeforeRect pins the floatWin.snapToSiblings rect()-guard
// invariant (mirrors snapToEdges): while lastWinW/lastWinH are unset (rect() has
// not run this session) the sibling snap is a no-op, even with a candidate that
// would otherwise capture it.
func TestSnapToSiblingsNoOpBeforeRect(t *testing.T) {
	a := testTabApp(t)
	a.panelMagnetRects = []sdl.Rect{{X: 100, Y: 100, W: 200, H: 150}}
	fw := &floatWin{x: 104, y: 100} // 4 px off the candidate's left; but lastWinW/H == 0
	a.snapToSiblings(fw)
	if fw.x != 104 || fw.y != 100 {
		t.Errorf("snapToSiblings must be a no-op until rect() stamps the dims, moved to (%d,%d)", fw.x, fw.y)
	}
}

// TestSnapToSiblingsEmptyCensusNoOp pins that with no candidates (a settled frame
// or the only open panel) the sibling snap leaves the window exactly where it is.
func TestSnapToSiblingsEmptyCensusNoOp(t *testing.T) {
	a := testTabApp(t)
	a.panelMagnetRects = a.panelMagnetRects[:0]
	fw := &floatWin{x: 250, y: 175, lastW: 200, lastH: 150, lastWinW: 1000, lastWinH: 800}
	a.snapToSiblings(fw)
	if fw.x != 250 || fw.y != 175 {
		t.Errorf("empty census must not move the window, moved to (%d,%d)", fw.x, fw.y)
	}
}

// TestLivePanelDragPersistsSnappedRect drives a REAL headless floatWin drag and
// pins the persist-ordering guarantee (the required test): a drag that lands
// within alignSnapPx of a sibling's edge snaps flush INSIDE floatWinDrag, and the
// drag-end persist (persistMsgSlot) then records the SNAPPED spot — not the raw
// mouse position. This proves the magnet mutates fw.x/fw.y before the gesture-END
// slot write sees it.
func TestLivePanelDragPersistsSnappedRect(t *testing.T) {
	a := testTabApp(t)
	const w, h = int32(1000), int32(800)

	// Give the Group Chat panel a known placed geometry and stamp its rect() dims
	// (lastW/lastH/lastWinW/lastWinH) so snapToSiblings can run.
	a.msgWin.x, a.msgWin.y, a.msgWin.w, a.msgWin.h, a.msgWin.placed = 300, 200, 480, 360, true
	r0 := a.msgPanelRect(w, h) // stamps the floatWin's last* dims via rect()

	// A sibling panel whose LEFT edge sits a few px from where the drag will land,
	// seeded into the per-frame census the drag handler reads.
	siblingX := int32(360)
	a.panelMagnetRects = []sdl.Rect{{X: siblingX, Y: 40, W: 200, H: 150}}

	handle := sdl.Rect{X: r0.X, Y: r0.Y, W: r0.W - 80, H: floatTitleH}

	// Press the title bar (grab offset 5,5).
	a.ctx.mouseDown = true
	a.ctx.mouseX, a.ctx.mouseY = handle.X+5, handle.Y+5
	pressed := true
	a.floatWinDrag(&a.msgWin, handle, &pressed)
	if !a.msgWin.dragging {
		t.Fatal("a press on the title must start the drag")
	}

	// Move so the raw top-left would be siblingX+4 (4 px off the sibling's left
	// edge, within alignSnapPx=6) — the magnet must pull it flush to siblingX.
	a.ctx.mouseX = siblingX + 4 + 5 // +5 = the grab offset captured above
	pressed = false
	a.floatWinDrag(&a.msgWin, handle, &pressed)
	if a.msgWin.x != siblingX {
		t.Fatalf("mid-drag: X must snap flush to the sibling edge %d, got %d", siblingX, a.msgWin.x)
	}

	// Release + persist (the gesture-END path drawMessagesPanel runs).
	a.ctx.mouseDown = false
	a.floatWinDrag(&a.msgWin, handle, &pressed)
	a.persistMsgSlot(w, h)

	// The persisted rect must reflect the SNAPPED X (siblingX / w), not the raw one.
	wantFracX := float64(siblingX) / float64(w)
	got := a.d.Prefs.ClassicLayoutOverrides()[slotMessages]
	if got[0] != wantFracX {
		t.Errorf("persisted frac X = %v, want the snapped %v (raw drop was %v)",
			got[0], wantFracX, float64(siblingX+4)/float64(w))
	}
}

// TestPanelMagnetCensusExcludesDraggedAndClosed pins the two behavioural fixes:
// the census skips the surface currently being dragged (by identity, via its drag
// flag) and any panel that isn't open, so a dragged panel never snaps to itself
// or to an invisible sibling. It also stays within panelMagnetCap.
func TestPanelMagnetCensusExcludesDraggedAndClosed(t *testing.T) {
	a := testTabApp(t)
	const w, h = int32(1000), int32(800)

	// Two panels open; the pair panel is the one being dragged.
	a.showPair = true
	a.pairWin.dragging = true
	a.showEvid = true // a second, non-dragged open panel → a legit candidate
	// A closed panel must contribute nothing even though its floatWin has a rect.
	a.showCMPanel = false

	a.rebuildPanelMagnetRects(w, h)

	if len(a.panelMagnetRects) == 0 {
		t.Fatal("an open non-dragged panel must appear in the census")
	}
	if len(a.panelMagnetRects) > panelMagnetCap {
		t.Errorf("census %d exceeds panelMagnetCap %d", len(a.panelMagnetRects), panelMagnetCap)
	}
	// The dragged pair panel's rect must NOT be present (identity skip).
	dragged := a.pairPanelRect(w, h)
	for _, r := range a.panelMagnetRects {
		if r == dragged {
			t.Error("the dragged panel must be excluded from its own candidate set")
		}
	}
	// The closed CM panel's rect must NOT be present.
	closed := a.cmPanelRect(w, h)
	for _, r := range a.panelMagnetRects {
		if r == closed {
			t.Error("a closed panel must not be a magnet candidate")
		}
	}

	// Nothing dragging → the census is empty (drag-gated; settled-frame no-op).
	a.pairWin.dragging = false
	a.rebuildPanelMagnetRects(w, h)
	if len(a.panelMagnetRects) != 0 {
		t.Errorf("no drag active → census must be empty, got %d", len(a.panelMagnetRects))
	}
}
