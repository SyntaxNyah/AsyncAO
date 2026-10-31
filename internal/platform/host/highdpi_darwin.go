package host

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// highDPIFlags makes the window Retina-aware; scale through SetScale, as SetLogicalSize also remaps mouse events.
func highDPIFlags() uint32 { return sdl.WINDOW_ALLOW_HIGHDPI }

// RetinaScale is drawable pixels per point; mouse unprojection must keep the unmultiplied UI scale.
func RetinaScale(ren *sdl.Renderer, win *sdl.Window) float32 {
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
