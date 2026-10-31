//go:build !windows && !darwin

package host

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// WindowDPI has no native equivalent off Windows, so it reports unavailable and the caller uses sdl.GetDisplayDPI.
func WindowDPI(_ *sdl.Window) (float64, bool) {
	return 0, false
}
