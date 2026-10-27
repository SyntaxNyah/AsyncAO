// Package sdl is AsyncAO's cgo binding to SDL3. Every package that calls SDL
// imports this one, so the rest of the client never sees a C type.
//
// The names and shapes are the ones the client was written against (go-sdl2's,
// for SDL2): integer Rects, Go event structs, display indexes. The binding
// converts at the boundary — Rects to SDL_FRect, SDL3's window events into one
// WindowEvent, display indexes to display IDs — so callers did not change when
// SDL did. It covers only the names AsyncAO uses: add one here when a caller
// needs it.
//
// Per-frame calls are declared noescape/nocallback, so passing a stack Rect or
// out-parameter does not move it to the heap: the draw loop stays
// allocation-free.
//
// Hard rule 1 applies: call this package from the locked main thread only.
package sdl
