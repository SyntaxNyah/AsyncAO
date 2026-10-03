package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestViewportRenderDefersToAnActiveClip pins #138: the hyperfocus camera zoom
// (internal/ui/vpzoom.go) renders the scene into an EXPANDED destination rect
// under a clip rect set to the ORIGINAL stage, and relies on that clip to keep
// the magnified scene inside the viewport. Render's own full-viewport-fill
// containment (background / desk / flash) must NOT stomp that clip — it must
// defer like the sprite mask, overlays, reflection, splash and particles all do
// — or the background (and everything drawn after it) paints at the expanded
// size and escapes the viewport.
func TestViewportRenderDefersToAnActiveClip(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
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
	scene := &courtroom.Scene{BackgroundBase: "bg"}

	// The stage, and the zoomed (expanded) destination the camera renders into:
	// a 1.5x zoom toward the stage centre.
	stage := sdl.Rect{X: 128, Y: 0, W: 256, H: 384}
	zoomed := sdl.Rect{X: 64, Y: -96, W: 384, H: 576}

	// X=127 is LEFT of the stage (X=128) but inside the un-clipped expanded fill
	// (X=64..448); X=140 is safely inside the stage.
	const outsideX, insideX, y = 127, 140, 192

	img, err := ct.Capture(ren, func(dst sdl.Rect) {
		vp.Update(scene, 16*time.Millisecond)
		clip := stage // the camera-zoom clip (the ORIGINAL stage)
		_ = ren.SetClipRect(&clip)
		vp.Render(ren, scene, zoomed)
		_ = ren.SetClipRect(nil)
	})
	if err != nil {
		t.Skipf("ReadPixels unavailable headlessly: %v", err)
	}
	if img.RGBAAt(insideX, y).R == 0 {
		t.Fatal("the on-stage background did not draw (test setup wrong)")
	}
	if img.RGBAAt(outsideX, y).R > 0 {
		t.Errorf("the zoomed background spilled LEFT of the stage — Render stomped the camera-zoom clip")
	}
}
