package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

type (
	RendererFlip = sdl2.RendererFlip
	ScaleMode    = sdl2.ScaleMode
)

const (
	FLIP_HORIZONTAL       = sdl2.FLIP_HORIZONTAL
	FLIP_NONE             = sdl2.FLIP_NONE
	FLIP_VERTICAL         = sdl2.FLIP_VERTICAL
	RENDERER_ACCELERATED  = sdl2.RENDERER_ACCELERATED
	RENDERER_PRESENTVSYNC = sdl2.RENDERER_PRESENTVSYNC
	RENDERER_SOFTWARE     = sdl2.RENDERER_SOFTWARE
	ScaleModeLinear       = sdl2.ScaleModeLinear
	ScaleModeNearest      = sdl2.ScaleModeNearest
	TEXTUREACCESS_STATIC  = sdl2.TEXTUREACCESS_STATIC
	TEXTUREACCESS_TARGET  = sdl2.TEXTUREACCESS_TARGET
)

func CreateRenderer(window *Window, index int, flags uint32) (*Renderer, error) {
	return sdl2.CreateRenderer(window, index, flags)
}
