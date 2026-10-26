package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Issues #37 and #36, both reported against the sprite-preview box.
//
// #37 "Buttons over other ui elements do not block mouse input behind them —
// I keep accidentally playing a song": the hover-preview box floats over the
// right-hand column, so the music row it covers took the same click that
// dismissed the box, and the track started playing. The kit is immediate-mode
// and single-pass, so the row (drawn far earlier) had no way to know the box
// was coming — that is what the overlay-fence registry is for.
//
// #36 "Window Dragging does not prevent other mouse inputs": dragging the box
// across the IC log ALSO drag-selected the log text underneath, because
// handleLogSelect keeps a live selection alive on a bare c.mouseDown that
// nothing had claimed.

// previewTestBox is the box footprint these tests place, and previewTestRow is a
// music-row-shaped widget drawn EARLY in the same pass that the box lands on top
// of. The row extends to the LEFT of the box so the tests can prove the fence is
// rect-scoped rather than a modalOn-style blanket.
var (
	previewTestBox = sdl.Rect{X: 900, Y: 300, W: 260, H: 300}
	previewTestRow = sdl.Rect{X: 700, Y: 320, W: 400, H: 24}
)

// previewFenceApp is a live courtroom with a preview box already drawn once (the
// box rect is last frame's latch — the same one handlePreviewInput has always
// hit-tested against).
func previewFenceApp(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	a.previewBase = "characters/Phoenix/(a)normal"
	a.previewFrameRect = previewTestBox
	return a
}

// TestPreviewFenceBlocksRowBeneath is #37's core contract: once the box's
// footprint is published, the music row it covers neither hovers nor fires, while
// the part of the row sticking out from under the box stays live.
func TestPreviewFenceBlocksRowBeneath(t *testing.T) {
	a := previewFenceApp(t)
	c := a.ctx
	// Cursor over the overlap: inside both the row and the box.
	c.mouseX, c.mouseY = previewTestBox.X+20, previewTestRow.Y+5
	c.clicked = true
	c.downX, c.downY = c.mouseX, c.mouseY // ClickedIn also needs the press origin

	// Baseline — this is the bug as reported.
	if !c.hovering(previewTestRow) || !c.ClickedIn(previewTestRow) {
		t.Fatal("test setup: the cursor must start over the buried music row")
	}

	a.fenceSpritePreview()
	if !a.previewFence.on {
		t.Fatal("a preview drawn last frame must publish its footprint")
	}
	if c.hovering(previewTestRow) {
		t.Error("a music row under the preview box must not hover (#37)")
	}
	if c.ClickedIn(previewTestRow) {
		t.Error("a music row under the preview box must not fire — this is the track that played (#37)")
	}

	// The box owns its own pixels: its − / + zoom buttons and pinned × must survive
	// the fence it just published, or the fix trades one dead click for another.
	prev := c.pushOverlayOwner(a.previewOwnerMark())
	if !c.hovering(previewTestBox) {
		t.Error("inside pushOverlayOwner the box must hit-test its OWN widgets")
	}
	c.popOverlayOwner(prev)

	// Rect-scoped, not a blanket: the exposed left end of the row is still clickable.
	c.mouseX, c.mouseY = previewTestRow.X+5, previewTestRow.Y+5
	c.downX, c.downY = c.mouseX, c.mouseY
	if !c.hovering(previewTestRow) {
		t.Error("the row where the box does NOT cover it must stay live — a fence covers its rect, not the frame")
	}
}

// TestPreviewFencePublishesNothingWhenNoBox pins the other half of the
// draw-vs-fence agreement rule: a fence over pixels nothing painted eats clicks,
// which is the mirror image of the leak being fixed. It also pins the mark
// fallback — with nothing published, the box's owner-suspend must suspend NOTHING,
// or a preview drawn on a pass that never fences (char select, the wardrobe) would
// become immune to the menu bar's open pane above it.
func TestPreviewFencePublishesNothingWhenNoBox(t *testing.T) {
	for _, tc := range []struct {
		name string
		// Takes the subtest's OWN app: a closure over an app built out here would
		// mutate the wrong one and every case would silently pass.
		apply func(*App)
	}{
		{"no preview up", func(a *App) { a.previewBase = "" }},
		{"nothing drawn last frame", func(a *App) { a.previewFrameRect = sdl.Rect{} }},
		{"zero-height rect", func(a *App) { a.previewFrameRect = sdl.Rect{X: 900, Y: 300, W: 260} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := previewFenceApp(t)
			c := a.ctx
			c.mouseX, c.mouseY = previewTestBox.X+20, previewTestRow.Y+5
			tc.apply(a)
			a.fenceSpritePreview()
			if a.previewFence.on {
				t.Error("nothing is painted there — publishing a fence would eat live clicks")
			}
			if !c.hovering(previewTestRow) {
				t.Error("the row must stay live when no box covers it")
			}
		})
	}

	// The unpublished mark must not suspend an EARLIER publisher's fence.
	a := previewFenceApp(t)
	c := a.ctx
	a.previewBase = ""
	someoneElse := sdl.Rect{X: 0, Y: 0, W: 1280, H: 22} // stand-in for the menu bar strip
	c.fenceOverlay(someoneElse)
	a.fenceSpritePreview()
	prev := c.pushOverlayOwner(a.previewOwnerMark())
	c.mouseX, c.mouseY = 100, 10
	if c.hovering(someoneElse) {
		t.Error("a box that published nothing must still yield to what is painted above it")
	}
	c.popOverlayOwner(prev)
}

