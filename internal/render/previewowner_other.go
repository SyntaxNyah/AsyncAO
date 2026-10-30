//go:build !windows

package render

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// LinkOwner has no equivalent off Windows, so it reports false and leaves z-order alone.
func (p *PreviewWindow) LinkOwner(mainWin *sdl.Window) bool {
	return false
}
