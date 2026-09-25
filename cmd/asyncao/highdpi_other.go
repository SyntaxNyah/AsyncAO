//go:build !darwin

package main

import "github.com/veandco/go-sdl2/sdl"

// highDPIFlags is a no-op off macOS: no high-DPI window flag, no Retina drawable
// to bridge, so Windows/Linux behave exactly as they always have.
func highDPIFlags() uint32 { return 0 }

// retinaScaleFactor is 1 off macOS (drawable == window size), so the render loop
// reduces to the plain UI scale.
func retinaScaleFactor(_ *sdl.Renderer, _ *sdl.Window) float32 { return 1 }