// TestPreviewClickIsConsumed pins the second half of #37: a press-release on the
// box body must not survive as a click for whatever the box floats over. The box
// still dismisses on that click, which is the behaviour the consume replaces.
func TestPreviewClickIsConsumed(t *testing.T) {
	a := previewFenceApp(t)
	c := a.ctx
	inBody := func() { c.mouseX, c.mouseY = previewTestBox.X+20, previewTestBox.Y+20 }

	inBody()
	c.mouseDown = true
	a.handlePreviewInput() // press frame: the box claims the gesture
	if !a.previewDrag {
		t.Fatal("a press on the box body must claim the press")
	}

	inBody()
	c.mouseDown, c.clicked = false, true
	a.handlePreviewInput() // release frame, same pixel: no movement
	if c.clicked {
		t.Error("a press-release on the preview box must be consumed — unconsumed it played the track underneath (#37)")
	}
	if a.previewBase != "" {
		t.Error("a plain click on the box must still dismiss it (the pre-fix behaviour the consume replaces)")
	}
	if a.previewDrag {
		t.Error("the gesture must end on release")
	}
}

// TestPreviewPinnedClickIsConsumedButKeepsBox pins that pinning changes only the
// dismissal: a pinned box is closed by its ×, never by a body click — but the
// click is still consumed, or the pinned box would leak every press it swallows.
func TestPreviewPinnedClickIsConsumedButKeepsBox(t *testing.T) {
	a := previewFenceApp(t)
	c := a.ctx
	a.previewPinned = true
	// Below the × (top-right, 18px) and above the zoom strip: plain body.
	c.mouseX, c.mouseY = previewTestBox.X+20, previewTestBox.Y+60

	c.mouseDown = true
	a.handlePreviewInput()
	c.mouseDown, c.clicked = false, true
	a.handlePreviewInput()

	if c.clicked {
		t.Error("a pinned box must consume its own body click too")
	}
	if a.previewBase == "" {
		t.Error("a pinned box closes on its × only, not on a body click")
	}
}

// TestPreviewDragFencesTheCourtroom is #36: while the box is being dragged or
// resized the whole courtroom pass runs pointer-blind, which is what ends the IC
// log's drag-selection (handleLogSelect keeps it alive on a bare c.mouseDown).
// A merely HOVERED box must not fence — it paints from inside the courtroom pass,
// so a footprint fence would kill its own − / + zoom buttons.
func TestPreviewDragFencesTheCourtroom(t *testing.T) {
	a := previewFenceApp(t)
	const w, h = int32(1280), int32(720)
	a.ctx.mouseX, a.ctx.mouseY = previewTestBox.X+20, previewTestBox.Y+20

	if a.boxFencesPointer(w, h) {
		t.Error("a box merely under the cursor must NOT fence the pass — its own zoom buttons draw inside it")
	}
	a.previewDrag = true
	if !a.boxFencesPointer(w, h) {
		t.Error("a drag in flight must fence the pass — without it the drag also selected the IC log (#36)")
	}
	a.previewDrag = false
	a.previewResize = true
	if !a.boxFencesPointer(w, h) {
		t.Error("a resize in flight must fence the pass too")
	}
}

// TestPreviewSurvivesItsOwnDrag guards the trap the #36 fence opens: the fenced
// pass blanks the pointer, so close-on-leave sees the cursor over neither the
// trigger nor the box and would close the box on the gesture's first frame.
func TestPreviewSurvivesItsOwnDrag(t *testing.T) {
	a := previewFenceApp(t)
	c := a.ctx
	a.previewEntered = true
	a.previewDrag = true
	c.mouseX, c.mouseY = -1, -1 // exactly what fencePointer leaves behind
	c.hoverID = ""

	a.closeSpritePreviewOnLeave()
	if a.previewBase == "" {
		t.Fatal("a box being dragged must not close under its own pointer fence (#36)")
	}
}
