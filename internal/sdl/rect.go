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

// Union returns the smallest rectangle holding both a and b; an empty one adds nothing. Plain Go: handing the rects
// to cgo would move them to the heap on every call.
func (a *Rect) Union(b *Rect) Rect {
	if a.W <= 0 || a.H <= 0 {
		return *b
	}
	if b.W <= 0 || b.H <= 0 {
		return *a
	}
	x0, y0 := min(a.X, b.X), min(a.Y, b.Y)
	x1, y1 := max(a.X+a.W, b.X+b.W), max(a.Y+a.H, b.Y+b.H)
	return Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
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
