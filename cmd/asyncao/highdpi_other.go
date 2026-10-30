//go:build !darwin

package main

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// highDPIFlags is a no-op off macOS.
func highDPIFlags() uint32 { return 0 }

// retinaScaleFactor is 1 off macOS, where the drawable is the window size.
func retinaScaleFactor(_ *sdl.Renderer, _ *sdl.Window) float32 { return 1 }
