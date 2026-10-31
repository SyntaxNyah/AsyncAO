//go:build darwin

package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// queryWindowDPI reports the baseline DPI (96 = 100%) on macOS. A window is sized in points, which already span the
// Retina backing scale, while sdl.GetDisplayDPI returns the physical DPI and would double-count it. The window
// factor still lifts the auto scale on a large display.
func queryWindowDPI(_ *sdl.Window) (float64, bool) {
	return config.BaselineDPI, true
}
