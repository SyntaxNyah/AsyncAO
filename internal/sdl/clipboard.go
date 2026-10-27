package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

func GetClipboardText() (string, error) {
	return sdl2.GetClipboardText()
}

func SetClipboardText(text string) error {
	return sdl2.SetClipboardText(text)
}
