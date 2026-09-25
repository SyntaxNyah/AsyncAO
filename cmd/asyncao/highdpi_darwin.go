//go:build darwin

package main

import "github.com/veandco/go-sdl2/sdl"

// highDPIFlags makes the window Retina-aware on macOS so the Metal drawable is
// the full 2x backing store. retinaScaleFactor then bridges the window's point
// size onto that drawable via SetScale (NOT SetLogicalSize, which would also
// remap mouse events and break click hit-testing).
func highDPIFlags() uint32 { return sdl.WINDOW_ALLOW_HIGHDPI }

// retinaScaleFactor reports how many drawable pixels one window point maps to on
// this renderer (2 on a Retina display, 1 elsewhere). The render loop multiplies
// the UI scale by it so the logical canvas still fills the window, while the
// mouse unprojection keeps using the un-multiplied UI scale.
func retinaScaleFactor(ren *sdl.Renderer, win *sdl.Window) float32 {
	outW, _, err := ren.GetOutputSize()
	if err != nil {
		return 1
	}
	w, _ := win.GetSize()
	if w <= 0 || outW <= w {
		return 1
	}
	return float32(outW) / float32(w)
}
