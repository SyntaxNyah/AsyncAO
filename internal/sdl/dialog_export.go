package sdl

// A file with //export may only declare in its preamble, so the C side of the dialogs lives in dialog.go.

/*
#include <SDL3/SDL.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// asyncaoDialogDone is SDL's dialog callback; filelist is NULL on error and points at NULL on cancel.
//
//export asyncaoDialogDone
func asyncaoDialogDone(userdata unsafe.Pointer, filelist **C.char, _ C.int) {
	handle := cgo.Handle(uintptr(userdata))
	p := handle.Value().(*pendingDialog)
	handle.Delete()
	p.free()
	if filelist == nil {
		err := GetError()
		if err == nil {
			err = errUnknown
		}
		p.done(nil, err)
		return
	}
	var paths []string
	for entry := filelist; *entry != nil; entry = (**C.char)(unsafe.Add(unsafe.Pointer(entry), unsafe.Sizeof(*entry))) {
		paths = append(paths, C.GoString(*entry))
	}
	p.done(paths, nil)
}
