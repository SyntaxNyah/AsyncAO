//go:build !windows

package ui

import (
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

// SetWindowIcon uploads the embedded Mayo art as the window / taskbar icon via
// SDL, since non-Windows platforms have no embedded .ico resource. Render-thread
// only; SDL copies the surface, so freeing it here is safe. Any failure is
// ignored (the platform's default icon stays).
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
