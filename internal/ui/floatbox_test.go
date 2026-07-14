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
	a.showIni = true // a blocking courtroom popup (showEvid is non-blocking now, #5)
	if a.extrasBoxVisible() {
		t.Error("a blocking popup must hide the box (it reappears when that closes)")
	}
	a.showIni = false
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
	pressed := true
	a.handleExtrasDrag(handle, w, h, &pressed) // press → start drag (grab offset 20,10)
	if !a.extrasDragging {
		t.Fatal("a press on the title bar must start a drag")
	}
	if pressed {
		t.Error("grabbing the title must consume the frame's press edge")
	}

	a.ctx.mouseX, a.ctx.mouseY = handle.X+120, handle.Y+60 // move +100,+50
	pressed = false
	a.handleExtrasDrag(handle, w, h, &pressed) // continue (no new press)
	if moved := a.extrasBoxRect(w, h); moved.X != r0.X+100 || moved.Y != r0.Y+50 {
		t.Errorf("box at (%d,%d), want (%d,%d) — must track the cursor by the grab offset",
			moved.X, moved.Y, r0.X+100, r0.Y+50)
	}
	if !a.extrasPlaced {
		t.Error("a drag must mark the box placed")
	}

	a.ctx.mouseDown = false
	a.handleExtrasDrag(handle, w, h, &pressed)
	if a.extrasDragging {
		t.Error("release must end the drag")
	}
}

// TestExtrasWidgetTearOff pins the tear-off: a press arms tracking, a drag past
// the threshold pops the widget into its own box (removed from the grid, set as
// the active drag), and that box then tracks the cursor by its grab offset.
func TestExtrasWidgetTearOff(t *testing.T) {
	a := testTabApp(t)
	const w, h = int32(1280), int32(720)
	cell := sdl.Rect{X: 200, Y: 200, W: 160, H: 34}
	const id = 3

	a.ctx.mouseDown = true
	a.ctx.mouseX, a.ctx.mouseY = cell.X+20, cell.Y+15
	pressed := true
	if a.extrasTearDetect(id, cell, &pressed) {
		t.Fatal("a press without movement must not tear yet")
	}
	if !a.extrasPressing || pressed {
		t.Fatal("the press must arm tear tracking and consume the edge")
	}

	a.ctx.mouseX += extrasTearPx + 4 // drag past the threshold
	pressed = false
	if !a.extrasTearDetect(id, cell, &pressed) {
		t.Fatal("dragging past the threshold must tear the widget out")
	}
	if len(a.extrasDetached) != 1 || a.extrasDetached[0].id != id {
		t.Fatalf("torn-off set = %v, want one box for id %d", a.extrasDetached, id)
	}
	if !a.widgetDetached(id) {
		t.Error("the torn widget must be filtered out of the grid")
	}
	if !a.extrasDetachDragging || a.extrasDetachIdx != 0 {
		t.Error("the new box must become the active drag target")
	}

	// The new box tracks the cursor by the grab offset captured at tear time.
	a.ctx.mouseX, a.ctx.mouseY = 500, 400
	a.handleDetachedDrag(0, sdl.Rect{}, w, h, &pressed) // continue-drag path (not on the handle)
	r := a.detachedBoxRect(0, w, h)
	if wantX, wantY := int32(500)-detachedBoxW/2, int32(400)-extrasTitleH/2; r.X != wantX || r.Y != wantY {
		t.Errorf("torn box at (%d,%d), want (%d,%d)", r.X, r.Y, wantX, wantY)
	}

	a.ctx.mouseDown = false
	a.handleDetachedDrag(0, sdl.Rect{}, w, h, &pressed)
	if a.extrasDetachDragging {
		t.Error("release must end the detached drag")
	}
}

// TestExtrasReattach pins closing a torn-off box: it's removed, its widget
// returns to the grid, and no drag is left dangling.
func TestExtrasReattach(t *testing.T) {
	a := testTabApp(t)
	a.extrasDetached = []detachedWidget{{id: 5, x: 100, y: 100}, {id: 2, x: 200, y: 200}}
	a.extrasDetachDragging = true
	a.reattachWidget(0)
	if len(a.extrasDetached) != 1 || a.extrasDetached[0].id != 2 {
		t.Fatalf("after closing box 0, detached = %v, want one box for id 2", a.extrasDetached)
	}
	if a.widgetDetached(5) {
		t.Error("id 5 must return to the grid")
	}
	if a.extrasDetachDragging {
		t.Error("closing a box must not leave a drag active")
	}
}

