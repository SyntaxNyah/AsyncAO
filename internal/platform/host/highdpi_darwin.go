package host

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// highDPIFlags makes the window Retina-aware; scale through SetScale, as SetLogicalSize also remaps mouse events.
func highDPIFlags() uint32 { return sdl.WINDOW_ALLOW_HIGHDPI }
