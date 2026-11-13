//go:build !darwin

package host

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

func highDPIFlags() uint32 { return 0 }

// RetinaScale is 1 off macOS, where the drawable is the window size.
func RetinaScale(_ *sdl.Renderer, _ *sdl.Window) float32 { return 1 }
