package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

const (
	BUTTON_LEFT   = sdl2.BUTTON_LEFT
	BUTTON_MIDDLE = sdl2.BUTTON_MIDDLE
	BUTTON_RIGHT  = sdl2.BUTTON_RIGHT
)

func GetMouseState() (x, y int32, state uint32) {
	return sdl2.GetMouseState()
}
