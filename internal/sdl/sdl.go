package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

const (
	INIT_AUDIO  = sdl2.INIT_AUDIO
	INIT_EVENTS = sdl2.INIT_EVENTS
	INIT_VIDEO  = sdl2.INIT_VIDEO
)

func Init(flags uint32) error {
	return sdl2.Init(flags)
}

func Quit() {
	sdl2.Quit()
}
