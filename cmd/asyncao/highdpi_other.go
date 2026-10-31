//go:build !darwin

package main

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// retinaScaleFactor is 1 off macOS, where the drawable is the window size.
func retinaScaleFactor(_ *sdl.Renderer, _ *sdl.Window) float32 { return 1 }