// TestBoxFencesPointerDetached pins the fence over torn-off boxes: the cursor
// over a detached box fences even off the main box, an in-flight drag fences
// regardless of position, and the cursor clear of every box does not.
func TestBoxFencesPointerDetached(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	a.showWidgets = true
	const w, h = int32(1280), int32(720)
	a.extrasDetached = []detachedWidget{{id: 1, x: 900, y: 600}}
	dr := a.detachedBoxRect(0, w, h)
	if pointIn(dr.X+5, dr.Y+5, a.extrasBoxRect(w, h)) {
		t.Skip("detached box overlaps the main box in this layout")
	}

	a.ctx.mouseX, a.ctx.mouseY = dr.X+5, dr.Y+5
	if !a.boxFencesPointer(w, h) {
		t.Error("cursor over a torn-off box must fence the courtroom")
	}
	a.ctx.mouseX, a.ctx.mouseY = 5, 5
	if a.boxFencesPointer(w, h) {
		t.Error("cursor clear of every box must not fence")
	}
	a.extrasDetachDragging = true // a fast drag must hold the fence between frames
	if !a.boxFencesPointer(w, h) {
		t.Error("an active drag must hold the fence regardless of cursor position")
	}
}

// TestExtrasDetachedPersistsClosed pins that closing the main box does NOT drop
// the widgets you tore out: with showWidgets=false but a torn-off box present on
// a live surface, that box still fences the courtroom (clicks can't leak), while
// the closed main box does not.
func TestExtrasDetachedPersistsClosed(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	const w, h = int32(1280), int32(720)
	a.extrasDetached = []detachedWidget{{id: 1, x: 900, y: 600}}
	a.showWidgets = false // main box CLOSED
	dr := a.detachedBoxRect(0, w, h)

	a.ctx.mouseX, a.ctx.mouseY = dr.X+5, dr.Y+5
	if !a.boxFencesPointer(w, h) {
		t.Error("a torn-off box must keep fencing when the main box is closed")
	}
	// Where the (closed) main box would sit must NOT fence.
	mainR := a.extrasBoxRect(w, h)
	a.ctx.mouseX, a.ctx.mouseY = mainR.X+5, mainR.Y+5
	if pointIn(a.ctx.mouseX, a.ctx.mouseY, dr) {
		t.Skip("main box overlaps the torn-off box in this layout")
	}
	if a.boxFencesPointer(w, h) {
		t.Error("a closed main box must not fence")
	}
}

// TestExtrasResize pins corner-resize: a press on the bottom-right grip starts a
// resize that grows W/H with the drag, a far-inward drag clamps at the minimum,
// and a release ends it.
func TestExtrasResize(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	a.showWidgets = true
	const w, h = int32(1280), int32(720)
	r := a.extrasBoxRect(w, h)
	grip := sdl.Rect{X: r.X + r.W - extrasGripSz, Y: r.Y + r.H - extrasGripSz, W: extrasGripSz, H: extrasGripSz}

	a.ctx.mouseDown = true
	a.ctx.mouseX, a.ctx.mouseY = grip.X+4, grip.Y+4
	pressed := true
	a.handleExtrasResize(grip, r, w, h, &pressed)
	if !a.extrasResizing || pressed {
		t.Fatal("a press on the grip must start a resize and consume the edge")
	}

	a.ctx.mouseX += 120 // drag the corner out by +120,+90
	a.ctx.mouseY += 90
	pressed = false
	a.handleExtrasResize(grip, r, w, h, &pressed)
	if got := a.extrasBoxRect(w, h); got.W != r.W+120 || got.H != r.H+90 {
		t.Errorf("resized to %dx%d, want %dx%d", got.W, got.H, r.W+120, r.H+90)
	}

	a.ctx.mouseX, a.ctx.mouseY = r.X-500, r.Y-500 // far inward → floor
	a.handleExtrasResize(grip, r, w, h, &pressed)
	if min := a.extrasBoxRect(w, h); min.W != extrasMinW || min.H != extrasMinH {
		t.Errorf("over-shrunk to %dx%d, want the floor %dx%d", min.W, min.H, extrasMinW, extrasMinH)
	}

	a.ctx.mouseDown = false
	a.handleExtrasResize(grip, r, w, h, &pressed)
	if a.extrasResizing {
		t.Error("release must end the resize")
	}
}

