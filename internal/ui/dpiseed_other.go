//go:build !windows && !darwin

package ui

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// queryWindowDPI has no native equivalent off Windows, so it reports unavailable and the caller uses sdl.GetDisplayDPI.
func queryWindowDPI(win *sdl.Window) (float64, bool) {
	return 0, false
}
