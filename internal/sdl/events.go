package sdl

/*
#include <SDL3/SDL.h>
*/
import "C"

import "unsafe"

// Event types in use. WINDOWEVENT covers every SDL3 window event; the
// WindowEvent's Event field says which one.
const (
	QUIT            = C.SDL_EVENT_QUIT
	WINDOWEVENT     = C.SDL_EVENT_WINDOW_FIRST
	KEYDOWN         = C.SDL_EVENT_KEY_DOWN
	KEYUP           = C.SDL_EVENT_KEY_UP
	TEXTEDITING     = C.SDL_EVENT_TEXT_EDITING
	TEXTINPUT       = C.SDL_EVENT_TEXT_INPUT
	MOUSEMOTION     = C.SDL_EVENT_MOUSE_MOTION
	MOUSEBUTTONDOWN = C.SDL_EVENT_MOUSE_BUTTON_DOWN
	MOUSEBUTTONUP   = C.SDL_EVENT_MOUSE_BUTTON_UP
	MOUSEWHEEL      = C.SDL_EVENT_MOUSE_WHEEL
	DROPFILE        = C.SDL_EVENT_DROP_FILE
)

// Window event kinds, carried in WindowEvent.Event.
const (
	WINDOWEVENT_NONE = iota
	WINDOWEVENT_SHOWN
	WINDOWEVENT_HIDDEN
	WINDOWEVENT_EXPOSED
	WINDOWEVENT_MOVED
	WINDOWEVENT_RESIZED
	WINDOWEVENT_SIZE_CHANGED
	WINDOWEVENT_MINIMIZED
	WINDOWEVENT_MAXIMIZED
	WINDOWEVENT_RESTORED
	WINDOWEVENT_ENTER
	WINDOWEVENT_LEAVE
	WINDOWEVENT_FOCUS_GAINED
	WINDOWEVENT_FOCUS_LOST
	WINDOWEVENT_CLOSE
	WINDOWEVENT_DISPLAY_CHANGED
)

// windowEventKinds maps SDL3's window event types to WindowEvent.Event.
var windowEventKinds = map[C.SDL_EventType]uint8{
	C.SDL_EVENT_WINDOW_SHOWN:              WINDOWEVENT_SHOWN,
	C.SDL_EVENT_WINDOW_HIDDEN:             WINDOWEVENT_HIDDEN,
	C.SDL_EVENT_WINDOW_EXPOSED:            WINDOWEVENT_EXPOSED,
	C.SDL_EVENT_WINDOW_MOVED:              WINDOWEVENT_MOVED,
	C.SDL_EVENT_WINDOW_RESIZED:            WINDOWEVENT_RESIZED,
	C.SDL_EVENT_WINDOW_PIXEL_SIZE_CHANGED: WINDOWEVENT_SIZE_CHANGED,
	C.SDL_EVENT_WINDOW_MINIMIZED:          WINDOWEVENT_MINIMIZED,
	C.SDL_EVENT_WINDOW_MAXIMIZED:          WINDOWEVENT_MAXIMIZED,
	C.SDL_EVENT_WINDOW_RESTORED:           WINDOWEVENT_RESTORED,
	C.SDL_EVENT_WINDOW_MOUSE_ENTER:        WINDOWEVENT_ENTER,
	C.SDL_EVENT_WINDOW_MOUSE_LEAVE:        WINDOWEVENT_LEAVE,
	C.SDL_EVENT_WINDOW_FOCUS_GAINED:       WINDOWEVENT_FOCUS_GAINED,
	C.SDL_EVENT_WINDOW_FOCUS_LOST:         WINDOWEVENT_FOCUS_LOST,
	C.SDL_EVENT_WINDOW_CLOSE_REQUESTED:    WINDOWEVENT_CLOSE,
	C.SDL_EVENT_WINDOW_DISPLAY_CHANGED:    WINDOWEVENT_DISPLAY_CHANGED,
}

// Event is one input or window event.
type Event interface {
	GetType() uint32
}

// CommonEvent is any event the client does not read fields of.
type CommonEvent struct{ Type uint32 }

