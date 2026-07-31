package render

// Per-texture scale mode: the missing half of AO2's `scaling` support.
//
// AsyncAO picks ONE filter for the whole client via HINT_RENDER_SCALE_QUALITY
// (cmd/asyncao/main.go), because that is all go-sdl2 v0.4.40 exposes. AO2 picks
// per SPRITE, from the character's char.ini `[Options] scaling` key
// (../AO2-Client/src/text_file_functions.cpp:175-191): a pixel-art character
// declares `scaling = pixel` and is point-sampled no matter what the rest of the
// client does. Issue #21 label 15 is exactly this — a hand-pixelled character
// rendered through our global linear filter "becomes a smoothie".
//
// SDL_SetTextureScaleMode (SDL 2.0.12+) is the per-texture override, and it is
// the ONLY mechanism that can work here:
//
//   - The hint cannot. SDL copies it into texture->scaleMode exactly once, in
//     SDL_CreateTexture, and uploadTier's `defer d.Release()` means a texture
//     built under the wrong hint is permanent. Worse, the char.ini fetch RACES
//     the sprite fetch, so whichever lands first would decide the filter —
//     mislatching intermittently on characters that ask for smooth.
//   - Decode-side resampling cannot. It would have to run before
//     boundedFrameCount, and re-triggering that path costs the long-preanim
//     decimation bug (v1.55.0-test17) plus a 20-40x readback blowup.
//
// go-sdl2 v0.4.40 exports the ScaleMode CONSTANTS (sdl.ScaleModeNearest and
// friends) but binds no (*Texture) method for them, so the ~5 lines of cgo below
// are the whole gap. Same shape as go-sdl2's own <2.0.12 fallback: the version
// guard keeps this compiling against an older SDL by degrading to "unsupported"
// instead of failing the build.
//
// Callers must be on the render thread (hard rule 1) — every one of them is,
// since a *sdl.Texture only exists there.

/*
#cgo pkg-config: sdl2
#include <SDL2/SDL.h>

#if !(SDL_VERSION_ATLEAST(2,0,12))
// Pre-2.0.12 headers have neither the enum nor the accessors. Mirror go-sdl2's
// own fallback (sdl/render.go) so this file still compiles; the wrappers then
// always report unsupported and the caller keeps the client-wide hint.
typedef enum { SDL_ScaleModeNearest, SDL_ScaleModeLinear, SDL_ScaleModeBest } SDL_ScaleMode;
static int SDL_SetTextureScaleMode(SDL_Texture *texture, SDL_ScaleMode scaleMode) { return -1; }
static int SDL_GetTextureScaleMode(SDL_Texture *texture, SDL_ScaleMode *scaleMode) { return -1; }
#endif

static int asyncaoSetTextureScaleMode(SDL_Texture *texture, int mode) {
	return SDL_SetTextureScaleMode(texture, (SDL_ScaleMode)mode);
}

static int asyncaoGetTextureScaleMode(SDL_Texture *texture, int *mode) {
	SDL_ScaleMode m = SDL_ScaleModeNearest;
	int rc = SDL_GetTextureScaleMode(texture, &m);
	*mode = (int)m;
	return rc;
}
*/
import "C"

import (
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

// scaleModeSupported reports whether this build can override a texture's filter
// at all. False only against pre-2.0.12 SDL headers, where the whole feature
// degrades to "everything follows HINT_RENDER_SCALE_QUALITY", i.e. today's
// behaviour. Compile-time constant, so the dead branch folds away.
const scaleModeSupported = C.SDL_MAJOR_VERSION > 2 ||
	(C.SDL_MAJOR_VERSION == 2 && (C.SDL_MINOR_VERSION > 0 ||
		(C.SDL_MINOR_VERSION == 0 && C.SDL_PATCHLEVEL >= 12)))

// setTextureScaleMode points one texture at one filter. Reports whether it took;
// a false return means "this SDL can't", never "the texture is broken", so the
// caller's correct response is to leave the client-wide hint in charge.
//
// Render thread only.
func setTextureScaleMode(tex *sdl.Texture, mode sdl.ScaleMode) bool {
	if tex == nil {
		return false
	}
	return C.asyncaoSetTextureScaleMode((*C.SDL_Texture)(unsafe.Pointer(tex)), C.int(mode)) == 0
}

// textureScaleMode reads a texture's filter back. Production never needs this —
// the page memo is the source of truth — but the binding's failure mode is
// SILENT (a bad pointer cast would corrupt rather than error, and every sprite
// would keep the client-wide filter while the code claimed otherwise), so the
// round-trip test needs a way to ask SDL directly. cgo is not permitted in
// _test.go files, which is why it sits here rather than beside its test.
//
// Render thread only.
func textureScaleMode(tex *sdl.Texture) (sdl.ScaleMode, bool) {
	if tex == nil {
		return sdl.ScaleModeNearest, false
	}
	var mode C.int
	rc := C.asyncaoGetTextureScaleMode((*C.SDL_Texture)(unsafe.Pointer(tex)), &mode)
	return sdl.ScaleMode(mode), rc == 0
}
