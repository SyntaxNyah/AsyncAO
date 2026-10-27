package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

type (
	Keysym = sdl2.Keysym
)

func GetKeyFromName(name string) Keycode {
	return sdl2.GetKeyFromName(name)
}

func GetKeyName(code Keycode) string {
	return sdl2.GetKeyName(code)
}

func GetKeyboardState() []uint8 {
	return sdl2.GetKeyboardState()
}

func GetModState() Keymod {
	return sdl2.GetModState()
}

func GetScancodeFromKey(code Keycode) Scancode {
	return sdl2.GetScancodeFromKey(code)
}

func SetModState(mod Keymod) {
	sdl2.SetModState(mod)
}
