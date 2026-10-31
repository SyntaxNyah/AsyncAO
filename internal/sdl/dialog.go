package sdl

/*
#include <stdint.h>
#include <stdlib.h>
#include <SDL3/SDL.h>

extern void asyncaoDialogDone(void *userdata, char **filelist, int filter);

static bool asyncaoShowDialog(uintptr_t handle, SDL_FileDialogType type, SDL_Window *window,
		const SDL_DialogFileFilter *filters, int nfilters, const char *title, const char *location, bool many) {
	SDL_PropertiesID props = SDL_CreateProperties();
	if (props == 0) {
		return false;
	}
	SDL_SetPointerProperty(props, SDL_PROP_FILE_DIALOG_WINDOW_POINTER, window);
	SDL_SetPointerProperty(props, SDL_PROP_FILE_DIALOG_FILTERS_POINTER, (void *)filters);
	SDL_SetNumberProperty(props, SDL_PROP_FILE_DIALOG_NFILTERS_NUMBER, nfilters);
	SDL_SetStringProperty(props, SDL_PROP_FILE_DIALOG_TITLE_STRING, title);
	SDL_SetStringProperty(props, SDL_PROP_FILE_DIALOG_LOCATION_STRING, location);
	SDL_SetBooleanProperty(props, SDL_PROP_FILE_DIALOG_MANY_BOOLEAN, many);
	SDL_ShowFileDialogWithProperties(type, (SDL_DialogFileCallback)asyncaoDialogDone, (void *)handle, props);
	SDL_DestroyProperties(props);
	return true;
}
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// DialogFilter limits a file dialog to a named set of extensions, such as Pattern "png;jpg" or "*" for all files.
type DialogFilter struct {
	Name, Pattern string
}

// DialogOptions configures a file or folder dialog; every field is optional.
type DialogOptions struct {
	Window   *Window
	Title    string
	Location string
	// Many lets the user select several entries, where the OS supports it.
	Many bool
	// Filters is ignored by folder dialogs.
	Filters []DialogFilter
}

// DialogDone receives the chosen paths, empty when cancelled. It may run on any thread and must not block.
type DialogDone func(paths []string, err error)

// pendingDialog keeps what the C side still reads until the dialog finishes.
type pendingDialog struct {
	done    DialogDone
	filters unsafe.Pointer
	strs    []*C.char
}

func (p *pendingDialog) cstr(s string) *C.char {
	if s == "" {
		return nil
	}
	c := C.CString(s)
	p.strs = append(p.strs, c)
	return c
}

func (p *pendingDialog) free() {
	for _, s := range p.strs {
		C.free(unsafe.Pointer(s))
	}
	C.free(p.filters)
}

// ShowOpenFileDialog asks the user for existing files. Call it on the main thread.
func ShowOpenFileDialog(opts DialogOptions, done DialogDone) {
	showDialog(C.SDL_FILEDIALOG_OPENFILE, opts, done)
}

// ShowOpenFolderDialog asks the user for existing folders. Call it on the main thread.
func ShowOpenFolderDialog(opts DialogOptions, done DialogDone) {
	showDialog(C.SDL_FILEDIALOG_OPENFOLDER, opts, done)
}

func showDialog(kind C.SDL_FileDialogType, opts DialogOptions, done DialogDone) {
	p := &pendingDialog{done: done}
	var filters *C.SDL_DialogFileFilter
	if n := len(opts.Filters); n > 0 {
		p.filters = C.malloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.SDL_DialogFileFilter{})))
		filters = (*C.SDL_DialogFileFilter)(p.filters)
		entries := unsafe.Slice(filters, n)
		for i, f := range opts.Filters {
			entries[i].name = p.cstr(f.Name)
			entries[i].pattern = p.cstr(f.Pattern)
		}
	}
	title, location := p.cstr(opts.Title), p.cstr(opts.Location)
	handle := cgo.NewHandle(p)
	shown := C.asyncaoShowDialog(C.uintptr_t(handle), kind, opts.Window.c(), filters, C.int(len(opts.Filters)),
		title, location, C.bool(opts.Many))
	if !shown {
		handle.Delete()
		p.free()
		done(nil, errorOr(false))
	}
}
