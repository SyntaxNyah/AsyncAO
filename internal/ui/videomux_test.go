package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestAudioCaptureSongSegments pins the multi-song windowing: each track plays from
// its cue to the next cue (change OR stop) or the video end; a stop bounds the
// previous window and adds none of its own; a single song is one untrimmed segment.
func TestAudioCaptureSongSegments(t *testing.T) {
	const frameMs, endFrame = 50, 100

	// No music → no segments.
	empty := &audioCapture{frameRef: func() int { return 0 }}
	if segs := empty.songSegments(frameMs, endFrame); len(segs) != 0 {
		t.Errorf("no music: got %d segments, want 0", len(segs))
	}

	// A single song from frame 10 → one untrimmed segment running to the end.
	frame := 0
	one := &audioCapture{frameRef: func() int { return frame }}
	frame = 10
	one.PlayMusic("A")
	if segs := one.songSegments(frameMs, endFrame); len(segs) != 1 || segs[0] != (songSegment{url: "A", startMs: 500, trimMs: 0}) {
		t.Errorf("single song: %+v, want one {A 500 0}", segs)
	}

	// A → (frame 40) B: A trimmed to B's start (30 frames = 1500 ms), B untrimmed.
	frame = 0
	chg := &audioCapture{frameRef: func() int { return frame }}
	frame = 10
	chg.PlayMusic("A")
	frame = 40
	chg.PlayMusic("B")
	want := []songSegment{{url: "A", startMs: 500, trimMs: 1500}, {url: "B", startMs: 2000, trimMs: 0}}
	if segs := chg.songSegments(frameMs, endFrame); len(segs) != 2 || segs[0] != want[0] || segs[1] != want[1] {
		t.Errorf("song change: %+v, want %+v", chg.songSegments(frameMs, endFrame), want)
	}

	// (leading stop, ignored) A → (frame 30) stop: A trimmed to the stop; stop adds none.
	frame = 0
	stop := &audioCapture{frameRef: func() int { return frame }}
	frame = 5
	stop.StopMusic()
	frame = 10
	stop.PlayMusic("A")
	frame = 30
	stop.StopMusic()
	if segs := stop.songSegments(frameMs, endFrame); len(segs) != 1 || segs[0] != (songSegment{url: "A", startMs: 500, trimMs: 1000}) {
		t.Errorf("song then stop: %+v, want one {A 500 1000}", stop.songSegments(frameMs, endFrame))
	}
}

// TestAudioCaptureSFXPlacements pins SFX/shout capture: every PlaySFX + PlayShout
// becomes a placement at its frame × frameMs, in fire order.
func TestAudioCaptureSFXPlacements(t *testing.T) {
	frame := 0
	m := &audioCapture{frameRef: func() int { return frame }}
	frame = 4
	m.PlaySFX("base/sounds/general/sfx-stab", 0)
	frame = 9
	m.PlayShout("base/sounds/general/objection")
	got := m.sfxPlacements(50)
	want := []sfxPlacement{
		{base: "base/sounds/general/sfx-stab", delayMs: 200},
		{base: "base/sounds/general/objection", delayMs: 450},
	}
	if len(got) != len(want) {
		t.Fatalf("sfxPlacements = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("placement %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDownloadTempAudio covers the soundtrack fetch: a 200 writes the bytes to a temp
// file, and a non-200 is an error (so the caller degrades to the silent video) rather
// than a half-written file.
func TestDownloadTempAudio(t *testing.T) {
	body := []byte("ID3fake-audio-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	path, err := downloadTempAudio(srv.URL)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer os.Remove(path)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("temp file = %q, want %q", got, body)
	}

	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv404.Close()
	if _, err := downloadTempAudio(srv404.URL); err == nil {
		t.Error("downloadTempAudio should error on HTTP 404, not return a partial file")
	}
}
