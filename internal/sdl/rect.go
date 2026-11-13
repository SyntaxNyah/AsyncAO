package sdl

/*
#include <SDL3/SDL.h>
*/
import "C"

import "unsafe"

// Rect is an integer rectangle. Its layout matches SDL_Rect, so a *Rect is
// handed to SDL without copying.
type Rect struct {
	X, Y, W, H int32
}

// Point is an integer point.
type Point struct {
	X, Y int32
}

// Intersect returns the intersection of a and b, and whether it is non-empty.
func (a *Rect) Intersect(b *Rect) (Rect, bool) {
	var out Rect
	ok := C.SDL_GetRectIntersection(a.c(), b.c(), out.c())
	return out, bool(ok)
}

func (a *Rect) c() *C.SDL_Rect { return (*C.SDL_Rect)(unsafe.Pointer(a)) }

// frect converts r to SDL3's float rectangle; nil stays nil (the whole target).
// The caller owns out, so no allocation happens here.
func frect(r *Rect, out *C.SDL_FRect) *C.SDL_FRect {
	if r == nil {
		return nil
	}
	*out = C.SDL_FRect{x: C.float(r.X), y: C.float(r.Y), w: C.float(r.W), h: C.float(r.H)}
	return out
}
