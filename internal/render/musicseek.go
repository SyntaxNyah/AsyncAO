package render

// Direct cgo binding for SDL_mixer's Mix_SetMusicPosition, kept separate from
// musicclock.go's runtime-resolved (dlsym/GetProcAddress) shim ON PURPOSE.
// useless for a sub-second custom loop point (issue #48). The underlying C
// function has taken a double since 2.0.0, so this file just calls it
// directly with the fractional value go-sdl2's own wrapper throws away.

/*
extern int Mix_SetMusicPosition(double position);
*/
import "C"

// SeekMusicPrecise seeks the currently-playing music stream to an exact
// sub-second position, in seconds. It reports whether the seek was accepted.
// seek. Main-thread only: a.music is owned by the render thread (rule #1),
// and Mix_SetMusicPosition acts on whatever stream the mixer currently has
// loaded, so this must never be called off that thread.
func (a *Audio) SeekMusicPrecise(sec float64) bool {
	if a == nil || !a.enabled || a.music == nil {
		return false // no device / no stream: nothing to seek
	}
	return C.Mix_SetMusicPosition(C.double(sec)) != -1
}
