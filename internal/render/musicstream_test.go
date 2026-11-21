package render

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/media"
)

const (
	streamTestSongSec = 1.0
	streamTestStepSec = 0.05
	streamTestWait    = 10 * time.Second
	// streamTestHeldBytes is the part of the file a held stream lets through: the header and a little audio.
	streamTestHeldBytes = 2048
	// streamTestAll lets every byte through.
	streamTestAll = 1 << 62
)

// heldStream is a file still arriving: reads past open wait until release, and a closed one stops waiting.
type heldStream struct {
	media.Stream
	open    atomic.Int64
	gate    chan struct{}
	stopped chan struct{}
	once    sync.Once
	closed  atomic.Bool
}

func newHeldStream(data []byte, open int64) *heldStream {
	s := &heldStream{Stream: media.BytesStream(data), gate: make(chan struct{}), stopped: make(chan struct{})}
	s.open.Store(open)
	return s
}

func (s *heldStream) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > s.open.Load() {
		select {
		case <-s.gate:
		case <-s.stopped:
			return 0, media.ErrStreamClosed
		}
	}
	return s.Stream.ReadAt(p, off)
}

func (s *heldStream) release() { s.open.Store(streamTestAll); close(s.gate) }

func (s *heldStream) Close() error {
	s.closed.Store(true)
	s.once.Do(func() { close(s.stopped) })
	return nil
}

// playUntil mixes and steps the music until done holds, failing the test after streamTestWait.
func playUntil(t *testing.T, a *Audio, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(streamTestWait)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		a.stepMusicStream()
		a.reapFinishedMusic()
		mixFor(t, a, streamTestStepSec)
		time.Sleep(time.Millisecond)
	}
}

func TestAStreamedSongPlaysToItsEnd(t *testing.T) {
	a := newTestAudio(t)
	src := newHeldStream(testSongWAV(streamTestSongSec), streamTestAll)
	a.startMusic("http://cdn/once.wav", src, false, 0, 0)
	playUntil(t, a, "it starts", func() bool { return a.music.track.Playing() })
	playUntil(t, a, "it ends and its slot clears", func() bool { return !a.MusicPlaying() })
	if !src.closed.Load() {
		t.Fatal("the finished song's file was left open")
	}
}

func TestAStreamedLoopKeepsPlayingAndLearnsItsLength(t *testing.T) {
	const passes = 2.5
	a := newTestAudio(t)
	url := "http://cdn/loop.wav"
	noLoopSidecar(a, url)
	a.startMusic(url, newHeldStream(testSongWAV(streamTestSongSec), streamTestAll), true, 0, 0)
	playUntil(t, a, "it starts", func() bool { return a.music.track.Playing() })
	playUntil(t, a, "it has played past its own length", func() bool { return a.clockPasses() >= passes })
	pos, dur, _ := a.MusicClock()
	if !a.MusicPlaying() || dur < streamTestSongSec-streamTestStepSec || pos > dur {
		t.Fatalf("playing %v at %.2fs of %.2fs; want it still playing, wrapped inside a %.1fs song", a.MusicPlaying(), pos, dur, streamTestSongSec)
	}
}

// clockPasses is how many song lengths the music track has played, from what the stream pushed and still holds.
func (a *Audio) clockPasses() float64 {
	ms := a.music.stream
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.out == nil || ms.rate == 0 {
		return 0
	}
	played := ms.pushed - int64(ms.out.Queued())
	return float64(played) / float64(ms.rate*ms.channels) / streamTestSongSec
}

func TestAStreamedSongRestartsAfterTheDownloadCatchesUp(t *testing.T) {
	a := newTestAudio(t)
	src := newHeldStream(testSongWAV(streamTestSongSec), streamTestHeldBytes)
	a.startMusic("http://cdn/slow.wav", src, false, 0, 0)
	time.Sleep(time.Duration(streamTestStepSec * float64(time.Second)))
	for range int(musicResumeSec/streamTestStepSec) + 1 {
		a.stepMusicStream()
		mixFor(t, a, streamTestStepSec)
	}
	if a.music.track.Playing() {
		t.Fatal("a song started before enough of it had arrived")
	}
	if !a.MusicPlaying() {
		t.Fatal("a song still downloading was dropped")
	}
	src.release()
	playUntil(t, a, "it plays once the rest arrives", func() bool { return a.music.track.Playing() })
}

func TestStoppingAStreamedSongClosesItsFile(t *testing.T) {
	a := newTestAudio(t)
	src := newHeldStream(testSongWAV(streamTestSongSec), streamTestHeldBytes)
	a.startMusic("http://cdn/stop.wav", src, false, 0, 0)
	a.StopMusic()
	if !src.closed.Load() || a.MusicPlaying() {
		t.Fatalf("closed %v, playing %v after StopMusic", src.closed.Load(), a.MusicPlaying())
	}
}
