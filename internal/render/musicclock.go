package render

// Runtime binding for SDL_mixer's Mix_MusicDuration / Mix_GetMusicPosition
// (both landed in SDL2_mixer 2.6.0). go-sdl2 v0.4.40 does NOT bind either, but
// the SDL2_mixer already linked into this binary (via go-sdl2/mix) exports them
// when the runtime library is >= 2.6 — which every AsyncAO ship target is (msys2
// ucrt64 is 2.8.x; the flatpak manifest pins release-2.8.0).
//
// WHY resolve at RUNTIME instead of a normal cgo call: a hard reference to
// Mix_MusicDuration would force the SYMBOL into the link, and some build
// environments (a CI box, a distro) may still carry pre-2.6 SDL2_mixer
// headers/import-libs — there the build would break at compile or link time even
// though we never call the function on that platform. Resolving the symbol from
// the already-loaded module at runtime keeps the binding invisible to the
// toolchain: it compiles and links everywhere, and simply reports "unknown"
// where the runtime library is too old to export the symbol. Precedent for a
// cgo binding in this package is go-sdl2 itself; the pattern of a thin cgo shim
// is the webp binding in internal/assets/webp_cgo.go.
//
// Everything here is called only from (a *Audio) methods, which are main-thread
// by rule #1 (all SDL access is on the locked render thread). The C resolver is
// guarded by a once-flag so it dlsym/GetProcAddress's exactly once, caching even
// the FAILED state so a hot path never re-resolves.

/*
// _GNU_SOURCE MUST be the first line of this preamble (before ANY #include).
// WHY: the non-Windows branch below resolves symbols with RTLD_DEFAULT, and
// glibc's <dlfcn.h> gates RTLD_DEFAULT/RTLD_NEXT behind #ifdef __USE_GNU, which
// is enabled only when _GNU_SOURCE is defined before <features.h> is first
// included (dlsym(3): "RTLD_DEFAULT and RTLD_NEXT are defined ... only when
// _GNU_SOURCE was defined before including <dlfcn.h>"). cgo does NOT auto-define
// it — stdlib cgo files that need GNU extensions define it themselves
// (net/cgo_unix_cgo.go). It has to sit ABOVE <stdlib.h>: <stdlib.h> transitively
// pulls in <features.h>, whose include guard makes a later #define a silent
// no-op — so a macro placed just above <dlfcn.h> would compile-fail on glibc CI.
// Harmless on Windows (that branch never touches dlfcn) and on musl (which
// defines RTLD_DEFAULT unconditionally).
#define _GNU_SOURCE 1

// Linux only: dlsym/dlopen live in libdl on glibc < 2.34 (2.34+ folds them into
// libc, where the flag is a harmless no-op). macOS provides them in libSystem
// and Windows uses the Win32 API below — so the flag is scoped to linux to avoid
// erroring the mac link (there is no libdl there).
#cgo linux LDFLAGS: -ldl
#include <stdlib.h>

// The two SDL_mixer 2.6+ entry points, typed against a plain void* so this file
// needs neither SDL_mixer.h nor pkg-config (the Mix_Music* ABI is a bare
// pointer — passing it as void* is identical at the call boundary, and it keeps
// a pre-2.6 header out of the picture entirely). Both return a double: seconds,
// or -1.0 when the source can't report it (mod/midi have no known length/pos).
typedef double (*asyncao_music_fn)(void *music);

#ifdef _WIN32
#include <windows.h>
static void *asyncao_mixer_sym(const char *name) {
    // SDL2_mixer is already loaded in-process (go-sdl2/mix linked it). Grab the
    // module by its DLL name and look the symbol up; a NULL module (name off) or
    // a NULL symbol (pre-2.6 library) both fall through to a NULL return, which
    // the Go side reads as "unresolved" -> ok=false. Never crashes.
    HMODULE h = GetModuleHandleA("SDL2_mixer.dll");
    if (h == NULL) {
        return NULL;
    }
    return (void *)GetProcAddress(h, name);
}
#else
#include <dlfcn.h>
static void *asyncao_mixer_sym(const char *name) {
    // RTLD_DEFAULT searches every object already loaded into the process, so it
    // finds the symbol wherever the dynamic linker put SDL2_mixer — no need to
    // know the soname. NULL (symbol absent on a pre-2.6 library) => "unresolved".
    return dlsym(RTLD_DEFAULT, name);
}
#endif

// Cached function pointers + a resolve-once flag. Resolution is idempotent and
// cheap, but we still gate it so a per-frame MusicClock() never re-scans the
// module table. The failed state (either pointer NULL) is cached too.
static asyncao_music_fn asyncao_dur_fn = NULL;
static asyncao_music_fn asyncao_pos_fn = NULL;
static int asyncao_resolved = 0; // 0 = not yet tried, 1 = tried (fns may still be NULL)

static void asyncao_resolve(void) {
    if (asyncao_resolved) {
        return;
    }
    asyncao_resolved = 1;
    asyncao_dur_fn = (asyncao_music_fn)asyncao_mixer_sym("Mix_MusicDuration");
    asyncao_pos_fn = (asyncao_music_fn)asyncao_mixer_sym("Mix_GetMusicPosition");
}

// asyncao_music_clock reads the CURRENT stream's position + total duration.
// music must be the live Mix_Music* (never NULL — the Go side guards that).
// Returns 1 on success with *pos/*dur filled (seconds), 0 when the symbols are
// unresolved (library too old) — leaving *pos/*dur untouched. A -1 duration
// (unknown length, e.g. mod/midi) is passed through as a valid read; the Go
// side treats dur<0 as "unknown" and reports ok=false.
static int asyncao_music_clock(void *music, double *pos, double *dur) {
    asyncao_resolve();
    if (asyncao_dur_fn == NULL || asyncao_pos_fn == NULL) {
        return 0;
    }
    *dur = asyncao_dur_fn(music);
    *pos = asyncao_pos_fn(music);
    return 1;
}
*/
import "C"

