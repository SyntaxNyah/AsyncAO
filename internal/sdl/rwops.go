package sdl

/*
#include <SDL3/SDL.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

// RWops is a readable stream over memory (SDL3's SDL_IOStream).
type RWops C.SDL_IOStream

var errEmptyStream = errors.New("sdl: cannot stream an empty buffer")

// RWFromMem streams mem without copying it. SDL reads mem until the stream is
// closed, so the caller keeps mem alive and unchanged until then.
func RWFromMem(mem []byte) (*RWops, error) {
	if len(mem) == 0 {
		return nil, errEmptyStream
	}
	rw := C.SDL_IOFromConstMem(unsafe.Pointer(&mem[0]), C.size_t(len(mem)))
	if rw == nil {
		return nil, errorOr(false)
	}
	return (*RWops)(rw), nil
}

// Free closes the stream.
func (rw *RWops) Free() error {
	return errorOr(C.SDL_CloseIO((*C.SDL_IOStream)(rw)))
}
