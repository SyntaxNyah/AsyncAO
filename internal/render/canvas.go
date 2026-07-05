package render

import (
	"log"

	"github.com/veandco/go-sdl2/sdl"
)

// Canvas is the compositor's frame cache: a window-sized render-target
// texture the whole UI draws into. The main loop re-renders it only when
// something actually changed — and clips that render to the damaged region —
// then blits it to the backbuffer EVERY pass (one textured quad, near-free)
// so presents stay dense at a steady cadence. That split is the point: the
// playtest evidence pinned the single-frame window flicker on SPARSE presents
// (low idle rates flicker, continuous presenting doesn't), while continuous
// full REDRAWS burn GPU — the cache gives steady presents at damage-only draw
// cost. Render thread only.
type Canvas struct {
	tex  *sdl.Texture
	w, h int32
	// ok is the boot-time render-target capability: without target-texture
	// support (some exotic/dummy backends) the compositor cannot exist and
	// the loop falls back to the classic paths.
	ok bool
	// lost marks the cached contents invalid (RENDER_TARGETS_RESET /
	// RENDER_DEVICE_RESET: target textures lose their pixels on a device
	// reset). The next Ensure rebuilds the texture and reports recreated so
	// the caller full-damages.
	lost bool
	// failed latches a texture-creation error so a broken driver logs once
	// and falls back instead of retrying (and re-logging) every pass.
	failed bool
}

// NewCanvas probes render-target support and returns the (empty) frame cache.
// Sizing happens lazily in Ensure — the window size isn't final until the
// loop runs (saved-size clamp, fullscreen restore).
func NewCanvas(ren *sdl.Renderer) *Canvas {
	info, err := ren.GetInfo()
	ok := err == nil && info.Flags&sdl.RENDERER_TARGETTEXTURE != 0
	if !ok {
		log.Printf("render: no target-texture support (%v); selective rendering unavailable", err)
	}
	return &Canvas{ok: ok}
}

// OK reports whether the compositor can run at all (render-target support
// present and the texture not permanently failed).
func (c *Canvas) OK() bool { return c.ok && !c.failed }

// Invalidate marks the cached frame lost (device/targets reset event): the
// texture still exists but its pixels are undefined, so Ensure must rebuild
// and the caller must re-render everything.
func (c *Canvas) Invalidate() { c.lost = true }

// Ensure (re)creates the cache at w×h. recreated=true means the previous
// contents are gone — resize, device reset, or first use — and the caller
// must mark the whole frame damaged. A creation failure latches failed (OK
// goes false) so the loop degrades to the classic render path.
func (c *Canvas) Ensure(ren *sdl.Renderer, w, h int32) (recreated bool) {
	if !c.OK() || w <= 0 || h <= 0 {
		return false
	}
	if c.tex != nil && !c.lost && w == c.w && h == c.h {
		return false
	}
	if c.tex != nil {
		_ = c.tex.Destroy()
		c.tex = nil
	}
	tex, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ARGB8888), sdl.TEXTUREACCESS_TARGET, w, h)
	if err != nil {
		c.failed = true
		log.Printf("render: frame-cache texture %dx%d failed (%v); selective rendering off", w, h, err)
		return false
	}
	// The cache is an opaque, fully-painted frame: blit it as a plain copy.
	// Left at BLEND, the backbuffer (undefined after present) would show
	// through any alpha the UI's blended fills left in the texture.
	_ = tex.SetBlendMode(sdl.BLENDMODE_NONE)
	c.tex, c.w, c.h, c.lost = tex, w, h, false
	return true
}

// Begin binds the cache as the render target: everything until End draws
// into the cached frame instead of the backbuffer.
func (c *Canvas) Begin(ren *sdl.Renderer) error { return ren.SetRenderTarget(c.tex) }

// End restores the default target (the backbuffer). SDL restores the default
// target's own viewport/clip/scale state alongside.
func (c *Canvas) End(ren *sdl.Renderer) { _ = ren.SetRenderTarget(nil) }

// Blit copies the whole cached frame onto the backbuffer at 1:1. The scale
// reset is deliberate: the classic paths leave the UI scale set on the
// default target, and a scaled blit would draw the cache oversized on the
// first pass after a live mode switch.
func (c *Canvas) Blit(ren *sdl.Renderer) {
	if c.tex == nil {
		return
	}
	_ = ren.SetScale(1, 1)
	_ = ren.Copy(c.tex, nil, nil)
}

// Destroy frees the cache texture.
func (c *Canvas) Destroy() {
	if c.tex != nil {
		_ = c.tex.Destroy()
		c.tex = nil
	}
}
