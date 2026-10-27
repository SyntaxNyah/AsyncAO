package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

const (
	HINT_RENDER_BATCHING      = sdl2.HINT_RENDER_BATCHING
	HINT_RENDER_SCALE_QUALITY = sdl2.HINT_RENDER_SCALE_QUALITY
)

func SetHint(name, value string) bool {
	return sdl2.SetHint(name, value)
}
