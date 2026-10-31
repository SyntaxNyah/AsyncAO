//go:build !windows

package host

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// LinkWindowOwner has no equivalent off Windows, so it reports false and leaves z-order alone.
func LinkWindowOwner(_, _ *sdl.Window) bool {
	return false
}
