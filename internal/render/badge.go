package render

import (
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// Badge is a single small text / emoji texture the UI can position, scale and ALPHA-FADE
// freely — Copy into a chosen dst rect plus SetTextureAlphaMod. Unlike MessageRaster (a
// multi-line, multi-span message raster with no alpha control), a Badge is exactly one
// texture with its blend mode forced to BLENDMODE_BLEND, so an alpha-mod actually fades it
// rather than the glyph popping in and out at full opacity. The floating-reaction overlay
// (#2) needs that. Built once per distinct glyph (the caller caches it), then drawn every
// frame with zero allocation.
type Badge struct {
	tex  *sdl.Texture
	w, h int32
}

// RasterizeBadge renders text (one emoji, or a short glyph run) to a standalone texture in
// col, with BLENDMODE_BLEND set explicitly so SetTextureAlphaMod fades it. A colour-emoji
// glyph keeps its own colours and ignores col (SDL_ttf draws the colour bitmap) — exactly
// what a reaction emoji wants. Render thread; call once per distinct badge and cache the
// result. Returns (nil, nil) for empty text or a nil font.
func RasterizeBadge(ren *sdl.Renderer, font *ttf.Font, text string, col sdl.Color) (*Badge, error) {
	if font == nil || text == "" {
		return nil, nil
	}
	surf, err := font.RenderUTF8Blended(text, col)
	if err != nil {
		return nil, err
	}
	defer surf.Free()
	tex, err := ren.CreateTextureFromSurface(surf)
	if err != nil {
		return nil, err
	}
	// CreateTextureFromSurface already picks BLEND for an alpha surface, but set it
	// explicitly so the fade can never silently no-op if that default ever changes.
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	return &Badge{tex: tex, w: surf.W, h: surf.H}, nil
}

// Size returns the badge's natural pixel dimensions (so the caller can centre / scale it).
func (b *Badge) Size() (w, h int32) {
	if b == nil {
		return 0, 0
	}
	return b.w, b.h
}

// Draw blits the badge into dst at the given alpha (0..255), then restores full opacity so
// the cached texture is never left dimmed for the next caller (the set→draw→restore
// discipline the shared-texture FX use). Zero-alloc — dst must be a reused scratch rect, not
// a fresh local (a cgo address-of forces a heap escape). A scaling dst (W/H ≠ natural) is
// fine; SDL scales the Copy.
func (b *Badge) Draw(ren *sdl.Renderer, dst *sdl.Rect, alpha uint8) {
	if b == nil || b.tex == nil {
		return
	}
	_ = b.tex.SetAlphaMod(alpha)
	_ = ren.Copy(b.tex, nil, dst)
	_ = b.tex.SetAlphaMod(0xFF) // restore: the texture is cached and reused next frame
}

// Destroy frees the badge's texture. Render thread only.
func (b *Badge) Destroy() {
	if b != nil && b.tex != nil {
		_ = b.tex.Destroy()
		b.tex = nil
	}
}
