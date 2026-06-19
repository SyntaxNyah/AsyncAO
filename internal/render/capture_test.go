package render

import (
	"image"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestCaptureTargetReadsBack is the render-to-target spike: render a known fill
// into the offscreen target and read it back — proving SetRenderTarget +
// ReadPixels work headlessly. That de-risks the whole GIF export (the scene
// render just replaces the fill).
func TestCaptureTargetReadsBack(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	ct, err := NewCaptureTarget(ren, 32, 24)
	if err != nil {
		t.Skipf("render targets unavailable: %v", err)
	}
	defer ct.Close()

	img, err := ct.Capture(ren, func(dst sdl.Rect) {
		_ = ren.SetDrawColor(200, 40, 40, 255)
		_ = ren.FillRect(&dst)
	})
	if err != nil {
		t.Fatalf("capture failed: %v", err)
	}
	if img.Bounds() != image.Rect(0, 0, 32, 24) {
		t.Fatalf("bounds = %v, want 32x24", img.Bounds())
	}
	r, g, b, _ := img.At(16, 12).RGBA() // centre pixel must be the red we filled, not black
	if r>>8 < 150 || g>>8 > 100 || b>>8 > 100 {
		t.Fatalf("captured pixel = (%d,%d,%d), want ~red — render-to-target readback is broken", r>>8, g>>8, b>>8)
	}
}
