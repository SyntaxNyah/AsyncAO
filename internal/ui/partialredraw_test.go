package ui

import (
	"image"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// stageAnimFrameDelay is the test background's frame time.
	stageAnimFrameDelay = 100 * time.Millisecond
	// partialMessageText is long enough to still be typing a few frames after it arrives.
	partialMessageText = "a message long enough that the typewriter is still revealing it several frames later"
	// partialMessageFrames bounds the frames driven while waiting for the message to start typing.
	partialMessageFrames = 60
)

// contains reports whether outer holds inner.
func contains(outer, inner sdl.Rect) bool {
	return inner.X >= outer.X && inner.Y >= outer.Y && inner.X+inner.W <= outer.X+outer.W &&
		inner.Y+inner.H <= outer.Y+outer.H
}

// TestPartialRedraw pins when a frame may redraw one region alone: only when the stage's animation or a playing message
// is the one reason to draw, and then over the stage, the chatbox while a message plays and the F8 panel while it is
// open, where the last frame drew them. Input or pending UI changes still draw everything, and a still stage nothing.
func TestPartialRedraw(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	const focused, noEvent, sawEvent = true, false, true

	driveFrames(a, 1)
	a.warnLine = "" // the first themed frame's hint draws every frame while it shows
	driveFrames(a, 1)
	if _, ok := a.PartialRedraw(focused, noEvent); ok {
		t.Fatal("a still stage asked for a partial frame; it should skip")
	}
	if !a.SkipFrame(focused, noEvent) {
		t.Fatal("a settled courtroom with a still stage did not skip")
	}

	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: "animated"})
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	animated := &assets.Decoded{Width: 2, Height: 2, Frames: []*image.RGBA{frame, frame},
		Delays: []time.Duration{stageAnimFrameDelay, stageAnimFrameDelay}}
	if err := a.d.Store.Upload(a.room.Scene.BackgroundBase, animated); err != nil {
		t.Fatalf("upload animated background: %v", err)
	}
	driveFrames(a, 2) // the upload is damage; the second frame sees the stage where the first drew it

	region, ok := a.PartialRedraw(focused, noEvent)
	if !ok {
		t.Fatal("an animated background with nothing else to draw did not ask for a partial frame")
	}
	if region != a.stageRect || region.W <= 0 || region.H <= 0 {
		t.Errorf("partial region = %+v, want the drawn stage %+v", region, a.stageRect)
	}
	if a.SkipFrame(focused, noEvent) {
		t.Error("an animated background skipped its frame")
	}
	if _, ok := a.PartialRedraw(focused, sawEvent); ok {
		t.Error("a pass with input asked for a partial frame")
	}
	a.uiDirty = true
	if _, ok := a.PartialRedraw(focused, noEvent); ok {
		t.Error("pending UI changes asked for a partial frame")
	}

	a.showDebugPanel = true
	driveFrames(a, 1)
	region, ok = a.PartialRedraw(focused, noEvent)
	if !ok || !contains(region, a.stageRect) || !contains(region, a.debugRect) {
		t.Errorf("with the F8 panel open the partial region = %+v (ok %v), want the stage %+v and the panel %+v",
			region, ok, a.stageRect, a.debugRect)
	}
	a.showDebugPanel = false
	driveFrames(a, 1)

	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(0, "Witch", partialMessageText)})
	for i := 0; i < partialMessageFrames && !(a.roomBusy() && a.chatRectOK && !a.RenderNeeded()); i++ {
		driveFrame(a)
	}
	if !a.roomBusy() || !a.chatRectOK {
		t.Fatalf("the message never started typing with a chatbox (busy %v, chatbox %v)", a.roomBusy(), a.chatRectOK)
	}
	region, ok = a.PartialRedraw(focused, noEvent)
	if !ok || !contains(region, a.stageRect) || !contains(region, a.chatRect) {
		t.Errorf("a typing message's partial region = %+v (ok %v), want the stage %+v and the chatbox %+v",
			region, ok, a.stageRect, a.chatRect)
	}

	// A pre-animation or shout hides the chatbox mid-message; where it drew must stay in the region.
	shown := a.chatRect
	a.room.Scene.IsBlankPost = true
	driveFrame(a)
	region, ok = a.PartialRedraw(focused, noEvent)
	if !a.roomBusy() {
		t.Fatal("the message finished before the hidden-chatbox frame")
	}
	if !ok || !contains(region, shown) {
		t.Errorf("with the chatbox hidden mid-message the partial region = %+v (ok %v), want it to keep %+v",
			region, ok, shown)
	}

	a.NoteRedraw(true, focused, noEvent)
	a.NoteRedraw(false, focused, sawEvent)
	const want = "Redraws, last 2: 1 partial · 1 full — input 1" // the ring's empty slots count for nothing
	if got := a.RedrawSummary(); got != want {
		t.Errorf("redraw summary = %q, want %q", got, want)
	}
}
