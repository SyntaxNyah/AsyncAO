// Package sdl is AsyncAO's cgo binding to SDL3 with go-sdl2's names, converting at the boundary.
// Per-frame calls are noescape/nocallback to stay off the heap. Call only from the locked main thread (hard rule 1).
package sdl
