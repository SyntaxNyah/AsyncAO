package render

// MusicClock reports the currently-playing stream's true position and total
// duration in seconds, read straight from SDL_mixer (2.6+) through the mixerx
// wrapper. ok is false when the device is disabled, no stream is loaded, or the
// source cannot report a duration/position (mod/midi return -1); in every one
// of those cases callers fall back to the wall-clock estimate rather than
// seeking blindly.
//
// Main-thread only: a.music is owned by the render thread (rule #1).
//
// This used to resolve Mix_MusicDuration / Mix_GetMusicPosition through a
// dlsym/GetProcAddress shim (so a pre-2.6 SDL_mixer link would not break the
// build). That shim is gone now: the binary links SDL Mixer X directly
// (internal/render/mixerx), which always exports both symbols, so the direct
// bindings below are safe and simpler.
func (a *Audio) MusicClock() (posSec, durSec float64, ok bool) {
	if a == nil || !a.enabled || a.music == nil {
		return 0, 0, false // no device / no stream: nothing to read
	}
	durSec = a.music.Duration()
	posSec = a.music.Position()
	if durSec < 0 || posSec < 0 {
		// Duration or position unknown (mod/midi, or the source refused it).
		// Without a length we cannot loop-wrap on resume, and a -1 position
		// would seed the resume math with a negative base, so either way
		// report unknown and let the caller restart from the top.
		return posSec, durSec, false
	}
	return posSec, durSec, true
}
