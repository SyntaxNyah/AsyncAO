package render

import (
	"fmt"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/mix"
)

const (
	testMixFrequency = 48000
	testMixChannels  = 2
	// testSongRate is the synthesized songs' sample rate.
	testSongRate = 8000
	// testMixStepSec is how much output one mixFor step generates.
	testMixStepSec = 0.05
	// testFadeOutFlag is the MUSIC_EFFECT FADE_OUT bit.
	testFadeOutFlag = musicEffectFadeOut
)

// newTestAudio is an Audio on a headless mixer: real decoding and playback,
// no sound card. Its output is pulled with mixFor.
func newTestAudio(t *testing.T) *Audio {
	t.Helper()
	m, err := mix.NewHeadless(testMixFrequency, testMixChannels)
	if err != nil {
		t.Skipf("SDL3_mixer unavailable: %v", err)
	}
	a := newAudio(nil, m)
	if !a.enabled() {
		t.Fatal("a headless mixer must give an enabled Audio")
	}
	t.Cleanup(a.Close)
	return a
}

// testSong is sec seconds of silent mono WAV.
func testSong(sec float64) []byte {
	const bytesPerSample = 2
	return wavMono16(make([]byte, int(sec*testSongRate)*bytesPerSample), testSongRate)
}

// mixFor advances the headless mixer by sec seconds.
func mixFor(t *testing.T, a *Audio, sec float64) {
	t.Helper()
	out := make([]float32, int(testMixStepSec*testMixFrequency)*testMixChannels)
	for mixed := 0.0; mixed < sec; mixed += testMixStepSec {
		if err := a.mixer.Generate(out); err != nil {
			t.Fatal(err)
		}
	}
}

// noLoopSidecar records that url has no loop sidecar, so a looping start never
// fetches one (the test Audio has no asset manager).
func noLoopSidecar(a *Audio, url string) {
	a.loopMeta[url] = loopMeta{fetchedAt: time.Now()}
}

// A manual StopMusic mid-crossfade unloads both songs (#112), so the next
// PlayMusic starts clean.
func TestStopMusicClearsCrossfade(t *testing.T) {
	const songSec = 2.0
	a := newTestAudio(t)
	a.startMusic("http://cdn/a.opus", testSong(songSec), false, 0, 0)
	a.startMusic("http://cdn/b.opus", testSong(songSec), false, testFadeOutFlag, 0)
	if a.musicOld.audio == nil {
		t.Fatal("FADE_OUT with an audible song must keep the old song fading")
	}
	a.StopMusic()
	if a.music.audio != nil || a.musicOld.audio != nil || a.musicURL != "" || a.musicLoop {
		t.Error("StopMusic must unload both songs and clear the URL and loop flag")
	}
}

// FADE_OUT is skipped for a silent music volume (aomusicplayer.cpp:138): the
// old song stops at once instead of fading.
func TestFadeOutGatedOnVolume(t *testing.T) {
	const songSec = 2.0
	a := newTestAudio(t)
	a.SetVolumes(0, fullVolumePercent, fullVolumePercent)
	a.startMusic("http://cdn/a.opus", testSong(songSec), false, 0, 0)
	a.startMusic("http://cdn/b.opus", testSong(songSec), false, testFadeOutFlag, 0)
	if a.musicOld.audio != nil {
		t.Error("a silent music volume must not crossfade")
	}
	if a.CurrentMusicURL() != "http://cdn/b.opus" {
		t.Errorf("CurrentMusicURL = %q, want the new song", a.CurrentMusicURL())
	}
}

// The old song of a crossfade is unloaded once its fade-out has run.
func TestCrossfadeOldSongReapedAfterFade(t *testing.T) {
	const (
		songSec     = 8.0
		pastFadeSec = float64(musicFadeOutMs)/msPerSecond + testMixStepSec*2
	)
	a := newTestAudio(t)
	a.startMusic("http://cdn/a.opus", testSong(songSec), false, 0, 0)
	a.startMusic("http://cdn/b.opus", testSong(songSec), false, testFadeOutFlag, 0)
	a.stepFadeOutOld()
	if a.musicOld.audio == nil {
		t.Fatal("the old song was unloaded before its fade ran")
	}
	mixFor(t, a, pastFadeSec)
	a.stepFadeOutOld()
	if a.musicOld.audio != nil {
		t.Error("the old song is still loaded after its fade-out finished")
	}
	if !a.MusicPlaying() {
		t.Error("the new song must keep playing through the crossfade")
	}
}