// QuitEvent asks the application to quit.
type QuitEvent struct{ Type uint32 }

// UserEvent is an application-defined event (see RegisterEvents).
type UserEvent struct {
	Type     uint32
	WindowID uint32
	Code     int32
}

// WindowEvent is a change to a window; Event is a WINDOWEVENT_* kind.
type WindowEvent struct {
	Type         uint32
	WindowID     uint32
	Event        uint8
	Data1, Data2 int32
}

// KeyboardEvent is a key press or release.
type KeyboardEvent struct {
	Type     uint32
	WindowID uint32
	State    uint8 // 1 pressed, 0 released
	Repeat   uint8 // 1 for a key-repeat press
	Keysym   Keysym
}

// TextEditingEvent is IME composition text.
type TextEditingEvent struct {
	Type          uint32
	WindowID      uint32
	Text          string
	Start, Length int32
}

// TextInputEvent is committed text.
type TextInputEvent struct {
	Type     uint32
	WindowID uint32
	text     string
}

// MouseMotionEvent is a pointer move, in window pixels.
type MouseMotionEvent struct {
	Type       uint32
	WindowID   uint32
	State      uint32
	X, Y       int32
	XRel, YRel int32
}

// MouseButtonEvent is a mouse button press or release.
type MouseButtonEvent struct {
	Type     uint32
	WindowID uint32
	Button   uint8
	State    uint8 // 1 pressed, 0 released
	Clicks   uint8
	X, Y     int32
}

// MouseWheelEvent is a scroll, in whole wheel steps (X, Y) and exact amounts.
type MouseWheelEvent struct {
	Type               uint32
	WindowID           uint32
	X, Y               int32
	PreciseX, PreciseY float32
}

// DropEvent is a file dropped on a window.
type DropEvent struct {
	Type     uint32
	WindowID uint32
	File     string
}

// RenderEvent reports that the renderer lost its textures or render targets.
type RenderEvent struct{ Type uint32 }

func (e *CommonEvent) GetType() uint32      { return e.Type }
func (e *QuitEvent) GetType() uint32        { return e.Type }
func (e *UserEvent) GetType() uint32        { return e.Type }
func (e *WindowEvent) GetType() uint32      { return e.Type }
func (e *KeyboardEvent) GetType() uint32    { return e.Type }
func (e *TextEditingEvent) GetType() uint32 { return e.Type }
func (e *TextInputEvent) GetType() uint32   { return e.Type }
func (e *MouseMotionEvent) GetType() uint32 { return e.Type }
func (e *MouseButtonEvent) GetType() uint32 { return e.Type }
func (e *MouseWheelEvent) GetType() uint32  { return e.Type }
func (e *DropEvent) GetType() uint32        { return e.Type }
func (e *RenderEvent) GetType() uint32      { return e.Type }

// GetText returns the committed text.
func (e *TextInputEvent) GetText() string { return e.text }

func pressed(down C.bool) uint8 {
	if down {
		return 1
	}
	return 0
}

