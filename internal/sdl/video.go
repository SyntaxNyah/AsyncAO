package sdl

/*
#include <stdlib.h>
#include <SDL3/SDL.h>

static SDL_Window *asyncaoCreateWindow(const char *title, int x, int y, int w, int h, SDL_WindowFlags flags) {
	SDL_PropertiesID props = SDL_CreateProperties();
	SDL_SetStringProperty(props, SDL_PROP_WINDOW_CREATE_TITLE_STRING, title);
	SDL_SetNumberProperty(props, SDL_PROP_WINDOW_CREATE_X_NUMBER, x);
	SDL_SetNumberProperty(props, SDL_PROP_WINDOW_CREATE_Y_NUMBER, y);
	SDL_SetNumberProperty(props, SDL_PROP_WINDOW_CREATE_WIDTH_NUMBER, w);
	SDL_SetNumberProperty(props, SDL_PROP_WINDOW_CREATE_HEIGHT_NUMBER, h);
	SDL_SetNumberProperty(props, SDL_PROP_WINDOW_CREATE_FLAGS_NUMBER, (Sint64)flags);
	SDL_Window *window = SDL_CreateWindowWithProperties(props);
	SDL_DestroyProperties(props);
	return window;
}

static void *asyncaoWin32Handle(SDL_Window *window) {
	return SDL_GetPointerProperty(SDL_GetWindowProperties(window), SDL_PROP_WINDOW_WIN32_HWND_POINTER, NULL);
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Window is a top-level OS window.
type Window C.SDL_Window

// Window flags in use. SDL3 shows windows by default, so WINDOW_SHOWN is no flag.
const (
	WINDOW_SHOWN              = 0
	WINDOW_HIDDEN             = C.SDL_WINDOW_HIDDEN
	WINDOW_RESIZABLE          = C.SDL_WINDOW_RESIZABLE
	WINDOW_MINIMIZED          = C.SDL_WINDOW_MINIMIZED
	WINDOW_MAXIMIZED          = C.SDL_WINDOW_MAXIMIZED
	WINDOW_INPUT_FOCUS        = C.SDL_WINDOW_INPUT_FOCUS
	WINDOW_ALLOW_HIGHDPI      = C.SDL_WINDOW_HIGH_PIXEL_DENSITY
	WINDOW_FULLSCREEN_DESKTOP = C.SDL_WINDOW_FULLSCREEN
)

// Window positions. A CENTERED or UNDEFINED position may carry a display
// index in its low bits; the binding turns that index into SDL3's display ID.
const (
	WINDOWPOS_UNDEFINED      = C.SDL_WINDOWPOS_UNDEFINED
	WINDOWPOS_CENTERED       = C.SDL_WINDOWPOS_CENTERED
	WINDOWPOS_CENTERED_MASK  = C.SDL_WINDOWPOS_CENTERED_MASK
	windowPosUndefinedMask   = C.SDL_WINDOWPOS_UNDEFINED_MASK
	windowPosDisplayBitsMask = 0xFFFF
)

// FlashOperation is how Flash draws attention.
type FlashOperation uint32

// FLASH_UNTIL_FOCUSED flashes until the window gains focus.
const FLASH_UNTIL_FOCUSED = C.SDL_FLASH_UNTIL_FOCUSED

// dpiPerScale converts a display content scale to a DPI value
// (1.0 = 96 DPI, the Windows and X11 baseline).
const dpiPerScale = 96

var errNoDisplay = errors.New("sdl: no such display")

// displays returns the connected displays' IDs; index 0 is the primary display.
func displays() []C.SDL_DisplayID {
	var n C.int
	ids := C.SDL_GetDisplays(&n)
	if ids == nil {
		return nil
	}
	defer C.SDL_free(unsafe.Pointer(ids))
	return append([]C.SDL_DisplayID(nil), unsafe.Slice(ids, int(n))...)
}

func displayID(index int) (C.SDL_DisplayID, error) {
	ids := displays()
	if index < 0 || index >= len(ids) {
		return 0, errNoDisplay
	}
	return ids[index], nil
}

// position turns a display index carried in a CENTERED/UNDEFINED position into
// the display ID SDL3 expects there. Plain coordinates pass through.
func position(p int32) C.int {
	mask := uint32(p) &^ windowPosDisplayBitsMask
	if mask != WINDOWPOS_CENTERED_MASK && mask != windowPosUndefinedMask {
		return C.int(p)
	}
	id, err := displayID(int(uint32(p) & windowPosDisplayBitsMask))
	if err != nil {
		return C.int(mask)
	}
	return C.int(mask | uint32(id))
}

// GetNumVideoDisplays returns how many displays are connected.
func GetNumVideoDisplays() (int, error) {
	ids := displays()
	if ids == nil {
		return 0, errorOr(false)
	}
	return len(ids), nil
}

// GetDisplayBounds returns a display's desktop area.
func GetDisplayBounds(index int) (Rect, error) {
	var r Rect
	id, err := displayID(index)
	if err != nil {
		return r, err
	}
	return r, errorOr(C.SDL_GetDisplayBounds(id, r.c()))
}

// GetDisplayUsableBounds returns a display's desktop area minus taskbars and docks.
func GetDisplayUsableBounds(index int) (Rect, error) {
	var r Rect
	id, err := displayID(index)
	if err != nil {
		return r, err
	}
	return r, errorOr(C.SDL_GetDisplayUsableBounds(id, r.c()))
}

// GetDisplayDPI returns a display's DPI, derived from its content scale; the
// diagonal, horizontal and vertical values are equal.
func GetDisplayDPI(index int) (ddpi, hdpi, vdpi float32, err error) {
	id, err := displayID(index)
	if err != nil {
		return 0, 0, 0, err
	}
	scale := float32(C.SDL_GetDisplayContentScale(id))
	if scale == 0 {
		return 0, 0, 0, errorOr(false)
	}
	dpi := scale * dpiPerScale
	return dpi, dpi, dpi, nil
}

// CreateWindow opens a window at (x, y) with size w×h.
func CreateWindow(title string, x, y, w, h int32, flags uint32) (*Window, error) {
	ctitle := C.CString(title)
	defer C.free(unsafe.Pointer(ctitle))
	win := C.asyncaoCreateWindow(ctitle, position(x), position(y), C.int(w), C.int(h), C.SDL_WindowFlags(flags))
	if win == nil {
		return nil, errorOr(false)
	}
	return (*Window)(win), nil
}

func (w *Window) c() *C.SDL_Window { return (*C.SDL_Window)(w) }

// Destroy closes the window.
func (w *Window) Destroy() error {
	C.SDL_DestroyWindow(w.c())
	return nil
}

// GetID returns the ID events carry for this window.
func (w *Window) GetID() (uint32, error) {
	id := C.SDL_GetWindowID(w.c())
	if id == 0 {
		return 0, errorOr(false)
	}
	return uint32(id), nil
}

// GetFlags returns the window's WINDOW_* state flags.
func (w *Window) GetFlags() uint32 { return uint32(C.SDL_GetWindowFlags(w.c())) }

// GetDisplayIndex returns the index of the display the window is on.
func (w *Window) GetDisplayIndex() (int, error) {
	id := C.SDL_GetDisplayForWindow(w.c())
	for i, d := range displays() {
		if d == id {
			return i, nil
		}
	}
	return 0, errNoDisplay
}

// GetPosition returns the window's top-left corner.
func (w *Window) GetPosition() (x, y int32) {
	var cx, cy C.int
	C.SDL_GetWindowPosition(w.c(), &cx, &cy)
	return int32(cx), int32(cy)
}

// SetPosition moves the window.
func (w *Window) SetPosition(x, y int32) { C.SDL_SetWindowPosition(w.c(), position(x), position(y)) }

// GetSize returns the window's size.
func (w *Window) GetSize() (width, height int32) {
	var cw, ch C.int
	C.SDL_GetWindowSize(w.c(), &cw, &ch)
	return int32(cw), int32(ch)
}

// SetSize resizes the window.
func (w *Window) SetSize(width, height int32) {
	C.SDL_SetWindowSize(w.c(), C.int(width), C.int(height))
}

// Restore un-minimises or un-maximises the window.
func (w *Window) Restore() { C.SDL_RestoreWindow(w.c()) }

// SetBordered shows or hides the window frame.
func (w *Window) SetBordered(bordered bool) { C.SDL_SetWindowBordered(w.c(), C.bool(bordered)) }

// SetFullscreen enters desktop fullscreen for WINDOW_FULLSCREEN_DESKTOP and
// leaves fullscreen for 0.
func (w *Window) SetFullscreen(flags uint32) error {
	return errorOr(C.SDL_SetWindowFullscreen(w.c(), flags&WINDOW_FULLSCREEN_DESKTOP != 0))
}

// SetIcon sets the window and taskbar icon. SDL copies the surface, so the
// caller may free it afterwards.
func (w *Window) SetIcon(icon *Surface) error {
	return errorOr(C.SDL_SetWindowIcon(w.c(), icon.s))
}

// Flash asks the OS to draw attention to the window.
func (w *Window) Flash(op FlashOperation) error {
	return errorOr(C.SDL_FlashWindow(w.c(), C.SDL_FlashOperation(op)))
}

// StartTextInput turns on text-input events for the window. SDL3 sends none
// until it is called.
func (w *Window) StartTextInput() error { return errorOr(C.SDL_StartTextInput(w.c())) }

// Win32Handle returns the window's HWND, or nil off Windows.
func (w *Window) Win32Handle() unsafe.Pointer { return C.asyncaoWin32Handle(w.c()) }
