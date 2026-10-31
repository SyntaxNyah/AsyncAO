//go:build darwin

package main

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// retinaScaleFactor is drawable pixels per point; mouse unprojection must keep the unmultiplied UI scale.
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
