package sdl

import sdl2 "github.com/veandco/go-sdl2/sdl"

type (
	DropEvent        = sdl2.DropEvent
	Event            = sdl2.Event
	KeyboardEvent    = sdl2.KeyboardEvent
	MouseButtonEvent = sdl2.MouseButtonEvent
	MouseMotionEvent = sdl2.MouseMotionEvent
	MouseWheelEvent  = sdl2.MouseWheelEvent
	QuitEvent        = sdl2.QuitEvent
	TextEditingEvent = sdl2.TextEditingEvent
	TextInputEvent   = sdl2.TextInputEvent
	UserEvent        = sdl2.UserEvent
	WindowEvent      = sdl2.WindowEvent
)

const (
	DROPFILE        = sdl2.DROPFILE
	KEYDOWN         = sdl2.KEYDOWN
	MOUSEBUTTONDOWN = sdl2.MOUSEBUTTONDOWN
	MOUSEBUTTONUP   = sdl2.MOUSEBUTTONUP
	MOUSEMOTION     = sdl2.MOUSEMOTION
	MOUSEWHEEL      = sdl2.MOUSEWHEEL
	QUIT            = sdl2.QUIT
	WINDOWEVENT     = sdl2.WINDOWEVENT
)

func PollEvent() Event {
	return sdl2.PollEvent()
}

func PushEvent(event Event) (filtered bool, err error) {
	return sdl2.PushEvent(event)
}

func RegisterEvents(numEvents int) uint32 {
	return sdl2.RegisterEvents(numEvents)
}

func WaitEventTimeout(timeout int) Event {
	return sdl2.WaitEventTimeout(timeout)
}
