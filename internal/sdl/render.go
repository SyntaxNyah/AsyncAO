package sdl

/*
#include <SDL3/SDL.h>

static SDL_Renderer *asyncaoCreateRenderer(SDL_Window *window, bool software) {
	return SDL_CreateRenderer(window, software ? SDL_SOFTWARE_RENDERER : NULL);
}

static const SDL_PixelFormat *asyncaoTextureFormats(SDL_Renderer *renderer) {
	return (const SDL_PixelFormat *)SDL_GetPointerProperty(SDL_GetRendererProperties(renderer),
		SDL_PROP_RENDERER_TEXTURE_FORMATS_POINTER, NULL);
}
*/
import "C"

import "unsafe"

// Renderer draws into one window.
type Renderer C.SDL_Renderer

// Texture is a GPU image owned by one renderer.
type Texture C.SDL_Texture

// BlendMode is how a draw combines with the target.
type BlendMode uint32

// Blend modes in use.
const (
	BLENDMODE_NONE  = C.SDL_BLENDMODE_NONE
	BLENDMODE_BLEND = C.SDL_BLENDMODE_BLEND
	BLENDMODE_ADD   = C.SDL_BLENDMODE_ADD
	BLENDMODE_MOD   = C.SDL_BLENDMODE_MOD
)

// RendererFlip mirrors a texture copy.
type RendererFlip uint32

// Flip directions.
const (
	FLIP_NONE       RendererFlip = C.SDL_FLIP_NONE
	FLIP_HORIZONTAL              = C.SDL_FLIP_HORIZONTAL
	FLIP_VERTICAL                = C.SDL_FLIP_VERTICAL
)

// ScaleMode is the filter a texture is sampled with.
type ScaleMode uint32

// Texture filters.
const (
	ScaleModeNearest ScaleMode = C.SDL_SCALEMODE_NEAREST
	ScaleModeLinear            = C.SDL_SCALEMODE_LINEAR
)

// Texture access modes.
const (
	TEXTUREACCESS_STATIC = C.SDL_TEXTUREACCESS_STATIC
	TEXTUREACCESS_TARGET = C.SDL_TEXTUREACCESS_TARGET
)

// Renderer creation flags. SDL3 has none of its own: software picks SDL's
// software renderer, and vsync is switched on after creation.
const (
	RENDERER_SOFTWARE = 1 << iota
	RENDERER_ACCELERATED
	RENDERER_PRESENTVSYNC
)

// RendererInfo describes a renderer.
type RendererInfo struct {
	Name           string
	TextureFormats []uint32
}

// CreateRenderer creates a renderer for window. index is ignored (SDL3 picks by
// name); flags are the RENDERER_* constants.
func CreateRenderer(window *Window, _ int, flags uint32) (*Renderer, error) {
	r := C.asyncaoCreateRenderer(window.c(), flags&RENDERER_SOFTWARE != 0)
	if r == nil {
		return nil, errorOr(false)
	}
	if flags&RENDERER_PRESENTVSYNC != 0 {
		C.SDL_SetRenderVSync(r, 1)
	}
	return (*Renderer)(r), nil
}

func (r *Renderer) c() *C.SDL_Renderer { return (*C.SDL_Renderer)(r) }

// Destroy frees the renderer and every texture it owns.
func (r *Renderer) Destroy() error {
	C.SDL_DestroyRenderer(r.c())
	return nil
}

// GetInfo reports the renderer's name and the texture formats it accepts.
func (r *Renderer) GetInfo() (RendererInfo, error) {
	name := C.SDL_GetRendererName(r.c())
	if name == nil {
		return RendererInfo{}, errorOr(false)
	}
	info := RendererInfo{Name: C.GoString(name)}
	if f := C.asyncaoTextureFormats(r.c()); f != nil {
		for p := f; *p != C.SDL_PIXELFORMAT_UNKNOWN; p = (*C.SDL_PixelFormat)(unsafe.Add(unsafe.Pointer(p), unsafe.Sizeof(*p))) {
			info.TextureFormats = append(info.TextureFormats, uint32(*p))
		}
	}
	return info, nil
}

// SetDefaultTextureScaleMode sets the filter for textures created from now on.
func (r *Renderer) SetDefaultTextureScaleMode(mode ScaleMode) error {
	return errorOr(C.SDL_SetDefaultTextureScaleMode(r.c(), C.SDL_ScaleMode(mode)))
}

