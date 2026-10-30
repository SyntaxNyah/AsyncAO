package render

import (
	"fmt"
	"image"
	"math"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// CaptureTarget is a fixed-size offscreen target for the GIF export; render thread only, as it binds the target.
type CaptureTarget struct {
	tex  *sdl.Texture
	w, h int32
	buf  []byte // reused ABGR8888 readback scratch (one frame)
}

func NewCaptureTarget(ren *sdl.Renderer, w, h int32) (*CaptureTarget, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("render: bad capture size %dx%d", w, h)
	}
	tex, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_TARGET, w, h)
	if err != nil {
		return nil, err
	}
	return &CaptureTarget{tex: tex, w: w, h: h, buf: make([]byte, int(w)*int(h)*texBytesPerPixel)}, nil
}

func (c *CaptureTarget) Size() (int32, int32) { return c.w, c.h }

// Capture draws into the cleared target, reads it back and restores the screen; the result owns its Pix.
func (c *CaptureTarget) Capture(ren *sdl.Renderer, draw func(dst sdl.Rect)) (*image.RGBA, error) {
	if err := ren.SetRenderTarget(c.tex); err != nil {
		return nil, err
	}
	defer func() { _ = ren.SetRenderTarget(nil) }()
	_ = ren.SetDrawColor(0, 0, 0, math.MaxUint8)
	_ = ren.Clear()
	draw(sdl.Rect{X: 0, Y: 0, W: c.w, H: c.h})
	pitch := int(c.w) * texBytesPerPixel
	rect := sdl.Rect{X: 0, Y: 0, W: c.w, H: c.h}
	if err := ren.ReadPixels(&rect, uint32(sdl.PIXELFORMAT_ABGR8888), unsafe.Pointer(&c.buf[0]), pitch); err != nil {
		return nil, err
	}
	pix := make([]byte, len(c.buf)) // ABGR8888 == image.RGBA byte order (same as the screenshot path)
	copy(pix, c.buf)
	return &image.RGBA{Pix: pix, Stride: pitch, Rect: image.Rect(0, 0, int(c.w), int(c.h))}, nil
}

func (c *CaptureTarget) Close() {
	if c.tex != nil {
		_ = c.tex.Destroy()
		c.tex = nil
	}
}