// Swapping a playing song out records where it was, so a parked tab can later
// resume it in place (cross-tab continuity).
func TestTakeoverRecordsSwapSnapshot(t *testing.T) {
	const (
		songSec   = 4.0
		playedSec = 1.0
		tolerance = 0.1
	)
	a := newTestAudio(t)
	a.startMusic("http://cdn/a.opus", testSong(songSec), false, 0, 0)
	mixFor(t, a, playedSec)
	a.startMusic("http://cdn/b.opus", testSong(songSec), false, 0, 0)
	pos, dur, _, ok := a.SwappedOutSnap("http://cdn/a.opus")
	if !ok {
		t.Fatal("no snapshot recorded for the swapped-out song")
	}
	if pos < playedSec-tolerance || pos > playedSec+tolerance || dur < songSec-tolerance {
		t.Errorf("snapshot pos=%.2fs dur=%.2fs, want pos≈%.1fs dur≈%.1fs", pos, dur, playedSec, songSec)
	}
}

// A play-once song is unloaded after it ends (2.9 NO_REPEAT, #15); a looping
// one never is.
func TestReapFinishedMusic(t *testing.T) {
	const (
		songSec  = 0.5
		mixedSec = 1.0
	)
	a := newTestAudio(t)
	a.startMusic("http://cdn/once.opus", testSong(songSec), false, 0, 0)
	a.reapFinishedMusic()
	if !a.MusicPlaying() {
		t.Fatal("a song still playing was reaped")
	}
	mixFor(t, a, mixedSec)
	a.reapFinishedMusic()
	if a.MusicPlaying() || a.CurrentMusicURL() != "" {
		t.Error("a finished play-once song must be unloaded so it can be played again")
	}

	const loopURL = "http://cdn/area.opus"
	noLoopSidecar(a, loopURL)
	a.startMusic(loopURL, testSong(songSec), true, 0, 0)
	mixFor(t, a, mixedSec)
	a.reapFinishedMusic()
	if !a.MusicPlaying() {
		t.Error("a looping song must never be reaped")
	}

	off := newAudio(nil, nil)
	off.reapFinishedMusic() // disabled: nothing to do, must not panic
}

// MusicClock reads the live song's position and length.
func TestMusicClock(t *testing.T) {
	const (
		songSec   = 3.0
		playedSec = 0.5
		tolerance = 0.1
	)
	for name, a := range map[string]*Audio{"disabled": newAudio(nil, nil), "empty": newTestAudio(t)} {
		if _, _, ok := a.MusicClock(); ok {
			t.Errorf("%s: MusicClock ok=true with no song", name)
		}
	}
	var nilAudio *Audio
	if _, _, ok := nilAudio.MusicClock(); ok {
		t.Error("a nil Audio must report MusicClock ok=false")
	}
	a := newTestAudio(t)
	a.startMusic("http://cdn/a.opus", testSong(songSec), false, 0, 0)
	mixFor(t, a, playedSec)
	pos, dur, ok := a.MusicClock()
	if !ok || pos < playedSec-tolerance || pos > playedSec+tolerance || dur < songSec-tolerance || dur > songSec+tolerance {
		t.Errorf("MusicClock = %.2f/%.2f ok=%v, want ≈%.1f/%.1f", pos, dur, ok, playedSec, songSec)
	}
}

// A resume start past the song's end wraps by its real length.
func TestStartMusicWrapsSeek(t *testing.T) {
	const (
		songSec   = 2.0
		seekSec   = 5.0 // 2.5 passes in: lands at 1.0s
		wantSec   = 1.0
		tolerance = 0.1
	)
	a := newTestAudio(t)
	a.startMusic("http://cdn/a.opus", testSong(songSec), false, 0, seekSec)
	pos, _, ok := a.MusicClock()
	if !ok || pos < wantSec-tolerance || pos > wantSec+tolerance {
		t.Errorf("start position = %.2f (ok=%v), want ≈%.1f", pos, ok, wantSec)
	}
}

// Filling the sound cache past its cap while the oldest sound still plays is
// safe: SDL3_mixer keeps an evicted sound alive until its track is done.
func TestChunkEvictionWhilePlaying(t *testing.T) {
	const (
		soundSec = 1.0
		mixedSec = 0.2
	)
	a := newTestAudio(t)
	first := a.loadChunk("sounds/general/first", testSong(soundSec))
	a.playChunk(first, pendingSFX)
	for i := range chunkCacheMax {
		a.loadChunk(fmt.Sprintf("sounds/general/%d", i), testSong(soundSec))
	}
	if a.HasChunk("sounds/general/first") {
		t.Fatal("the oldest sound must have been evicted")
	}
	mixFor(t, a, mixedSec)
	if len(a.chunks) > chunkCacheMax {
		t.Errorf("cache holds %d sounds, cap is %d", len(a.chunks), chunkCacheMax)
	}
}
