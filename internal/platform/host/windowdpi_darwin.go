package host

import (
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// WindowDPI reports the baseline DPI (96 = 100%) on macOS. A window is sized in points, which already span the
// Retina backing scale, while sdl.GetDisplayDPI returns the physical DPI and would double-count it. The window
// factor still lifts the auto scale on a large display.
// baselineDPI is the DPI of 100% scale.
const baselineDPI = 96.0

func WindowDPI(_ *sdl.Window) (float64, bool) {
	return baselineDPI, true
}
