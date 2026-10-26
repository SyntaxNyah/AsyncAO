package ui

// Layered-input regression tests (playtest: scrolling the What's New modal
// also scrolled the lobby server list behind it — "mouse events don't
// differentiate between layers"). Three pins: the wheel is single-consumer
// per frame, the update modal holds/releases the kit's modal fence, and the
// floating hotkey sheet fences the screen pass while it owns the cursor.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/update"
)

// TestWheelInSingleConsumer pins that two stacked scroll regions can't both
// take the same frame's wheel: the first hovered WheelIn wins, every later
// one (an overlapped list underneath) reads 0.
func TestWheelInSingleConsumer(t *testing.T) {
	c := &Ctx{mouseX: 50, mouseY: 50, wheelY: 3}
	top := sdl.Rect{X: 0, Y: 0, W: 200, H: 200}
	under := sdl.Rect{X: 0, Y: 0, W: 400, H: 400} // fully overlaps `top`

	if got := c.WheelIn(top); got != 3 {
		t.Fatalf("first hovered WheelIn = %d, want 3", got)
	}
	if got := c.WheelIn(under); got != 0 {
		t.Fatalf("second overlapping WheelIn = %d, want 0 (wheel already consumed)", got)
	}
	if !c.wheelTaken {
		t.Error("a consumed wheel must be marked taken for the page-level handlers")
	}

	// A miss must NOT consume: cursor outside the first region leaves the
	// wheel for the region actually under the pointer.
	c2 := &Ctx{mouseX: 300, mouseY: 300, wheelY: -2}
	if got := c2.WheelIn(top); got != 0 {
		t.Fatalf("unhovered WheelIn = %d, want 0", got)
	}
	if got := c2.WheelIn(under); got != -2 {
		t.Fatalf("hovered WheelIn after a miss = %d, want -2", got)
	}
}

// TestUpdateModalFence pins the What's New modal's modal-fence lifecycle:
// modalOn while shown (everything underneath pointer-blind, on any screen),
// released the frame after it closes — an un-released modalOn freezes the UI.
func TestUpdateModalFence(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx

	a.updateRel = &update.Release{Version: "9.9.9"}
	a.updateShow = true
	a.updateModalFence(c)
	if !c.modalOn {
		t.Fatal("open modal must hold the modal fence")
	}
	if c.hovering(sdl.Rect{X: -100, Y: -100, W: 10000, H: 10000}) {
		t.Fatal("hovering() must be false everywhere under the modal fence")
	}

	a.updateShow = false // Close / click-away / Esc
	a.updateModalFence(c)
	if c.modalOn {
		t.Fatal("closing the modal must release the fence")
	}
	// And a second pass stays released (no flip-flop).
	a.updateModalFence(c)
	if c.modalOn {
		t.Fatal("fence must stay released while the modal is closed")
	}
}

// TestHkSheetFencesPointer pins the sheet's screen-pass fence: hovered or
// mid-drag/resize fences, elsewhere doesn't, closed never does.
func TestHkSheetFencesPointer(t *testing.T) {
	a := testTabApp(t)
	const w, h = int32(1280), int32(720)

	a.showHotkeys = true
	r := a.hkSheetRect(w, h)
	a.ctx.mouseX, a.ctx.mouseY = r.X+r.W/2, r.Y+r.H/2
	if !a.hkSheetFencesPointer(w, h) {
		t.Fatal("cursor over the open sheet must fence the screen pass")
	}

	a.ctx.mouseX, a.ctx.mouseY = r.X-10, r.Y-10
	if a.hkSheetFencesPointer(w, h) {
		t.Fatal("cursor off the sheet must not fence")
	}

	a.hkWin.dragging = true // a drag in flight keeps the fence even off-rect
	if !a.hkSheetFencesPointer(w, h) {
		t.Fatal("an in-flight sheet drag must keep the fence")
	}
	a.hkWin.dragging = false

	a.showHotkeys = false
	a.ctx.mouseX, a.ctx.mouseY = r.X+r.W/2, r.Y+r.H/2
	if a.hkSheetFencesPointer(w, h) {
		t.Fatal("a closed sheet must never fence")
	}
}
