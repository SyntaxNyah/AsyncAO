// SDL_IOStream callbacks that read through a Go Reader; userdata is its cgo.Handle.
#include <SDL3/SDL.h>
#include "_cgo_export.h"

static Sint64 SDLCALL ioSize(void *userdata) { return asyncaoIOSize((uintptr_t)userdata); }

static Sint64 SDLCALL ioSeek(void *userdata, Sint64 offset, SDL_IOWhence whence) {
	return asyncaoIOSeek((uintptr_t)userdata, offset, (int)whence);
}

static size_t SDLCALL ioRead(void *userdata, void *ptr, size_t size, SDL_IOStatus *status) {
	return asyncaoIORead((uintptr_t)userdata, ptr, size, status);
}

static bool SDLCALL ioClose(void *userdata) {
	asyncaoIOClose((uintptr_t)userdata);
	return true;
}

SDL_IOStream *asyncaoOpenIO(uintptr_t handle) {
	SDL_IOStreamInterface iface;
	SDL_INIT_INTERFACE(&iface);
	iface.size = ioSize;
	iface.seek = ioSeek;
	iface.read = ioRead;
	iface.close = ioClose;
	return SDL_OpenIO(&iface, (void *)handle);
}
