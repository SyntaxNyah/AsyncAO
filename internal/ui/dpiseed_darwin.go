//go:build darwin

package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// queryWindowDPI reports the baseline DPI (96 = 100%) on macOS instead of
// querying the display. A macOS window is sized in POINTS, not physical pixels,
// so a point already spans the Retina backing scale; sdl.GetDisplayDPI returns
// the PHYSICAL dpi (≈227 on a Retina panel), which double-counts the density and
// pushes the auto UI scale up to 200%. Reporting baseline keeps the auto scale
// window-size-driven only on macOS (the window factor still lifts it on a large
// display, so it never reads tiny). #77 Part B.
func queryWindowDPI(_ *sdl.Window) (float64, bool) {
	return config.BaselineDPI, true
}
