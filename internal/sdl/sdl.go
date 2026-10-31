package sdl

// noescape/nocallback keep per-frame calls allocation-free; none of these keeps a pointer or calls back into Go.

/*
#cgo pkg-config: sdl3
#cgo noescape SDL_RenderTexture
#cgo nocallback SDL_RenderTexture
#cgo noescape SDL_RenderTextureRotated
#cgo nocallback SDL_RenderTextureRotated
#cgo noescape SDL_RenderFillRect
#cgo nocallback SDL_RenderFillRect
#cgo noescape SDL_RenderRect
#cgo nocallback SDL_RenderRect
#cgo noescape SDL_SetRenderClipRect
#cgo nocallback SDL_SetRenderClipRect
#cgo noescape SDL_GetRenderClipRect
#cgo nocallback SDL_GetRenderClipRect
#cgo noescape SDL_GetRectIntersection
#cgo nocallback SDL_GetRectIntersection
#cgo noescape SDL_GetRenderScale
#cgo nocallback SDL_GetRenderScale
#cgo noescape SDL_GetRenderOutputSize
#cgo nocallback SDL_GetRenderOutputSize
#cgo noescape SDL_GetWindowSize
#cgo nocallback SDL_GetWindowSize
#cgo noescape SDL_GetWindowPosition
#cgo nocallback SDL_GetWindowPosition
#cgo noescape SDL_GetKeyboardState
#cgo nocallback SDL_GetKeyboardState
#cgo noescape SDL_GetMouseState
#cgo nocallback SDL_GetMouseState
#cgo noescape SDL_PollEvent
#cgo nocallback SDL_PollEvent
#cgo noescape SDL_WaitEventTimeout
#cgo nocallback SDL_WaitEventTimeout
#cgo noescape SDL_PushEvent
#cgo nocallback SDL_PushEvent
#cgo noescape SDL_GetTextureAlphaMod
#cgo nocallback SDL_GetTextureAlphaMod
#cgo noescape SDL_GetTextureBlendMode
#cgo nocallback SDL_GetTextureBlendMode
#include <stdlib.h>
#include <SDL3/SDL.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Subsystem flags for Init.
const (
	INIT_AUDIO  = C.SDL_INIT_AUDIO
	INIT_VIDEO  = C.SDL_INIT_VIDEO
	INIT_EVENTS = C.SDL_INIT_EVENTS
)

// Init initialises the given SDL subsystems.
func Init(flags uint32) error {
	if !C.SDL_Init(C.SDL_InitFlags(flags)) {
		return GetError()
	}
	return nil
}

// Quit shuts every SDL subsystem down.
func Quit() { C.SDL_Quit() }

// WasInit reports which of flags are running.
func WasInit(flags uint32) uint32 { return uint32(C.SDL_WasInit(C.SDL_InitFlags(flags))) }

// SetAppMetadata names the app to the OS; it must run before Init.
func SetAppMetadata(name, version, id string) error {
	cName, cVersion, cID := C.CString(name), C.CString(version), C.CString(id)
	defer C.free(unsafe.Pointer(cName))
	defer C.free(unsafe.Pointer(cVersion))
	defer C.free(unsafe.Pointer(cID))
	return errorOr(C.SDL_SetAppMetadata(cName, cVersion, cID))
}

// GetError returns SDL's last error message, or nil when there is none.
func GetError() error {
	if msg := C.GoString(C.SDL_GetError()); msg != "" {
		return errors.New(msg)
	}
	return nil
}

// errorOr returns SDL's error when ok is false. SDL3 reports failure through a
// bool return; an empty message still counts as a failure.
func errorOr(ok C.bool) error {
	if ok {
		return nil
	}
	if err := GetError(); err != nil {
		return err
	}
	return errUnknown
}

var errUnknown = errors.New("sdl: call failed without an error message")

// SetHint sets an SDL configuration hint. It reports whether the hint was set.
func SetHint(name, value string) bool {
	cname, cvalue := C.CString(name), C.CString(value)
	defer C.free(unsafe.Pointer(cname))
	defer C.free(unsafe.Pointer(cvalue))
	return bool(C.SDL_SetHint(cname, cvalue))
}

// GetClipboardText returns the clipboard's text.
func GetClipboardText() (string, error) {
	text := C.SDL_GetClipboardText()
	if text == nil {
		return "", GetError()
	}
	defer C.SDL_free(unsafe.Pointer(text))
	return C.GoString(text), nil
}

// SetClipboardText puts text on the clipboard.
func SetClipboardText(text string) error {
	ctext := C.CString(text)
	defer C.free(unsafe.Pointer(ctext))
	return errorOr(C.SDL_SetClipboardText(ctext))
}
