package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

func TestUnionRect(t *testing.T) {
	a := sdl.Rect{X: 10, Y: 20, W: 30, H: 40}   // x[10,40) y[20,60)
	b := sdl.Rect{X: 100, Y: 200, W: 50, H: 60} // x[100,150) y[200,260)
	if got, want := unionRect(a, b), (sdl.Rect{X: 10, Y: 20, W: 140, H: 240}); got != want {
		t.Fatalf("unionRect = %+v, want %+v", got, want)
	}
	// A zero-area rect contributes nothing (so a preview with no captured trigger
	// still gets a sane corridor = the box alone).
	if got := unionRect(sdl.Rect{}, b); got != b {
		t.Fatalf("unionRect(zero,b) = %+v, want %+v", got, b)
	}
	if got := unionRect(a, sdl.Rect{}); got != a {
		t.Fatalf("unionRect(a,zero) = %+v, want %+v", got, a)
	}
}

// TestSpritePreviewTravelCorridor pins the hover-preview close logic the beta surfaced:
// the box must survive the cursor travelling from the trigger cell to the bottom-right
// box, vanish if the cursor strays off that path, and close promptly once the cursor has
// reached the box and then left it.
func TestSpritePreviewTravelCorridor(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	trigger := sdl.Rect{X: 100, Y: 100, W: 80, H: 80}
	box := sdl.Rect{X: 600, Y: 500, W: 200, H: 200}
	// corridor = unionRect(trigger, box) = x[100,800) y[100,700)
	open := func() {
		a.previewBase, a.previewEntered = "x", false
		a.previewFrameRect, a.previewTriggerRect = box, trigger
		c.clicked, c.hoverID = false, ""
	}

	// On the trigger → stays up.
	open()
	c.hoverID, c.hoverRect = "char:x", trigger
	c.mouseX, c.mouseY = trigger.X+5, trigger.Y+5
	a.closeSpritePreviewOnLeave()
	if a.previewBase == "" {
		t.Fatal("cursor on the trigger: preview must stay open")
	}

	// In the gap between trigger and box (over neither) → stays up to travel.
	c.hoverID = ""
	c.mouseX, c.mouseY = 350, 300
	a.closeSpritePreviewOnLeave()
	if a.previewBase == "" {
		t.Fatal("cursor in the travel corridor: preview must stay open")
	}

	// Off the corridor (moved away) → closes.
	c.mouseX, c.mouseY = 900, 40
	a.closeSpritePreviewOnLeave()
	if a.previewBase != "" {
		t.Fatal("cursor strayed off the travel path: preview must close")
	}

	// Reached the box → entered latches; leaving the box then closes even though the
	// cursor is back inside the corridor.
	open()
	c.hoverID = ""
	c.mouseX, c.mouseY = box.X+10, box.Y+10
	a.closeSpritePreviewOnLeave()
	if a.previewBase == "" || !a.previewEntered {
		t.Fatal("cursor in the box: must stay open and mark entered")
	}
	c.mouseX, c.mouseY = 350, 300 // in the corridor, but the box was already entered
	a.closeSpritePreviewOnLeave()
	if a.previewBase != "" {
		t.Fatal("after entering the box, leaving it must close (not held by the corridor)")
	}

	// A click always dismisses (a selection commits).
	open()
	c.hoverID, c.hoverRect = "char:x", trigger
	c.mouseX, c.mouseY = trigger.X+5, trigger.Y+5
	c.clicked = true
	a.closeSpritePreviewOnLeave()
	if a.previewBase != "" {
		t.Fatal("a click must dismiss the preview")
	}
}

// TestSpritePreviewStaleTriggerCloses pins the orphaned-trigger half of the
// frame-pacer cap-latch report: hoverID is cleared only by its own trigger's
// HoverPreview call, so once that trigger stops being drawn (drawer closed,
// emote page flipped, screen switched) the id lingers. Trusting the bare id
// pinned "over trigger" true forever and the box could never leave-close —
// close-on-leave must demand the pointer actually be ON the remembered rect.
func TestSpritePreviewStaleTriggerCloses(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	trigger := sdl.Rect{X: 100, Y: 100, W: 80, H: 80}
	box := sdl.Rect{X: 600, Y: 500, W: 200, H: 200}
	a.previewBase, a.previewEntered = "x", false
	a.previewFrameRect, a.previewTriggerRect = box, trigger
	c.hoverID, c.hoverRect = "char:x", trigger // stale: the trigger's screen is gone
	c.mouseX, c.mouseY = 900, 40               // off the trigger, the box, and the corridor
	a.closeSpritePreviewOnLeave()
	if a.previewBase != "" {
		t.Fatal("a stale trigger id must not pin the preview open (the cap-latch bug)")
	}
}

