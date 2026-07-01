package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestOpenDropdownPunchesThroughPanelFence pins the custom-layout playtest bug
// ("the popup is layered beneath some elements — can't select higher than Gray"):
// an OPEN dropdown's list paints ABOVE the floating panels, so while the cursor is
// over that list the courtroom pass must NOT be pointer-fenced — even when the
// cursor is simultaneously inside a floating panel's rect. Closed (or cursor off
// the list), the panel fence behaves exactly as before.
func TestOpenDropdownPunchesThroughPanelFence(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	const w, h = int32(1000), int32(800)

	// A floating panel is open and the cursor sits inside it: fenced (baseline).
	a.showEvid = true
	r := a.evidPanelRect(w, h)
	a.ctx.mouseX, a.ctx.mouseY = r.X+5, r.Y+5
	if !a.boxFencesPointer(w, h) {
		t.Fatal("baseline: cursor over the evidence panel must fence the courtroom")
	}

	// An open dropdown whose flipped-up list covers that same spot: the list is
	// visually on top, so the fence must stand down (input follows visuals).
	a.ctx.ddOpen = "iccolor"
	a.ctx.ddOpenList = sdl.Rect{X: r.X, Y: r.Y, W: 120, H: 300}
	if a.boxFencesPointer(w, h) {
		t.Error("an open dropdown list over the panel must punch through the fence (dead-rows bug)")
	}

	// Cursor over the panel but OFF the list: the panel fences as usual.
	a.ctx.mouseX = r.X + 200
	if int32(200) < a.ctx.ddOpenList.W {
		t.Fatal("test geometry: cursor must be off the list")
	}
	if !a.boxFencesPointer(w, h) {
		t.Error("cursor off the open list must still fence over the panel")
	}

	// Dropdown closed: the stale list rect is never consulted.
	a.ctx.mouseX = r.X + 5
	a.ctx.ddOpen = ""
	if !a.boxFencesPointer(w, h) {
		t.Error("a closed dropdown must not punch through (stale ddOpenList consulted?)")
	}
}
