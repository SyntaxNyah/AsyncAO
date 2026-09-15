package render

import "testing"

// TestSeekMusicPreciseGuards pins SeekMusicPrecise's early-return guards —
// exactly the shape TestMusicClockDisabledOrEmpty pins for MusicClock and
// TestReapFinishedMusicGuards pins for reapFinishedMusic: a nil receiver, a
// disabled device, and an enabled device with no loaded stream must all
// report false WITHOUT reaching the cgo call, so this runs headlessly (no
// SDL_mixer device, no live *mix.Music). The clean early return IS the
// assertion — a device fixture is deliberately NOT built here: doing so
// would risk crossing into the sentinel-must-never-reach-a-real-mixer-call
// hazard audio_test.go's dummy *mix.Music warns about three times, for a
// binding whose only job in this file is the guard, not the real seek.
func TestSeekMusicPreciseGuards(t *testing.T) {
	// A nil receiver is safe (mirrors MusicClock's nil-receiver guard).
	var nilA *Audio
	if nilA.SeekMusicPrecise(1.5) {
		t.Error("a nil Audio must report SeekMusicPrecise=false")
	}

	// Disabled device: false before any cgo call.
	if (&Audio{}).SeekMusicPrecise(1.5) {
		t.Error("a disabled device must report SeekMusicPrecise=false")
	}

	// Enabled but no stream loaded (music==nil): false before any cgo call.
	if (&Audio{enabled: true}).SeekMusicPrecise(1.5) {
		t.Error("an enabled device with no loaded stream must report SeekMusicPrecise=false")
	}
}
