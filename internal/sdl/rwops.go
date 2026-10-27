package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

type (
	RWops = sdl2.RWops
)

func RWFromMem(mem []byte) (*RWops, error) {
	return sdl2.RWFromMem(mem)
}
