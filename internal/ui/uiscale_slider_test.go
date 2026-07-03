package ui

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestManualUIScaleCommitOnRelease drives the manual UI-scale slider through a
// real press → drag → release frame sequence (BeginFrame → events → draw, the
// main-loop order) and pins the commit-on-release contract: the value PREVIEWS
// during the drag (no mid-drag rescale — that's the feedback loop the control
// exists to avoid) and applies to uiScalePct + the prefs exactly once, on the
// release frame (the playtest report was "snaps back to 100% immediately").
func TestManualUIScaleCommitOnRelease(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	a := testTabApp(t)
	a.ctx.Ren = ren
	a.d.Prefs.SetUIScaleAuto(false)
	a.uiScalePct = 100
	a.ctx.SetUIScale(100)

	c := a.ctx
	const rowY = int32(40)
	// sliderRow's track geometry with formX=0: {X: 130, Y: rowY+5, W: 90, H: 16}.
	a.formX = 0
	frame := func(evs ...sdl.Event) {
		c.BeginFrame(16 * time.Millisecond)
		for _, ev := range evs {
			c.HandleEvent(ev)
		}
		a.drawManualUIScale(rowY)
	}

	// Press mid-track (mouse-down jumps the thumb, arming the drag)…
	frame(
		&sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, X: 175, Y: rowY + 12},
		&sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONDOWN, Button: sdl.BUTTON_LEFT, X: 175, Y: rowY + 12},
	)
	if !a.uiScaleDragging {
		t.Fatal("press on the track must arm the commit-on-release drag")
	}
	if a.uiScalePct != 100 {
		t.Fatalf("mid-drag must NOT rescale (feedback loop), uiScalePct = %d", a.uiScalePct)
	}

	// …drag to the far right end (200%)…
	frame(&sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, X: 130 + 90, Y: rowY + 12})
	if a.uiScalePct != 100 {
		t.Fatalf("still dragging: must not apply yet, uiScalePct = %d", a.uiScalePct)
	}
	if a.uiScalePending != config.MaxUIScalePercent {
		t.Fatalf("drag preview = %d, want %d (thumb at the right end)", a.uiScalePending, config.MaxUIScalePercent)
	}

	// …release: the pending value commits to the App, the kit and the prefs.
	frame(&sdl.MouseButtonEvent{Type: sdl.MOUSEBUTTONUP, Button: sdl.BUTTON_LEFT, X: 130 + 90, Y: rowY + 12})
	if a.uiScaleDragging {
		t.Error("release must disarm the drag")
	}
	if a.uiScalePct != config.MaxUIScalePercent {
		t.Fatalf("release must apply the dragged value, uiScalePct = %d, want %d", a.uiScalePct, config.MaxUIScalePercent)
	}
	if got := a.d.Prefs.UIScale(); got != config.MaxUIScalePercent {
		t.Fatalf("release must persist the scale, prefs = %d", got)
	}

	// One quiet frame after the commit: nothing re-applies or snaps back.
	frame(&sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, X: 20, Y: 200})
	if a.uiScalePct != config.MaxUIScalePercent {
		t.Fatalf("post-commit frame must hold the value, uiScalePct = %d", a.uiScalePct)
	}
}
