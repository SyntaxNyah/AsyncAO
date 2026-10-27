// Package sdl is AsyncAO's binding to SDL. Every package that calls SDL imports
// this one, never go-sdl2 directly, so the SDL version is swapped in one place.
//
// For now each name forwards to the go-sdl2 name it copies, and every type is an
// alias, so values pass freely between this package and go-sdl2's methods. The
// SDL3 move reimplements this package over SDL3 behind the same names
// (docs/RESTRUCTURING_PLAN.md, Part 5d). It covers only the names AsyncAO uses:
// add one here when a caller needs it. Files are named after the go-sdl2 file
// that declares the name.
//
// Hard rule 1 applies: call this package from the locked main thread only.
package sdl