// TestDetachedResize pins corner-resize on a torn-off box: a grip press starts a
// resize that grows the box with the drag, a far-inward drag clamps at the
// minimum, and release ends it.
func TestDetachedResize(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	const w, h = int32(1280), int32(720)
	a.extrasDetached = []detachedWidget{{id: 1, x: 500, y: 300}}
	r := a.detachedBoxRect(0, w, h)
	grip := sdl.Rect{X: r.X + r.W - detachedGripSz, Y: r.Y + r.H - detachedGripSz, W: detachedGripSz, H: detachedGripSz}

	a.ctx.mouseDown = true
	a.ctx.mouseX, a.ctx.mouseY = grip.X+3, grip.Y+3
	pressed := true
	a.handleDetachedResize(0, grip, r, w, h, &pressed)
	if !a.extrasDetachResizing || pressed {
		t.Fatal("a press on the grip must start a resize and consume the edge")
	}

	a.ctx.mouseX += 60 // drag the corner out by +60,+40
	a.ctx.mouseY += 40
	pressed = false
	a.handleDetachedResize(0, grip, r, w, h, &pressed)
	if got := a.detachedBoxRect(0, w, h); got.W != r.W+60 || got.H != r.H+40 {
		t.Errorf("resized to %dx%d, want %dx%d", got.W, got.H, r.W+60, r.H+40)
	}

	a.ctx.mouseX, a.ctx.mouseY = r.X-500, r.Y-500 // far inward → floor
	a.handleDetachedResize(0, grip, r, w, h, &pressed)
	if got := a.detachedBoxRect(0, w, h); got.W != detachedMinW || got.H != detachedMinH {
		t.Errorf("over-shrunk to %dx%d, want the floor %dx%d", got.W, got.H, detachedMinW, detachedMinH)
	}

	a.ctx.mouseDown = false
	a.handleDetachedResize(0, grip, r, w, h, &pressed)
	if a.extrasDetachResizing {
		t.Error("release must end the resize")
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

// TestTornWidgetPersistence pins the torn-off-widget survival cycle (FIX 1): a
// persisted torn slot re-tears the widget at its saved rect on the next courtroom
// entry, reattaching clears the slot, and reconstruction ignores unknown / already-
// torn ids without unbounding the set.
func TestTornWidgetPersistence(t *testing.T) {
	a := testTabApp(t)
	const w, h = int32(1280), int32(720)
	id := 3 // a stable, in-range extrasWidgets() index
	if id >= len(a.extrasWidgets()) {
		t.Fatalf("test id %d out of range (%d widgets)", id, len(a.extrasWidgets()))
	}

	// Tear it out and persist on the gesture end.
	a.extrasDetached = []detachedWidget{{id: id, x: 600, y: 400}}
	want := a.detachedBoxRect(0, w, h)
	a.persistTornWidgetSlot(0, w, h)
	if _, ok := a.classicOv[tornWidgetSlotKey(id)]; !ok {
		t.Fatalf("persist did not write the torn slot into classicOv")
	}
	if _, ok := a.d.Prefs.ClassicLayoutOverrides()[tornWidgetSlotKey(id)]; !ok {
		t.Fatalf("persist did not write the torn slot to prefs")
	}

	// Simulate a relaunch: drop the live boxes, re-arm the latch, reconstruct from
	// the persisted overrides. The widget must re-tear at (approximately) its saved
	// rect (the frac→px round-trip is lossy to the pixel — asserted with tolerance).
	a.extrasDetached = nil
	a.extrasTornRebuilt = false
	a.reconstructTornWidgets(w, h)
	if len(a.extrasDetached) != 1 || a.extrasDetached[0].id != id {
		t.Fatalf("reconstruct = %v, want one box for id %d", a.extrasDetached, id)
	}
	// The frac→px round-trip is resolution-independent but lossy to the pixel (like
	// every classic slot), so allow ±1px rather than demanding an exact rect.
	if got := a.detachedBoxRect(0, w, h); abs32(got.X-want.X) > 1 || abs32(got.Y-want.Y) > 1 ||
		abs32(got.W-want.W) > 1 || abs32(got.H-want.H) > 1 {
		t.Errorf("re-torn box = %+v, want ≈ %+v", got, want)
	}

	// The latch is one-shot: a second call must not double-append.
	a.reconstructTornWidgets(w, h)
	if len(a.extrasDetached) != 1 {
		t.Fatalf("second reconstruct double-appended: %v", a.extrasDetached)
	}

	// Reattach clears the slot everywhere (pref + live map) so it won't re-tear.
	a.reattachWidget(0)
	if _, ok := a.classicOv[tornWidgetSlotKey(id)]; ok {
		t.Fatalf("reattach left the slot in classicOv")
	}
	if _, ok := a.d.Prefs.ClassicLayoutOverrides()[tornWidgetSlotKey(id)]; ok {
		t.Fatalf("reattach left the slot in prefs")
	}
	a.extrasTornRebuilt = false
	a.reconstructTornWidgets(w, h)
	if len(a.extrasDetached) != 0 {
		t.Fatalf("a cleared slot must not re-tear, got %v", a.extrasDetached)
	}

	// Unknown / malformed ids are IGNORED (a newer build's widget), not re-torn and
	// not deleted — the set stays bounded to known widgets.
	a.classicOv[tornWidgetSlotKey(9999)] = [4]float64{0.1, 0.1, 0.1, 0.1}
	a.classicOv[tornWidgetSlotPrefix+"junk"] = [4]float64{0.1, 0.1, 0.1, 0.1}
	a.extrasTornRebuilt = false
	a.reconstructTornWidgets(w, h)
	if len(a.extrasDetached) != 0 {
		t.Fatalf("unknown/malformed torn ids must be ignored, got %v", a.extrasDetached)
	}
}
