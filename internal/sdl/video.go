package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

type (
	Window = sdl2.Window
)

const (
	FLASH_UNTIL_FOCUSED         = sdl2.FLASH_UNTIL_FOCUSED
	WINDOWEVENT_CLOSE           = sdl2.WINDOWEVENT_CLOSE
	WINDOWEVENT_DISPLAY_CHANGED = sdl2.WINDOWEVENT_DISPLAY_CHANGED
	WINDOWEVENT_FOCUS_GAINED    = sdl2.WINDOWEVENT_FOCUS_GAINED
	WINDOWEVENT_FOCUS_LOST      = sdl2.WINDOWEVENT_FOCUS_LOST
	WINDOWEVENT_MOVED           = sdl2.WINDOWEVENT_MOVED
	WINDOWPOS_CENTERED          = sdl2.WINDOWPOS_CENTERED
	WINDOWPOS_CENTERED_MASK     = sdl2.WINDOWPOS_CENTERED_MASK
	WINDOWPOS_UNDEFINED         = sdl2.WINDOWPOS_UNDEFINED
	WINDOW_ALLOW_HIGHDPI        = sdl2.WINDOW_ALLOW_HIGHDPI
	WINDOW_FULLSCREEN_DESKTOP   = sdl2.WINDOW_FULLSCREEN_DESKTOP
	WINDOW_HIDDEN               = sdl2.WINDOW_HIDDEN
	WINDOW_INPUT_FOCUS          = sdl2.WINDOW_INPUT_FOCUS
	WINDOW_MAXIMIZED            = sdl2.WINDOW_MAXIMIZED
	WINDOW_MINIMIZED            = sdl2.WINDOW_MINIMIZED
	WINDOW_RESIZABLE            = sdl2.WINDOW_RESIZABLE
	WINDOW_SHOWN                = sdl2.WINDOW_SHOWN
)

func CreateWindow(title string, x, y, w, h int32, flags uint32) (*Window, error) {
	return sdl2.CreateWindow(title, x, y, w, h, flags)
}

func GetDisplayBounds(displayIndex int) (rect Rect, err error) {
	return sdl2.GetDisplayBounds(displayIndex)
}

func GetDisplayDPI(displayIndex int) (ddpi, hdpi, vdpi float32, err error) {
	return sdl2.GetDisplayDPI(displayIndex)
}

func GetDisplayUsableBounds(displayIndex int) (rect Rect, err error) {
	return sdl2.GetDisplayUsableBounds(displayIndex)
}

func GetNumVideoDisplays() (int, error) {
	return sdl2.GetNumVideoDisplays()
}
