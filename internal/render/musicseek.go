package render

// Direct cgo binding for SDL_mixer's Mix_SetMusicPosition, kept separate from
// musicclock.go's runtime-resolved (dlsym/GetProcAddress) shim ON PURPOSE.
//
// musicclock.go resolves Mix_MusicDuration/Mix_GetMusicPosition at runtime
// because those two are genuinely 2.6+-only and a hard reference would force
// the symbol into the link on a toolchain whose SDL2_mixer import lib predates
// 2.6 — the build would break even on a platform that never calls them.
//
// Mix_SetMusicPosition carries none of that risk: it has been ABI-stable
// since SDL_mixer 2.0.0, and it is ALREADY hard-linked into every AsyncAO
// build today via go-sdl2 v0.4.40's own truncating wrapper
// (mix.SetMusicPosition(position int64), go-sdl2@v0.4.40/mix/sdl_mixer.go:
// 619-627: `C.Mix_SetMusicPosition(C.double(_position))`). Every build that
// links this program already resolves this exact symbol against the exact
// same SDL2_mixer import library — there is no old-toolchain link-time
// scenario where go-sdl2's own call succeeds and ours would not. Reaching for
// the dlsym/GetProcAddress indirection here would only add the complexity
// musicclock.go needs for a symbol that can never be missing.
//
// The only thing go-sdl2's wrapper gets wrong for our purposes is the
// signature: it takes an int64 and truncates to whole seconds, which is fine
// for a song resume (audio.go's existing cross-tab seek, §1 decision 31) but
// useless for a sub-second custom loop point (issue #48). The underlying C
// function has taken a double since 2.0.0, so this file just calls it
// directly with the fractional value go-sdl2's own wrapper throws away.

/*
extern int Mix_SetMusicPosition(double position);
*/
import "C"

// SeekMusicPrecise seeks the currently-playing music stream to an exact
// sub-second position, in seconds. It reports whether the seek was accepted.
//
// Guarded exactly like MusicClock(): a nil receiver, a disabled device, or no
// loaded stream all return false before any cgo call — there is nothing to
// seek. Main-thread only: a.music is owned by the render thread (rule #1),
// and Mix_SetMusicPosition acts on whatever stream the mixer currently has
// loaded, so this must never be called off that thread.
//
// A false return is not necessarily "unsupported codec": SDL_mixer masks
// several distinct failures behind the SAME generic "Position not
// implemented for music type" error string (release-2.8.x src/music.c
// Mix_SetMusicPosition), and a target past the end of the stream is one of
// them (e.g. opusfile's op_pcm_seek returns OP_EINVAL there) — see the long
// CAUTION comment above audio.go's existing seek block, which this binding
// replaces the truncating call inside. Callers must not read false as proof
// the format can't seek; they degrade to "keep playing from wherever it is"
// exactly as that block already does.
func (a *Audio) SeekMusicPrecise(sec float64) bool {
	if a == nil || !a.enabled || a.music == nil {
		return false // no device / no stream: nothing to seek
	}
	return C.Mix_SetMusicPosition(C.double(sec)) != -1
}
