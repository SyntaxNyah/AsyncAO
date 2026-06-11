package ui

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestInputSnapshotOrder pins the Ctx frame contract main.go's loop relies
// on: BeginFrame opens (and clears) the input snapshot, HandleEvent then
// fills it, and widgets read it during the draw pass. The inverse order
// shipped once — events fed before BeginFrame — and the reset erased every
// click/keypress/wheel tick before any widget saw it, leaving the whole UI
// dead. No SDL init is needed: events are plain structs and GetMouseState
// reads SDL's static mouse state.
func TestInputSnapshotOrder(t *testing.T) {
	c := &Ctx{}

	c.BeginFrame(time.Millisecond)
	c.HandleEvent(&sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, X: 5, Y: 6})
	if c.mouseX != 5 || c.mouseY != 6 {
		t.Fatalf("motion event must refresh the cursor snapshot, got (%d,%d)", c.mouseX, c.mouseY)
	}
	c.HandleEvent(&sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONUP, Button: sdl.BUTTON_LEFT, X: 40, Y: 20})
	if !c.clicked {
		t.Fatal("MOUSEBUTTONUP after BeginFrame must leave clicked set for the draw pass")
	}
	if c.mouseX != 40 || c.mouseY != 20 {
		t.Fatalf("button event must move the cursor snapshot to the release point, got (%d,%d)", c.mouseX, c.mouseY)
	}
	if !c.hovering(sdl.Rect{X: 30, Y: 10, W: 20, H: 20}) {
		t.Fatal("hovering must hit-test against the release coordinates")
	}
	c.HandleEvent(&sdl.MouseWheelEvent{Type: sdl.MOUSEWHEEL, Y: 3})
	if c.wheelY != 3 {
		t.Fatalf("wheel ticks must accumulate, got %d", c.wheelY)
	}

	// The next BeginFrame consumes the frame's input.
	c.BeginFrame(time.Millisecond)
	if c.clicked || c.wheelY != 0 {
		t.Fatal("BeginFrame must clear the previous frame's input snapshot")
	}
}
