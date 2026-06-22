package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestMusicCaptureFirstSong pins the soundtrack pick: the FIRST real track and its
// start delay (frame × frameMs), skipping a leading stop, and ok=false when no music
// ever played (only stops / empty cues).
func TestMusicCaptureFirstSong(t *testing.T) {
	frame := 0
	m := &musicCapture{frameRef: func() int { return frame }}

	if _, _, ok := m.firstSong(50); ok {
		t.Error("firstSong with no cues should be ok=false")
	}

	frame = 0
	m.StopMusic() // a leading stop is skipped
	frame = 10
	m.PlayMusic("https://cdn/song.opus") // the primary bed
	frame = 20
	m.PlayMusic("https://cdn/second.opus") // a later change isn't slice-1's pick
	url, delay, ok := m.firstSong(50)
	if !ok || url != "https://cdn/song.opus" || delay != 10*50 {
		t.Errorf("firstSong = (%q, %d, %v), want (song.opus, 500, true)", url, delay, ok)
	}

	only := &musicCapture{frameRef: func() int { return 0 }}
	only.StopMusic()
	only.PlayMusic("") // empty url = a stop, never a track
	if _, _, ok := only.firstSong(50); ok {
		t.Error("firstSong with only stops should be ok=false")
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