// CreateTexture creates a w×h texture.
func (r *Renderer) CreateTexture(format uint32, access int, w, h int32) (*Texture, error) {
	t := C.SDL_CreateTexture(r.c(), C.SDL_PixelFormat(format), C.SDL_TextureAccess(access), C.int(w), C.int(h))
	if t == nil {
		return nil, errorOr(false)
	}
	return (*Texture)(t), nil
}

// CreateTextureFromSurface uploads a surface into a new texture.
func (r *Renderer) CreateTextureFromSurface(s *Surface) (*Texture, error) {
	t := C.SDL_CreateTextureFromSurface(r.c(), s.s)
	if t == nil {
		return nil, errorOr(false)
	}
	return (*Texture)(t), nil
}

// Clear fills the target with the draw colour.
func (r *Renderer) Clear() error { return errorOr(C.SDL_RenderClear(r.c())) }

// Present shows the frame.
func (r *Renderer) Present() { C.SDL_RenderPresent(r.c()) }

// Flush submits queued draws now.
func (r *Renderer) Flush() error { return errorOr(C.SDL_FlushRenderer(r.c())) }

// Copy draws src of t into dst; nil means the whole texture or target.
func (r *Renderer) Copy(t *Texture, src, dst *Rect) error {
	var s, d C.SDL_FRect
	return errorOr(C.SDL_RenderTexture(r.c(), t.c(), frect(src, &s), frect(dst, &d)))
}

// CopyEx is Copy with a rotation (degrees, around center or dst's middle when
// nil) and a flip.
func (r *Renderer) CopyEx(t *Texture, src, dst *Rect, angle float64, center *Point, flip RendererFlip) error {
	var s, d C.SDL_FRect
	var cp *C.SDL_FPoint
	var c C.SDL_FPoint
	if center != nil {
		c = C.SDL_FPoint{x: C.float(center.X), y: C.float(center.Y)}
		cp = &c
	}
	return errorOr(C.SDL_RenderTextureRotated(r.c(), t.c(), frect(src, &s), frect(dst, &d),
		C.double(angle), cp, C.SDL_FlipMode(flip)))
}

// SetDrawColor sets the colour for Clear, lines and rectangles.
func (r *Renderer) SetDrawColor(red, green, blue, alpha uint8) error {
	return errorOr(C.SDL_SetRenderDrawColor(r.c(), C.Uint8(red), C.Uint8(green), C.Uint8(blue), C.Uint8(alpha)))
}

// SetDrawBlendMode sets how lines and rectangles blend.
func (r *Renderer) SetDrawBlendMode(bm BlendMode) error {
	return errorOr(C.SDL_SetRenderDrawBlendMode(r.c(), C.SDL_BlendMode(bm)))
}

// DrawLine draws a line between two pixels, both included.
func (r *Renderer) DrawLine(x1, y1, x2, y2 int32) error {
	return errorOr(C.SDL_RenderLine(r.c(), C.float(x1), C.float(y1), C.float(x2), C.float(y2)))
}

// DrawRect outlines rect.
func (r *Renderer) DrawRect(rect *Rect) error {
	var f C.SDL_FRect
	return errorOr(C.SDL_RenderRect(r.c(), frect(rect, &f)))
}

// FillRect fills rect; nil fills the target.
func (r *Renderer) FillRect(rect *Rect) error {
	var f C.SDL_FRect
	return errorOr(C.SDL_RenderFillRect(r.c(), frect(rect, &f)))
}

// SetClipRect limits drawing to rect; nil removes the limit.
func (r *Renderer) SetClipRect(rect *Rect) error {
	var cr *C.SDL_Rect
	if rect != nil {
		cr = rect.c()
	}
	return errorOr(C.SDL_SetRenderClipRect(r.c(), cr))
}

// GetClipRect returns the clip rectangle (empty when clipping is off).
func (r *Renderer) GetClipRect() Rect {
	var rect Rect
	C.SDL_GetRenderClipRect(r.c(), rect.c())
	return rect
}

// IsClipEnabled reports whether a clip rectangle is set.
func (r *Renderer) IsClipEnabled() bool { return bool(C.SDL_RenderClipEnabled(r.c())) }

