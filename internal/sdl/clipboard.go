package sdl

/*
#include <stdint.h>
#include <stdlib.h>
#include <SDL3/SDL.h>

extern void *asyncaoClipboardData(void *userdata, char *mimeType, size_t *size);
extern void asyncaoClipboardCleanup(void *userdata);

static bool asyncaoSetClipboardData(uintptr_t handle, const char *const *mimeTypes, size_t count) {
	return SDL_SetClipboardData((SDL_ClipboardDataCallback)asyncaoClipboardData,
		(SDL_ClipboardCleanupCallback)asyncaoClipboardCleanup, (void *)handle, mimeTypes, count);
}
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// clipboardOffer holds what the clipboard serves, in C memory because SDL keeps the pointers it is handed.
type clipboardOffer struct {
	data map[string]unsafe.Pointer
	size map[string]C.size_t
}

func (o *clipboardOffer) free() {
	for _, p := range o.data {
		C.free(p)
	}
}

// SetClipboardData offers data, keyed by MIME type, until the clipboard next changes or SDL quits. Main thread only.
func SetClipboardData(data map[string][]byte) error {
	offer := &clipboardOffer{data: make(map[string]unsafe.Pointer, len(data)), size: make(map[string]C.size_t, len(data))}
	mimeTypes := make([]*C.char, 0, len(data))
	defer func() {
		for _, m := range mimeTypes {
			C.free(unsafe.Pointer(m))
		}
	}()
	for mime, bytes := range data {
		mimeTypes = append(mimeTypes, C.CString(mime))
		offer.data[mime] = C.CBytes(bytes)
		offer.size[mime] = C.size_t(len(bytes))
	}
	handle := cgo.NewHandle(offer)
	// SDL copies the type list, and calls the cleanup itself even when it refuses the offer.
	return errorOr(C.asyncaoSetClipboardData(C.uintptr_t(handle), &mimeTypes[0], C.size_t(len(mimeTypes))))
}

// GetClipboardData returns the clipboard's data for a MIME type, nil when it has none.
func GetClipboardData(mime string) []byte {
	cmime := C.CString(mime)
	defer C.free(unsafe.Pointer(cmime))
	var size C.size_t
	p := C.SDL_GetClipboardData(cmime, &size)
	if p == nil {
		return nil
	}
	defer C.SDL_free(p)
	return C.GoBytes(p, C.int(size))
}
