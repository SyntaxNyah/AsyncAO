package render

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

const (
	streamSongURL   = "http://cdn/base/sounds/music/song.opus"
	signedSongURL   = "https://cdn.example/song.opus?sig=abc"
	loopSongSec     = 4.0
	loopEndSec      = 2.0
	loopSidecar     = "seconds=true\nloop_start=0\nloop_end=2\n"
	loopWaitTimeout = 5 * time.Second
	loopPollStep    = time.Millisecond
	positionSlack   = 0.1
)

// blockingSidecars answers sidecar fetches only once released, like a slow host.
type blockingSidecars struct {
	release chan struct{}
	once    sync.Once
	body    []byte
}

func newBlockingSidecars(body string) *blockingSidecars {
	return &blockingSidecars{release: make(chan struct{}), body: []byte(body)}
}

func (s *blockingSidecars) fetch(ctx context.Context, _ string) ([]byte, error) {
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.body, nil
}

func (s *blockingSidecars) open() { s.once.Do(func() { close(s.release) }) }

func newSidecarAudio(t *testing.T, sidecars *blockingSidecars) *Audio {
	t.Helper()
	a := newTestAudio(t)
	a.fetchSidecar = sidecars.fetch
	t.Cleanup(sidecars.open)
	return a
}

// deliverSong requests a looping song and delivers its bytes, the way Frame does.
func deliverSong(a *Audio, url string) {
	a.pending[url] = pendingPlay{kind: pendingMusic, deadline: time.Now().Add(pendingPlayTTL), loop: true}
	a.onAudioBytes(assets.AudioAsset{Base: url, Data: testSong(loopSongSec), Type: assets.AssetTypeMusic})
}

// collectFetches steps until every sidecar fetch has reported back.
func collectFetches(t *testing.T, a *Audio) {
	t.Helper()
	deadline := time.Now().Add(loopWaitTimeout)
	for len(a.loopFetches) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("sidecar result never collected")
		}
		a.stepLoopMeta()
		time.Sleep(loopPollStep)
	}
}

// A streamed song plays at once while its sidecar fetch is stuck.
func TestSongStartsWhileItsSidecarIsFetched(t *testing.T) {
	sidecars := newBlockingSidecars("")
	a := newSidecarAudio(t, sidecars)
	deliverSong(a, streamSongURL)
	if a.musicURL != streamSongURL {
		t.Fatalf("playing %q, want the song to start at once", a.musicURL)
	}
	if len(a.loopFetches) != 1 {
		t.Errorf("%d sidecar fetches running, want 1", len(a.loopFetches))
	}
}

// Loop points arriving mid-song apply without moving the playback position, then the song loops at them.
func TestArrivingLoopPointsApplyToThePlayingSong(t *testing.T) {
	const (
		beforeArrivalSec = 1.0
		pastLoopEndSec   = 1.5
	)
	sidecars := newBlockingSidecars(loopSidecar)
	a := newSidecarAudio(t, sidecars)
	deliverSong(a, streamSongURL)
	mixFor(t, a, beforeArrivalSec)
	before, _, _ := a.MusicClock()

	sidecars.open()
	collectFetches(t, a)
	after, _, ok := a.MusicClock()
	if !ok || after < before-positionSlack || after > before+positionSlack {
		t.Fatalf("position moved from %.2f to %.2f (ok=%v) when loop points applied", before, after, ok)
	}

	mixFor(t, a, pastLoopEndSec)
	pos, _, ok := a.MusicClock()
	if !ok || pos >= loopEndSec {
		t.Errorf("position %.2f (ok=%v), want it looped before %.1fs", pos, ok, loopEndSec)
	}
}

// Points that arrive after the song passed the loop end leave it alone: replaying would end it at once.
func TestLoopPointsPastTheirEndDoNotMoveTheSong(t *testing.T) {
	const pastEndSec = 3.0
	sidecars := newBlockingSidecars(loopSidecar)
	a := newSidecarAudio(t, sidecars)
	deliverSong(a, streamSongURL)
	mixFor(t, a, pastEndSec)
	before, _, _ := a.MusicClock()

	sidecars.open()
	collectFetches(t, a)
	after, _, ok := a.MusicClock()
	if !ok || after < before-positionSlack {
		t.Errorf("position moved from %.2f to %.2f (ok=%v)", before, after, ok)
	}
	if !a.music.track.Playing() {
		t.Error("song stopped")
	}
	if !a.loopMeta[streamSongURL].found {
		t.Error("the next play must still get the loop points")
	}
}

// A fade-in must finish first: replaying restarts the ramp from silence.
func TestLoopPointsWaitForTheFadeIn(t *testing.T) {
	sidecars := newBlockingSidecars(loopSidecar)
	a := newSidecarAudio(t, sidecars)
	deliverSong(a, streamSongURL)
	a.musicFadeInEnd = time.Now().Add(loopWaitTimeout)

	sidecars.open()
	collectFetches(t, a)
	if a.loopRearmURL != streamSongURL {
		t.Fatalf("rearm pending for %q during the fade-in", a.loopRearmURL)
	}
	a.musicFadeInEnd = time.Time{}
	a.stepLoopMeta()
	if a.loopRearmURL != "" {
		t.Error("loop points not applied once the fade-in ended")
	}
}

// A result for a song no longer playing is kept for its next play and touches nothing now.
func TestLateResultForAnotherSongIsOnlyCached(t *testing.T) {
	sidecars := newBlockingSidecars(loopSidecar)
	a := newSidecarAudio(t, sidecars)
	deliverSong(a, streamSongURL)
	a.StopMusic()

	sidecars.open()
	collectFetches(t, a)
	if a.musicURL != "" || a.music.track.Playing() {
		t.Errorf("stopped song is playing again: %q", a.musicURL)
	}
	if !a.loopMeta[streamSongURL].found {
		t.Error("result not cached for the next play")
	}
}

// A cached answer, including "no sidecar exists", is never fetched again (hard rule 6).
func TestCachedAnswerIsNotFetchedAgain(t *testing.T) {
	sidecars := newBlockingSidecars("")
	a := newSidecarAudio(t, sidecars)
	noLoopSidecar(a, streamSongURL)
	deliverSong(a, streamSongURL)
	if len(a.loopFetches) != 0 {
		t.Error("a cached answer started a fetch")
	}
}

// A signed direct link has no sidecars to look for.
func TestDirectLinkFetchesNoSidecar(t *testing.T) {
	sidecars := newBlockingSidecars("")
	a := newSidecarAudio(t, sidecars)
	deliverSong(a, signedSongURL)
	if a.musicURL != signedSongURL || len(a.loopFetches) != 0 {
		t.Errorf("playing %q with %d fetches, want it playing with none", a.musicURL, len(a.loopFetches))
	}
}

// Past the fetch cap a song still plays, so sidecar goroutines stay bounded (hard rule 4).
func TestFetchCapStillPlaysTheSong(t *testing.T) {
	sidecars := newBlockingSidecars("")
	a := newSidecarAudio(t, sidecars)
	for i := range loopFetchCap {
		a.loopFetches[string(rune('a'+i))] = struct{}{}
	}
	deliverSong(a, streamSongURL)
	if a.musicURL != streamSongURL {
		t.Errorf("playing %q, want the song", a.musicURL)
	}
	if len(a.loopFetches) != loopFetchCap {
		t.Errorf("%d fetches, want the cap %d", len(a.loopFetches), loopFetchCap)
	}
	clear(a.loopFetches)
}
