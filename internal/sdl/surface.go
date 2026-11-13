package sdl

/*
#include <SDL3/SDL.h>

static bool asyncaoCopySurface(SDL_Surface *src, SDL_Surface *dst, int x, int y) {
	SDL_SetSurfaceBlendMode(src, SDL_BLENDMODE_NONE);
	SDL_Rect r = {x, y, src->w, src->h};
	return SDL_BlitSurface(src, NULL, dst, &r);
}
*/
import "C"

import (
	"image/color"
	"unsafe"
)

// Pixel formats in use.
const (
	PIXELFORMAT_ABGR8888 = C.SDL_PIXELFORMAT_ABGR8888
	PIXELFORMAT_ARGB8888 = C.SDL_PIXELFORMAT_ARGB8888
)

// Color is an RGBA colour, laid out like SDL_Color.
type Color struct {
	R, G, B, A uint8
}

// PixelFormat names a surface's pixel format.
type PixelFormat struct {
	Format uint32
}

// Surface is a CPU-side image. W, H, Pitch and Format are read once at creation;
// SDL never changes them for a live surface.
type Surface struct {
	W, H, Pitch int32
	Format      *PixelFormat

	format PixelFormat
	s      *C.SDL_Surface
}

// WrapSurface takes ownership of an SDL_Surface* made by a satellite library
// (ttf). A nil pointer reports SDL's error.
func WrapSurface(p unsafe.Pointer) (*Surface, error) { return newSurface((*C.SDL_Surface)(p)) }

func newSurface(s *C.SDL_Surface) (*Surface, error) {
	if s == nil {
		return nil, errorOr(false)
	}
	sf := &Surface{W: int32(s.w), H: int32(s.h), Pitch: int32(s.pitch), s: s}
	sf.format.Format = uint32(s.format)
	sf.Format = &sf.format
	return sf, nil
}

// CreateRGBSurfaceWithFormat allocates a w×h surface. flags and depth are
// ignored: SDL3 derives depth from the format.
func CreateRGBSurfaceWithFormat(_ uint32, w, h, _ int32, format uint32) (*Surface, error) {
	return newSurface(C.SDL_CreateSurface(C.int(w), C.int(h), C.SDL_PixelFormat(format)))
}

// CreateRGBSurfaceWithFormatFrom wraps existing pixels, which must outlive the
// surface. depth is ignored: SDL3 derives it from the format.
func CreateRGBSurfaceWithFormatFrom(pixels unsafe.Pointer, w, h, _, pitch int32, format uint32) (*Surface, error) {
	return newSurface(C.SDL_CreateSurfaceFrom(C.int(w), C.int(h), C.SDL_PixelFormat(format), pixels, C.int(pitch)))
}

// Free releases the surface.
func (s *Surface) Free() { C.SDL_DestroySurface(s.s) }

// Data returns the first pixel byte.
func (s *Surface) Data() unsafe.Pointer { return s.s.pixels }

// Pixels returns the pixel bytes, Pitch*H long.
func (s *Surface) Pixels() []byte {
	return unsafe.Slice((*byte)(s.s.pixels), int(s.Pitch)*int(s.H))
}

// ConvertFormat returns a copy of s in another pixel format. flags is ignored.
func (s *Surface) ConvertFormat(format, _ uint32) (*Surface, error) {
	return newSurface(C.SDL_ConvertSurface(s.s, C.SDL_PixelFormat(format)))
}

// At returns the colour of one pixel.
func (s *Surface) At(x, y int) color.Color {
	var r, g, b, a C.Uint8
	C.SDL_ReadSurfacePixel(s.s, C.int(x), C.int(y), &r, &g, &b, &a)
	return color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)}
}

// CopyTo copies s into dst with its top-left at (x, y), replacing dst's pixels
// rather than blending with them, and clipped to dst.
func (s *Surface) CopyTo(dst *Surface, x, y int32) error {
	return errorOr(C.asyncaoCopySurface(s.s, dst.s, C.int(x), C.int(y)))
}
