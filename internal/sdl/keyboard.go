package sdl

/*
#include <stdlib.h>
#include <SDL3/SDL.h>
*/
import "C"

import "unsafe"

// Keycode is a layout-dependent key (what the key types).
type Keycode int32

// Scancode is a physical key position.
type Scancode uint32

// Keymod is a set of held modifier keys.
type Keymod uint32

// Keysym is the key of a KeyboardEvent.
type Keysym struct {
	Scancode Scancode
	Sym      Keycode
	Mod      uint16
}

// Modifier masks.
const (
	KMOD_NONE   = C.SDL_KMOD_NONE
	KMOD_LSHIFT = C.SDL_KMOD_LSHIFT
	KMOD_SHIFT  = C.SDL_KMOD_SHIFT
	KMOD_LCTRL  = C.SDL_KMOD_LCTRL
	KMOD_CTRL   = C.SDL_KMOD_CTRL
	KMOD_ALT    = C.SDL_KMOD_ALT
)

// Keycodes in use.
const (
	K_UNKNOWN   = C.SDLK_UNKNOWN
	K_BACKSPACE = C.SDLK_BACKSPACE
	K_TAB       = C.SDLK_TAB
	K_RETURN    = C.SDLK_RETURN
	K_ESCAPE    = C.SDLK_ESCAPE
	K_SPACE     = C.SDLK_SPACE
	K_DELETE    = C.SDLK_DELETE
	K_0         = C.SDLK_0
	K_1         = C.SDLK_1
	K_9         = C.SDLK_9
	K_a         = C.SDLK_A
	K_c         = C.SDLK_C
	K_h         = C.SDLK_H
	K_l         = C.SDLK_L
	K_m         = C.SDLK_M
	K_r         = C.SDLK_R
	K_s         = C.SDLK_S
	K_v         = C.SDLK_V
	K_x         = C.SDLK_X
	K_y         = C.SDLK_Y
	K_z         = C.SDLK_Z
	K_F1        = C.SDLK_F1
	K_F2        = C.SDLK_F2
	K_F3        = C.SDLK_F3
	K_F4        = C.SDLK_F4
	K_F8        = C.SDLK_F8
	K_F9        = C.SDLK_F9
	K_F11       = C.SDLK_F11
	K_HOME      = C.SDLK_HOME
	K_END       = C.SDLK_END
	K_LEFT      = C.SDLK_LEFT
	K_RIGHT     = C.SDLK_RIGHT
	K_UP        = C.SDLK_UP
	K_DOWN      = C.SDLK_DOWN
	K_KP_ENTER  = C.SDLK_KP_ENTER
)

// Mouse buttons.
const (
	BUTTON_LEFT   = C.SDL_BUTTON_LEFT
	BUTTON_MIDDLE = C.SDL_BUTTON_MIDDLE
	BUTTON_RIGHT  = C.SDL_BUTTON_RIGHT
)

// GetKeyName returns a key's human-readable name ("A", "Left", "F2"). Hotkeys
// are saved by this name.
func GetKeyName(code Keycode) string { return C.GoString(C.SDL_GetKeyName(C.SDL_Keycode(code))) }

// GetKeyFromName is the inverse of GetKeyName; K_UNKNOWN when name is unknown.
func GetKeyFromName(name string) Keycode {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return Keycode(C.SDL_GetKeyFromName(cname))
}

// GetScancodeFromKey returns the physical key that types code.
func GetScancodeFromKey(code Keycode) Scancode {
	return Scancode(C.SDL_GetScancodeFromKey(C.SDL_Keycode(code), nil))
}

// GetKeyboardState returns one byte per scancode, 1 while that key is held.
// The slice aliases SDL's own array and stays current.
func GetKeyboardState() []uint8 {
	var n C.int
	state := C.SDL_GetKeyboardState(&n)
	return unsafe.Slice((*uint8)(unsafe.Pointer(state)), int(n))
}

// GetModState returns the held modifiers.
func GetModState() Keymod { return Keymod(C.SDL_GetModState()) }

// SetModState overrides the held modifiers (tests use it to simulate keys).
func SetModState(mod Keymod) { C.SDL_SetModState(C.SDL_Keymod(mod)) }

// GetMouseState returns the pointer position in the focused window and the
// held buttons.
func GetMouseState() (x, y int32, state uint32) {
	var cx, cy C.float
	buttons := C.SDL_GetMouseState(&cx, &cy)
	return int32(cx), int32(cy), uint32(buttons)
}
