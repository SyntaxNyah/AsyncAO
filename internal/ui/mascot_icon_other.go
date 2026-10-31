//go:build !windows

package ui

import (
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// SetWindowIcon uploads the embedded Mayo art as the window icon via SDL, since non-Windows has no .ico resource.
// Render thread only; SDL copies the surface, so freeing it here is safe. Failure leaves the default icon.
func SetWindowIcon(win *sdl.Window) {
	if win == nil {
		return
	}
	rgba, ok := decodeMayoRGBA()
	if !ok {
		return
	}
	b := rgba.Bounds()
	surf, err := sdl.CreateRGBSurfaceWithFormatFrom(
		unsafe.Pointer(&rgba.Pix[0]), int32(b.Dx()), int32(b.Dy()), 32, int32(rgba.Stride),
		uint32(sdl.PIXELFORMAT_ABGR8888))
	if err != nil {
		return
	}
	win.SetIcon(surf)
	surf.Free()
}
