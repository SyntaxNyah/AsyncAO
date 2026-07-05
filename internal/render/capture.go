package render

import (
	"fmt"
	"image"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

// CaptureTarget is a reusable offscreen render target for the scene GIF export:
// the scene is rendered into a FIXED, capped-size texture (decoupled from the
// window) and read back to an *image.RGBA. Capping the size here bounds memory
// by construction — every captured frame is born at the export resolution, so a
// long scene can't balloon past the budget — and the readback is cheaper than a
// full-window grab. Render-thread only (it binds the renderer's target).
type CaptureTarget struct {
	tex  *sdl.Texture
	w, h int32
	buf  []byte // reused ABGR8888 readback scratch (one frame)
}

// NewCaptureTarget allocates the offscreen target at w×h.
func NewCaptureTarget(ren *sdl.Renderer, w, h int32) (*CaptureTarget, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("render: bad capture size %dx%d", w, h)
	}
	tex, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_TARGET, w, h)
	if err != nil {
		return nil, err
	}
	return &CaptureTarget{tex: tex, w: w, h: h, buf: make([]byte, int(w)*int(h)*4)}, nil
}

// Size reports the target dimensions.
func (c *CaptureTarget) Size() (int32, int32) { return c.w, c.h }

// Capture binds the target, clears it to black, runs draw (which renders into
// the rect the target occupies), reads the pixels back, and restores the screen
// target. Returns a FRESH *image.RGBA each call (its own Pix), so the caller can
// quantize it and drop the RGBA immediately — the readback scratch is reused.
func (c *CaptureTarget) Capture(ren *sdl.Renderer, draw func(dst sdl.Rect)) (*image.RGBA, error) {
	// Restore whatever target was bound on entry, NOT nil: under the
	// compositor the whole UI pass renders inside the frame-cache texture, so
	// a blind nil restore would dump the rest of that pass onto the
	// backbuffer (which is never presented directly in that mode).
	prev := ren.GetRenderTarget()
	if err := ren.SetRenderTarget(c.tex); err != nil {
		return nil, err
	}
	defer func() { _ = ren.SetRenderTarget(prev) }()
	_ = ren.SetDrawColor(0, 0, 0, 255)
	_ = ren.Clear()
	draw(sdl.Rect{X: 0, Y: 0, W: c.w, H: c.h})
	pitch := int(c.w) * 4
	rect := sdl.Rect{X: 0, Y: 0, W: c.w, H: c.h}
	if err := ren.ReadPixels(&rect, uint32(sdl.PIXELFORMAT_ABGR8888), unsafe.Pointer(&c.buf[0]), pitch); err != nil {
		return nil, err
	}
	pix := make([]byte, len(c.buf)) // ABGR8888 == image.RGBA byte order (same as the screenshot path)
	copy(pix, c.buf)
	return &image.RGBA{Pix: pix, Stride: pitch, Rect: image.Rect(0, 0, int(c.w), int(c.h))}, nil
}

// Close frees the target texture.
func (c *CaptureTarget) Close() {
	if c.tex != nil {
		_ = c.tex.Destroy()
		c.tex = nil
	}
}
