package render

import (
	"math"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// Badge is one small text or emoji texture; BLENDMODE_BLEND is forced so alpha mod fades it.
type Badge struct {
	tex  *sdl.Texture
	w, h int32
	// devScale is the glyph's device font scale; Draw blits 1:1 when the renderer scale equals it.
	devScale int32
	// cgoRect and cgoClip are fields because a rect built inline and passed to cgo heap-escapes on every call.
	cgoRect sdl.Rect
	cgoClip sdl.Rect
}

// RasterizeBadge renders text with BLENDMODE_BLEND; render thread, once per badge; nil for empty text or font.
func RasterizeBadge(ren *sdl.Renderer, font *ttf.Font, text string, col sdl.Color, devScale int32) (*Badge, error) {
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
	// Set explicitly so the fade can't silently do nothing if the default changes.
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	return &Badge{tex: tex, w: surf.W, h: surf.H, devScale: devScale}, nil
}

// Size returns logical dimensions, by logicalFromDevice so a badge and label of one size agree to the pixel.
func (b *Badge) Size() (w, h int32) {
	if b == nil {
		return 0, 0
	}
	return logicalFromDevice(b.w, b.devScale), logicalFromDevice(b.h, b.devScale)
}

// RawSize returns the device-pixel texture size; unlike Size it changes with UI scale.
func (b *Badge) RawSize() (w, h int32) {
	if b == nil {
		return 0, 0
	}
	return b.w, b.h
}

// Draw blits at alpha, then restores opacity as the texture is cached; dst must be reused, or cgo makes it escape.
func (b *Badge) Draw(ren *sdl.Renderer, dst *sdl.Rect, alpha uint8, renderPct int32, clip *sdl.Rect) {
	if b == nil || b.tex == nil {
		return
	}
	_ = b.tex.SetAlphaMod(alpha)
	if deviceExactAt(renderPct, b.devScale) {
		// Drop to 1:1 before re-asserting the clip: one set before the scale change reads back wrong on Metal.
		_ = ren.SetScale(1, 1)
		if clip != nil {
			b.cgoClip = sdl.Rect{
				X: deviceFromLogicalAt(clip.X, b.devScale), Y: deviceFromLogicalAt(clip.Y, b.devScale),
				W: deviceFromLogicalAt(clip.W, b.devScale), H: deviceFromLogicalAt(clip.H, b.devScale),
			}
			_ = ren.SetClipRect(&b.cgoClip)
		}
		// Use the texture's size: dst.W is already rounded, and scaling it back can land on another width.
		b.cgoRect = sdl.Rect{X: deviceFromLogicalAt(dst.X, b.devScale), Y: deviceFromLogicalAt(dst.Y, b.devScale), W: b.w, H: b.h}
		_ = ren.Copy(b.tex, nil, &b.cgoRect)
		// Recompute, not read back: GetScale heap-allocates two float32s per call through cgo.
		s := float32(renderPct) / float32(DefaultDevScale)
		_ = ren.SetScale(s, s)
		if clip != nil {
			b.cgoClip = *clip
			_ = ren.SetClipRect(&b.cgoClip)
		}
	} else {
		_ = ren.Copy(b.tex, nil, dst)
	}
	_ = b.tex.SetAlphaMod(math.MaxUint8) // restore: the texture is cached and reused next frame
}

// Destroy frees the texture; render thread only.
func (b *Badge) Destroy() {
	if b != nil && b.tex != nil {
		_ = b.tex.Destroy()
		b.tex = nil
	}
}
