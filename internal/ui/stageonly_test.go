package ui

import (
	"image"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// stageAnimFrameDelay is the test background's frame time.
const stageAnimFrameDelay = 100 * time.Millisecond

// TestStageOnlyFrames pins when a frame may redraw the stage alone: only when the stage's animation is the one reason
// to draw, and then at the rect the last frame drew the stage at. Input or pending UI changes still draw everything,
// and a still stage draws nothing.
func TestStageOnlyFrames(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	const focused, noEvent, sawEvent = true, false, true

	driveFrames(a, 1)
	a.warnLine = "" // the first themed frame's hint draws every frame while it shows
	driveFrames(a, 1)
	if _, ok := a.StageOnly(focused, noEvent); ok {
		t.Fatal("a still stage asked for a stage-only frame; it should skip")
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

	stage, ok := a.StageOnly(focused, noEvent)
	if !ok {
		t.Fatal("an animated background with nothing else to draw did not ask for a stage-only frame")
	}
	if stage.W <= 0 || stage.H <= 0 || stage != a.stageRect {
		t.Errorf("stage-only rect = %+v, want the drawn stage %+v", stage, a.stageRect)
	}
	if a.SkipFrame(focused, noEvent) {
		t.Error("an animated background skipped its frame")
	}
	if _, ok := a.StageOnly(focused, sawEvent); ok {
		t.Error("a pass with input asked for a stage-only frame")
	}
	a.NoteRedraw(true, focused, noEvent)
	a.NoteRedraw(false, focused, sawEvent)
	a.NoteRedraw(false, focused, noEvent) // the stage was due, but a full frame drew it
	if got := a.redrawSummary(); !strings.Contains(got, "1 stage only") || !strings.Contains(got, "input 1") ||
		!strings.Contains(got, "stage, drawn full 1") {
		t.Errorf("redraw summary = %q, want one stage-only frame, one for input and one stage drawn in full", got)
	}
	a.uiDirty = true
	if _, ok := a.StageOnly(focused, noEvent); ok {
		t.Error("pending UI changes asked for a stage-only frame")
	}
}
