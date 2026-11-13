package sdl

// A file with //export may only declare in its preamble, so the C side of the clipboard lives in clipboard.go.

/*
#include <stddef.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

//export asyncaoClipboardData
func asyncaoClipboardData(userdata unsafe.Pointer, mimeType *C.char, size *C.size_t) unsafe.Pointer {
	offer := cgo.Handle(uintptr(userdata)).Value().(*clipboardOffer)
	mime := C.GoString(mimeType)
	*size = offer.size[mime]
	return offer.data[mime]
}

//export asyncaoClipboardCleanup
func asyncaoClipboardCleanup(userdata unsafe.Pointer) {
	handle := cgo.Handle(uintptr(userdata))
	handle.Value().(*clipboardOffer).free()
	handle.Delete()
}
