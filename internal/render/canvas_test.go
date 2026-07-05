package render

import (
	"image"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestCanvasEnsureAndBlit pins the compositor frame-cache lifecycle headlessly:
// Ensure creates at size (recreated=true), a same-size Ensure is a no-op, a
// resize and an Invalidate both rebuild, and a fill drawn INTO the cache
// blits back out to the default target — the whole render-into-cache →
// blit-every-pass loop in miniature.
func TestCanvasEnsureAndBlit(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	cv := NewCanvas(ren)
	if !cv.OK() {
		t.Skip("render targets unavailable on this backend")
	}
	defer cv.Destroy()

	if !cv.Ensure(ren, 32, 24) {
		t.Fatal("first Ensure must report recreated")
	}
	if cv.Ensure(ren, 32, 24) {
		t.Fatal("same-size Ensure must be a no-op")
	}
	if !cv.Ensure(ren, 48, 24) {
		t.Fatal("resized Ensure must recreate")
	}
	cv.Invalidate()
	if !cv.Ensure(ren, 48, 24) {
		t.Fatal("Ensure after Invalidate must recreate (target pixels are undefined after a device reset)")
	}

	// Paint the cache green, then blit it into a capture target and read it
	// back: proves Begin/End target routing and the 1:1 opaque copy.
	if err := cv.Begin(ren); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	_ = ren.SetDrawColor(30, 200, 60, 255)
	_ = ren.Clear()
	cv.End(ren)

	ct, err := NewCaptureTarget(ren, 48, 24)
	if err != nil {
		t.Skipf("capture target unavailable: %v", err)
	}
	defer ct.Close()
	img, err := ct.Capture(ren, func(dst sdl.Rect) {
		cv.Blit(ren)
	})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if img.Bounds() != image.Rect(0, 0, 48, 24) {
		t.Fatalf("bounds = %v, want 48x24", img.Bounds())
	}
	r, g, b, _ := img.At(24, 12).RGBA()
	if g>>8 < 150 || r>>8 > 100 || b>>8 > 100 {
		t.Fatalf("blitted pixel = (%d,%d,%d), want ~green — the cache blit is broken", r>>8, g>>8, b>>8)
	}
}

// TestCaptureRestoresPreviousTarget pins the nested-target contract the
// compositor depends on: a Capture that runs while ANOTHER target is bound
// (the frame cache, mid-walk) must restore THAT target on exit, not the
// backbuffer — a blind nil restore would dump the rest of the UI pass onto a
// surface that is never presented in compositor mode.
func TestCaptureRestoresPreviousTarget(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	cv := NewCanvas(ren)
	if !cv.OK() {
		t.Skip("render targets unavailable on this backend")
	}
	defer cv.Destroy()
	cv.Ensure(ren, 16, 16)

	ct, err := NewCaptureTarget(ren, 8, 8)
	if err != nil {
		t.Skipf("capture target unavailable: %v", err)
	}
	defer ct.Close()

	if err := cv.Begin(ren); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := ct.Capture(ren, func(dst sdl.Rect) {}); err != nil {
		t.Fatalf("nested capture: %v", err)
	}
	if got := ren.GetRenderTarget(); got != cv.tex {
		t.Fatal("Capture must restore the previously bound target (the frame cache), not nil")
	}
	cv.End(ren)
}
