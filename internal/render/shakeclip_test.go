package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestShakeClipsTheBackgroundToTheStage pins #80: the background fill rides the
// SHAKEN viewport rect, so without clipping a screenshake spills it past the stage
// boundary and over the UI beside the stage. Render must clip it (and the desk /
// flash) to the UN-shaken stage.
func TestShakeClipsTheBackgroundToTheStage(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	if err := store.Upload("bg", decodedFixture()); err != nil {
		t.Fatal(err)
	}
	ct, err := NewCaptureTarget(ren, 512, 384)
	if err != nil {
		t.Skipf("capture target unavailable headlessly: %v", err)
	}
	defer ct.Close()

	vp := NewViewport(store)
	scene := &courtroom.Scene{}
	scene.BackgroundBase = "bg"
	scene.ShakeLeft = courtroom.ScreenshakeDuration // full-strength shake

	stage := sdl.Rect{X: 128, Y: 0, W: 256, H: 384}
	// At full strength amp = vp.W/48 ≈ 5.3 px; the sinusoid shifts the bg LEFT by
	// ~3 px, so X=126 is inside the UN-clipped fill but LEFT of the stage. X=140 is
	// safely inside the stage.
	const leftX, inX, y = 126, 140, 192

	img, err := ct.Capture(ren, func(sdl.Rect) {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, stage)
	})
	if err != nil {
		t.Skipf("ReadPixels unavailable headlessly: %v", err)
	}
	if img.RGBAAt(inX, y).R == 0 {
		t.Fatal("the on-stage background did not draw (test setup wrong)")
	}
	if img.RGBAAt(leftX, y).R > 0 {
		t.Errorf("the shaken background spilled LEFT of the stage — it must be clipped to the stage")
	}
}
