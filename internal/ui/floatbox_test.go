package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestExtrasBoxVisibility pins when the floating Extras box draws: opened, in a
// live courtroom, and not shadowed by a blocking popup.
func TestExtrasBoxVisibility(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}

	a.showWidgets = true
	if !a.extrasBoxVisible() {
		t.Fatal("open box in a live courtroom must be visible")
	}
	a.showEvid = true // a blocking courtroom popup
	if a.extrasBoxVisible() {
		t.Error("a blocking popup must hide the box (it reappears when that closes)")
	}
	a.showEvid = false
	a.showWidgets = false
	if a.extrasBoxVisible() {
		t.Error("a closed box must not be visible")
	}
}

// TestBoxFencesPointer pins the input fence: the courtroom runs pointer-blind
// only while the box is up AND the cursor is over it — so clicks in the box
// don't leak through, but the courtroom stays live everywhere else.
func TestBoxFencesPointer(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	a.showWidgets = true
	const w, h = int32(1280), int32(720)
	r := a.extrasBoxRect(w, h)

	a.ctx.mouseX, a.ctx.mouseY = r.X+5, r.Y+5
	if !a.boxFencesPointer(w, h) {
		t.Error("pointer over the box must fence the courtroom")
	}
	a.ctx.mouseX, a.ctx.mouseY = r.X-20, r.Y-20
	if a.boxFencesPointer(w, h) {
		t.Error("pointer off the box must NOT fence (courtroom stays live)")
	}
	a.showWidgets = false // closed
	a.ctx.mouseX, a.ctx.mouseY = r.X+5, r.Y+5
	if a.boxFencesPointer(w, h) {
		t.Error("a closed box must never fence")
	}
}

// TestExtrasDrag pins title-bar move: a press on the handle starts a drag, the
// box then tracks the cursor by the captured grab offset and is marked placed,
// and release ends the drag.
func TestExtrasDrag(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	a.showWidgets = true
	const w, h = int32(1280), int32(720)
	r0 := a.extrasBoxRect(w, h)
	handle := sdl.Rect{X: r0.X, Y: r0.Y, W: r0.W - 30, H: extrasTitleH}

	a.ctx.mouseDown = true
	a.ctx.mouseX, a.ctx.mouseY = handle.X+20, handle.Y+10
	a.handleExtrasDrag(handle, w, h) // press → start drag (grab offset 20,10)
	if !a.extrasDragging {
		t.Fatal("a press on the title bar must start a drag")
	}

	a.ctx.mouseX, a.ctx.mouseY = handle.X+120, handle.Y+60 // move +100,+50
	a.handleExtrasDrag(handle, w, h)                        // continue (no new press)
	if moved := a.extrasBoxRect(w, h); moved.X != r0.X+100 || moved.Y != r0.Y+50 {
		t.Errorf("box at (%d,%d), want (%d,%d) — must track the cursor by the grab offset",
			moved.X, moved.Y, r0.X+100, r0.Y+50)
	}
	if !a.extrasPlaced {
		t.Error("a drag must mark the box placed")
	}

	a.ctx.mouseDown = false
	a.handleExtrasDrag(handle, w, h)
	if a.extrasDragging {
		t.Error("release must end the drag")
	}
}

// TestPointerFence pins the fence round-trip: it blanks the live pointer, is
// idempotent (a second fence must NOT save the blanked state), and restores the
// original on unfence — the heart of the non-blocking box's input isolation.
func TestPointerFence(t *testing.T) {
	c := &Ctx{mouseX: 100, mouseY: 50, clicked: true, rightClicked: true, mouseDown: true, wheelY: 3}
	c.fencePointer()
	if c.mouseX != -1 || c.mouseY != -1 || c.clicked || c.rightClicked || c.mouseDown || c.wheelY != 0 {
		t.Fatalf("fence must blank the pointer, got x=%d clk=%v wheel=%d", c.mouseX, c.clicked, c.wheelY)
	}
	c.fencePointer() // idempotent: must not overwrite the saved real state with the blank
	c.unfencePointer()
	if c.mouseX != 100 || c.mouseY != 50 || !c.clicked || !c.rightClicked || !c.mouseDown || c.wheelY != 3 {
		t.Fatalf("unfence must restore the original, got x=%d clk=%v wheel=%d", c.mouseX, c.clicked, c.wheelY)
	}
	c.unfencePointer() // no-op when not fenced
	if c.mouseX != 100 {
		t.Fatal("double unfence must be a no-op")
	}
}
