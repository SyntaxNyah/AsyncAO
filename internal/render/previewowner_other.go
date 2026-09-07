//go:build !windows

package render

import "github.com/veandco/go-sdl2/sdl"

// LinkOwner has no portable equivalent off Windows in this build (the
// z-order-linked-owner-window concept is Win32-specific; X11/macOS have
// their own, different native mechanisms this project does not yet reach
// for), so it always reports false and leaves the preview window's z-order
// exactly as it was before this call — matching
// internal/ui/dpiseed_windows.go's own !windows sibling's "no reliable
// native equivalent, report unavailable" contract.
func (p *PreviewWindow) LinkOwner(mainWin *sdl.Window) bool {
	return false
}