// goEvent copies an SDL_Event into its Go form. Strings are copied at once:
// SDL frees them on the next poll.
func goEvent(e *C.SDL_Event) Event {
	p := unsafe.Pointer(e)
	t := C.SDL_EventType(*(*C.Uint32)(p))
	if kind, ok := windowEventKinds[t]; ok {
		w := (*C.SDL_WindowEvent)(p)
		return &WindowEvent{Type: WINDOWEVENT, WindowID: uint32(w.windowID), Event: kind,
			Data1: int32(w.data1), Data2: int32(w.data2)}
	}
	switch {
	case t == C.SDL_EVENT_QUIT:
		return &QuitEvent{Type: QUIT}
	case t == C.SDL_EVENT_KEY_DOWN || t == C.SDL_EVENT_KEY_UP:
		k := (*C.SDL_KeyboardEvent)(p)
		repeat := pressed(k.repeat)
		return &KeyboardEvent{Type: uint32(t), WindowID: uint32(k.windowID), State: pressed(k.down), Repeat: repeat,
			Keysym: Keysym{Scancode: Scancode(k.scancode), Sym: Keycode(k.key), Mod: uint16(k.mod)}}
	case t == C.SDL_EVENT_TEXT_EDITING:
		te := (*C.SDL_TextEditingEvent)(p)
		return &TextEditingEvent{Type: uint32(t), WindowID: uint32(te.windowID), Text: C.GoString(te.text),
			Start: int32(te.start), Length: int32(te.length)}
	case t == C.SDL_EVENT_TEXT_INPUT:
		ti := (*C.SDL_TextInputEvent)(p)
		return &TextInputEvent{Type: uint32(t), WindowID: uint32(ti.windowID), text: C.GoString(ti.text)}
	case t == C.SDL_EVENT_MOUSE_MOTION:
		m := (*C.SDL_MouseMotionEvent)(p)
		return &MouseMotionEvent{Type: uint32(t), WindowID: uint32(m.windowID), State: uint32(m.state),
			X: int32(m.x), Y: int32(m.y), XRel: int32(m.xrel), YRel: int32(m.yrel)}
	case t == C.SDL_EVENT_MOUSE_BUTTON_DOWN || t == C.SDL_EVENT_MOUSE_BUTTON_UP:
		b := (*C.SDL_MouseButtonEvent)(p)
		return &MouseButtonEvent{Type: uint32(t), WindowID: uint32(b.windowID), Button: uint8(b.button),
			State: pressed(b.down), Clicks: uint8(b.clicks), X: int32(b.x), Y: int32(b.y)}
	case t == C.SDL_EVENT_MOUSE_WHEEL:
		w := (*C.SDL_MouseWheelEvent)(p)
		return &MouseWheelEvent{Type: uint32(t), WindowID: uint32(w.windowID), X: int32(w.integer_x), Y: int32(w.integer_y),
			PreciseX: float32(w.x), PreciseY: float32(w.y)}
	case t == C.SDL_EVENT_DROP_FILE:
		d := (*C.SDL_DropEvent)(p)
		return &DropEvent{Type: uint32(t), WindowID: uint32(d.windowID), File: C.GoString(d.data)}
	case t == C.SDL_EVENT_RENDER_TARGETS_RESET || t == C.SDL_EVENT_RENDER_DEVICE_RESET || t == C.SDL_EVENT_RENDER_DEVICE_LOST:
		return &RenderEvent{Type: uint32(t)}
	case t >= C.SDL_EVENT_USER:
		u := (*C.SDL_UserEvent)(p)
		return &UserEvent{Type: uint32(t), WindowID: uint32(u.windowID), Code: int32(u.code)}
	}
	return &CommonEvent{Type: uint32(t)}
}

// PollEvent returns the next pending event, or nil when there is none.
func PollEvent() Event {
	var e C.SDL_Event
	if !C.SDL_PollEvent(&e) {
		return nil
	}
	return goEvent(&e)
}

// WaitEventTimeout waits up to ms milliseconds for an event; nil on timeout.
func WaitEventTimeout(ms int) Event {
	var e C.SDL_Event
	if !C.SDL_WaitEventTimeout(&e, C.Sint32(ms)) {
		return nil
	}
	return goEvent(&e)
}

// PushEvent queues a QuitEvent or UserEvent; other kinds push their type only.
// Safe from any goroutine. filtered reports that an event filter dropped it.
func PushEvent(ev Event) (filtered bool, err error) {
	var e C.SDL_Event
	u := (*C.SDL_UserEvent)(unsafe.Pointer(&e))
	u._type = C.Uint32(ev.GetType())
	if ue, ok := ev.(*UserEvent); ok {
		u.windowID = C.SDL_WindowID(ue.WindowID)
		u.code = C.Sint32(ue.Code)
	}
	if C.SDL_PushEvent(&e) {
		return false, nil
	}
	if err := GetError(); err != nil {
		return false, err
	}
	return true, nil
}

// RegisterEvents reserves n user event types and returns the first, or
// ^uint32(0) when none are left.
func RegisterEvents(n int) uint32 {
	first := C.SDL_RegisterEvents(C.int(n))
	if first == 0 {
		return ^uint32(0)
	}
	return uint32(first)
}
