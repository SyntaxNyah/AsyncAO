package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

func GetError() error {
	return sdl2.GetError()
}
