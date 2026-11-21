package render

// MusicClock reports the live song's position and duration in seconds; ok is false when unknown. Render thread only.
func (a *Audio) MusicClock() (posSec, durSec float64, ok bool) {
	if a == nil || !a.enabled() || !a.music.loaded() {
		return 0, 0, false // no device / no song: nothing to read
	}
	if ms := a.music.stream; ms != nil {
		return ms.clock()
	}
	durSec, haveDur := a.music.audio.Duration()
	posSec, havePos := a.music.track.Position()
	return posSec, durSec, haveDur && havePos
}