// SetScale scales every later draw.
func (r *Renderer) SetScale(x, y float32) error {
	return errorOr(C.SDL_SetRenderScale(r.c(), C.float(x), C.float(y)))
}

// GetScale returns the draw scale.
func (r *Renderer) GetScale() (x, y float32) {
	var cx, cy C.float
	C.SDL_GetRenderScale(r.c(), &cx, &cy)
	return float32(cx), float32(cy)
}

// GetOutputSize returns the window's drawable size in pixels, whatever the
// current target.
func (r *Renderer) GetOutputSize() (w, h int32, err error) {
	var cw, ch C.int
	err = errorOr(C.SDL_GetRenderOutputSize(r.c(), &cw, &ch))
	return int32(cw), int32(ch), err
}

// SetRenderTarget draws into t from now on; nil draws into the window.
func (r *Renderer) SetRenderTarget(t *Texture) error {
	return errorOr(C.SDL_SetRenderTarget(r.c(), t.c()))
}

// GetRenderTarget returns the current target (nil = the window).
func (r *Renderer) GetRenderTarget() *Texture { return (*Texture)(C.SDL_GetRenderTarget(r.c())) }

// ReadPixels copies rect of the current target (nil = all of it) into pixels,
// converted to format, with the given row pitch.
func (r *Renderer) ReadPixels(rect *Rect, format uint32, pixels unsafe.Pointer, pitch int) error {
	var cr *C.SDL_Rect
	if rect != nil {
		cr = rect.c()
	}
	s := C.SDL_RenderReadPixels(r.c(), cr)
	if s == nil {
		return errorOr(false)
	}
	defer C.SDL_DestroySurface(s)
	return errorOr(C.SDL_ConvertPixels(s.w, s.h, s.format, s.pixels, s.pitch,
		C.SDL_PixelFormat(format), pixels, C.int(pitch)))
}

func (t *Texture) c() *C.SDL_Texture { return (*C.SDL_Texture)(t) }

// Destroy frees the texture.
func (t *Texture) Destroy() error {
	C.SDL_DestroyTexture(t.c())
	return nil
}

// Update replaces rect of the texture (nil = all) with pixels.
func (t *Texture) Update(rect *Rect, pixels unsafe.Pointer, pitch int) error {
	var cr *C.SDL_Rect
	if rect != nil {
		cr = rect.c()
	}
	return errorOr(C.SDL_UpdateTexture(t.c(), cr, pixels, C.int(pitch)))
}

// SetBlendMode sets how the texture blends when drawn.
func (t *Texture) SetBlendMode(bm BlendMode) error {
	return errorOr(C.SDL_SetTextureBlendMode(t.c(), C.SDL_BlendMode(bm)))
}

// GetBlendMode returns the texture's blend mode.
func (t *Texture) GetBlendMode() (BlendMode, error) {
	var bm C.SDL_BlendMode
	err := errorOr(C.SDL_GetTextureBlendMode(t.c(), &bm))
	return BlendMode(bm), err
}

// SetAlphaMod multiplies the texture's alpha when drawn.
func (t *Texture) SetAlphaMod(alpha uint8) error {
	return errorOr(C.SDL_SetTextureAlphaMod(t.c(), C.Uint8(alpha)))
}

// GetAlphaMod returns the alpha multiplier.
func (t *Texture) GetAlphaMod() (uint8, error) {
	var a C.Uint8
	err := errorOr(C.SDL_GetTextureAlphaMod(t.c(), &a))
	return uint8(a), err
}

// SetColorMod multiplies the texture's colour when drawn.
func (t *Texture) SetColorMod(red, green, blue uint8) error {
	return errorOr(C.SDL_SetTextureColorMod(t.c(), C.Uint8(red), C.Uint8(green), C.Uint8(blue)))
}

// SetScaleMode sets the texture's filter.
func (t *Texture) SetScaleMode(mode ScaleMode) error {
	return errorOr(C.SDL_SetTextureScaleMode(t.c(), C.SDL_ScaleMode(mode)))
}

// GetScaleMode returns the texture's filter.
func (t *Texture) GetScaleMode() (ScaleMode, error) {
	var m C.SDL_ScaleMode
	err := errorOr(C.SDL_GetTextureScaleMode(t.c(), &m))
	return ScaleMode(m), err
}
