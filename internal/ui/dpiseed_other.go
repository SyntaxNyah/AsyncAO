//go:build !windows

package ui

import "github.com/veandco/go-sdl2/sdl"

// queryWindowDPI has no reliable native equivalent off Windows in this build, so
// it always reports "unavailable" and the caller falls back to sdl.GetDisplayDPI
// (which is reliable on X11/macOS, where the SDL_WINDOWS_DPI_AWARENESS quirk that
// motivates the Windows path does not apply). #77 Part B.
func queryWindowDPI(win *sdl.Window) (float64, bool) {
	return 0, false
}
