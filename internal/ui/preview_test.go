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
