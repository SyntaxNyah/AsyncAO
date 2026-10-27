package render

// MusicClock reports the live song's true position and total duration in
// seconds. ok is false when the device is disabled, no song is loaded, or the
// decoder cannot tell its length; in every one of those cases callers fall back
// to the wall-clock estimate rather than seeking blindly.
//
// Main-thread only: the music tracks are owned by the render thread (rule #1).
func (a *Audio) MusicClock() (posSec, durSec float64, ok bool) {
	if a == nil || !a.enabled() || a.music.audio == nil {
		return 0, 0, false // no device / no song: nothing to read
	}
	durSec, haveDur := a.music.audio.Duration()
	posSec, havePos := a.music.track.Position()
	return posSec, durSec, haveDur && havePos
}
