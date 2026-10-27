package sdl

import (
	"unsafe"

	sdl2 "github.com/veandco/go-sdl2/sdl"
)

type (
	Surface = sdl2.Surface
)

func CreateRGBSurfaceWithFormat(flags uint32, width, height, depth int32, format uint32) (*Surface, error) {
	return sdl2.CreateRGBSurfaceWithFormat(flags, width, height, depth, format)
}

func CreateRGBSurfaceWithFormatFrom(pixels unsafe.Pointer, width, height, depth, pitch int32, format uint32) (*Surface, error) {
	return sdl2.CreateRGBSurfaceWithFormatFrom(pixels, width, height, depth, pitch, format)
}