import "unsafe"

// MusicClock reports the currently-playing stream's true position and total
// duration in seconds, read straight from SDL_mixer (2.6+). ok is false when the
// device is disabled, no stream is loaded, the SDL_mixer runtime is too old to
// export the symbols, or the source can't report a duration (mod/midi return
// -1) — in every one of those cases callers fall back to the wall-clock estimate
// rather than seeking blindly.
//
// Main-thread only: a.music is owned by the render thread (rule #1), and the C
// resolver + Mix_* calls run synchronously here. The a.music handle is the same
// *mix.Music the mixer streams from; we hand it to C as an opaque void*
// (unsafe.Pointer) exactly as audio_test.go casts the dummy sentinel — the
// Mix_Music* ABI is a bare pointer, so no header dependency is needed.
func (a *Audio) MusicClock() (posSec, durSec float64, ok bool) {
	if a == nil || !a.enabled || a.music == nil {
		return 0, 0, false // no device / no stream: nothing to read
	}
	var cPos, cDur C.double
	got := C.asyncao_music_clock(unsafe.Pointer(a.music), &cPos, &cDur)
	if got == 0 {
		return 0, 0, false // symbols unresolved: SDL_mixer runtime predates 2.6
	}
	durSec = float64(cDur)
	posSec = float64(cPos)
	if durSec < 0 || posSec < 0 {
		// Duration or position unknown (mod/midi, or the source refused it —
		// the two APIs report -1.0 INDEPENDENTLY, so a codec can know its length
		// yet not its position). Without a length we can't loop-wrap on resume,
		// and a -1 position would seed the resume math with a negative base, so
		// either way report unknown and let the caller restart from the top.
		return posSec, durSec, false
	}
	return posSec, durSec, true
}
