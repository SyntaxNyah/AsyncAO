package render

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

const (
	heldSongURL     = "local://mount/sounds/music/song.opus"
	heldSongSec     = 4.0
	heldWaitTimeout = 5 * time.Second
	heldPollStep    = time.Millisecond
)

var errNoSidecar = errors.New("no sidecar")

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
	if s.body == nil {
		return nil, errNoSidecar
	}
	return s.body, nil
}

func (s *blockingSidecars) open() { s.once.Do(func() { close(s.release) }) }

// newHeldAudio is a test Audio whose sidecar fetches block until released.
func newHeldAudio(t *testing.T, sidecars *blockingSidecars) *Audio {
	t.Helper()
	a := newTestAudio(t)
	a.fetchSidecar = sidecars.fetch
	t.Cleanup(sidecars.open)
	return a
}

// deliverSong requests a looping song and delivers its bytes, the way Frame does.
func deliverSong(a *Audio, url string, seconds float64) {
	a.pending[url] = pendingPlay{kind: pendingMusic, deadline: time.Now().Add(pendingPlayTTL), loop: true}
	a.onAudioBytes(assets.AudioAsset{Base: url, Data: testSong(seconds), Type: assets.AssetTypeMusic})
}

// stepUntilPlaying steps the held music until url plays, failing after heldWaitTimeout.
func stepUntilPlaying(t *testing.T, a *Audio, url string) {
	t.Helper()
	deadline := time.Now().Add(heldWaitTimeout)
	for a.musicURL != url {
		if time.Now().After(deadline) {
			t.Fatalf("%q never started; playing %q", url, a.musicURL)
		}
		a.stepHeldMusic()
		time.Sleep(heldPollStep)
	}
}

// Delivering a song returns while its sidecar fetch is stuck: a synchronous fetch would hang here.
func TestSlowSidecarDoesNotBlockDelivery(t *testing.T) {
	sidecars := newBlockingSidecars("")
	a := newHeldAudio(t, sidecars)
	deliverSong(a, heldSongURL, heldSongSec)
	a.stepHeldMusic()
	if a.musicURL == heldSongURL {
		t.Fatal("song started before its sidecars or the wait finished")
	}
	if _, held := a.pending[heldSongURL]; !held {
		t.Fatal("song is not held while its sidecars are fetched")
	}
}

// The first playthrough still gets its loop points when the sidecar arrives within the wait.
func TestHeldSongStartsWithItsLoopPoints(t *testing.T) {
	const (
		loopEndSec = 2.0
		pastEndSec = 3.0
	)
	sidecars := newBlockingSidecars("seconds=true\nloop_start=0\nloop_end=2\n")
	a := newHeldAudio(t, sidecars)
	deliverSong(a, heldSongURL, heldSongSec)
	sidecars.open()
	stepUntilPlaying(t, a, heldSongURL)
	if meta := a.loopMeta[heldSongURL]; !meta.found {
		t.Fatalf("sidecar result not cached: %+v", meta)
	}
	mixFor(t, a, pastEndSec)
	pos, _, ok := a.MusicClock()
	if !ok || pos >= loopEndSec {
		t.Errorf("position %.2f (ok=%v) after %.1fs, want it looped before %.1fs", pos, ok, pastEndSec, loopEndSec)
	}
}

// A sidecar that never answers delays the song by the wait at most, and a late answer serves the next play.
func TestHeldSongStartsWhenTheWaitRunsOut(t *testing.T) {
	sidecars := newBlockingSidecars("seconds=true\nloop_start=0\nloop_end=2\n")
	a := newHeldAudio(t, sidecars)
	deliverSong(a, heldSongURL, heldSongSec)
	stepUntilPlaying(t, a, heldSongURL)
	if _, cached := a.loopMeta[heldSongURL]; cached {
		t.Fatal("no sidecar answered, yet a result is cached")
	}
	sidecars.open()
	deadline := time.Now().Add(heldWaitTimeout)
	for len(a.loopFetches) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("late sidecar result never collected")
		}
		a.stepHeldMusic()
		time.Sleep(heldPollStep)
	}
	if meta := a.loopMeta[heldSongURL]; !meta.found {
		t.Errorf("late sidecar result not cached for the next play: %+v", meta)
	}
}

// A cached answer, including "no sidecar exists", starts at once and is never re-probed (hard rule 6).
func TestCachedSidecarStartsAtOnce(t *testing.T) {
	sidecars := newBlockingSidecars("")
	a := newHeldAudio(t, sidecars)
	noLoopSidecar(a, heldSongURL)
	deliverSong(a, heldSongURL, heldSongSec)
	if a.musicURL != heldSongURL {
		t.Errorf("playing %q, want the song to start at once", a.musicURL)
	}
	if len(a.loopFetches) != 0 {
		t.Error("a cached answer started a fetch")
	}
}

// Stopping music while a song is held cancels it: the held bytes must not start later.
func TestStopMusicDropsAHeldSong(t *testing.T) {
	sidecars := newBlockingSidecars("")
	a := newHeldAudio(t, sidecars)
	deliverSong(a, heldSongURL, heldSongSec)
	a.StopMusic()
	sidecars.open()
	deadline := time.Now().Add(heldWaitTimeout)
	for len(a.loopFetches) != 0 && time.Now().Before(deadline) {
		a.stepHeldMusic()
		time.Sleep(heldPollStep)
	}
	if a.musicURL != "" {
		t.Errorf("stopped song started anyway: %q", a.musicURL)
	}
}

// At the fetch cap a song starts without waiting, so sidecar goroutines stay bounded (hard rule 4).
func TestFetchCapStartsWithoutWaiting(t *testing.T) {
	sidecars := newBlockingSidecars("")
	a := newHeldAudio(t, sidecars)
	for i := range loopFetchCap {
		a.loopFetches[string(rune('a'+i))] = struct{}{}
	}
	deliverSong(a, heldSongURL, heldSongSec)
	if a.musicURL != heldSongURL {
		t.Errorf("playing %q, want a start without waiting at the cap", a.musicURL)
	}
	clear(a.loopFetches)
}