// TestCloseSpritePreviewDisarmsDwell pins the click-commit half: a close while
// the pointer still rests on the trigger must clear the trigger id, or the
// already-elapsed dwell re-opens the box on the very next frame — the silent
// re-arm that carried a "closed" preview across a char pick into the courtroom.
func TestCloseSpritePreviewDisarmsDwell(t *testing.T) {
	a := testTabApp(t)
	a.previewBase = "x"
	a.ctx.hoverID = "char:x"
	a.closeSpritePreview()
	if a.previewBase != "" || a.ctx.hoverID != "" {
		t.Fatal("closeSpritePreview must clear the trigger id (no instant dwell re-open)")
	}
}

// TestScreenSwitchDropsOrphanPreview pins noteScreenTransition: a screen switch
// with a preview still up (pinned or not) must drop the preview, its trigger id,
// and both cached rects — with every close path living in per-screen draw tails,
// a switched-away preview has no owner: it held the event-driven loop at the
// ACTIVE cap and its ghost rect kept claiming wheel/press on the new screen.
func TestScreenSwitchDropsOrphanPreview(t *testing.T) {
	a := testTabApp(t)
	a.screen = ScreenCharSelect
	a.noteScreenTransition() // absorb the initial lobby→charselect flip
	a.previewBase, a.previewPinned = "x", true
	a.ctx.hoverID, a.ctx.hoverRect = "char:x", sdl.Rect{X: 1, Y: 1, W: 10, H: 10}
	a.previewFrameRect = sdl.Rect{X: 5, Y: 5, W: 50, H: 50}
	a.previewTriggerRect = sdl.Rect{X: 1, Y: 1, W: 10, H: 10}

	a.screen = ScreenCourtroom
	a.noteScreenTransition()
	if a.previewBase != "" || a.previewPinned || a.ctx.hoverID != "" {
		t.Fatal("a screen switch must drop the orphaned preview + pin + trigger id")
	}
	if a.previewFrameRect.W != 0 || a.previewTriggerRect.W != 0 {
		t.Fatal("a screen switch must zero the cached box/trigger rects (ghost input claim)")
	}

	// Same screen: a live preview stays untouched.
	a.previewBase = "y"
	a.noteScreenTransition()
	if a.previewBase == "" {
		t.Fatal("no switch → the live preview must stay")
	}
}

// TestHoverPreviewToggleGatesOnlyDwell pins the playtest regression: turning
// hover-previews OFF must disable ONLY the dwell pop-up — an explicit
// right-click on a trigger still opens the preview.
func TestHoverPreviewToggleGatesOnlyDwell(t *testing.T) {
	trigger := sdl.Rect{X: 10, Y: 10, W: 50, H: 50}
	c := &Ctx{mouseX: 20, mouseY: 20}
	c.SetHoverPreview(false, 0) // previews toggle OFF

	if c.HoverPreview("emote:a", trigger) {
		t.Fatal("hover with the toggle off must never open a preview")
	}
	c.rightClicked = true
	if !c.HoverPreview("emote:a", trigger) {
		t.Fatal("right-click must open the preview even with the toggle off")
	}
	if c.hoverID != "emote:a" {
		t.Fatal("the right-click open must register the trigger (close-on-leave contract)")
	}
	// Subsequent frames (no right-click) keep the trigger alive while hovered…
	c.rightClicked = false
	if c.HoverPreview("emote:a", trigger) {
		t.Fatal("no dwell may start while the toggle is off")
	}
	if c.hoverID != "emote:a" {
		t.Fatal("an open right-click preview's trigger must stay registered while hovered")
	}
	// …and clear the moment the cursor leaves the trigger.
	c.mouseX, c.mouseY = 500, 500
	if c.HoverPreview("emote:a", trigger) {
		t.Fatal("off-trigger must not preview")
	}
	if c.hoverID != "" {
		t.Fatal("leaving the trigger must clear its id")
	}

	// Toggle ON: the dwell path arms (first frame registers, no instant open).
	c.SetHoverPreview(true, 0)
	c.mouseX, c.mouseY = 20, 20
	if c.HoverPreview("emote:a", trigger) {
		t.Fatal("the first hovered frame only arms the dwell")
	}
	if !c.HoverPreview("emote:a", trigger) {
		t.Fatal("with a zero dwell, the second frame must open")
	}
}
