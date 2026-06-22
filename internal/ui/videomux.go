package ui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/videoenc"
)

// Video audio mux (#99 slice 1): a video export is silent at capture time; this adds
// the scene's PRIMARY background song as the soundtrack afterward. During capture a
// musicCapture sink records each music change with the export-frame it fired on
// (playing nothing — the export runs faster than realtime, so live audio would be
// garbled). finishVideoExport then downloads that song and has ffmpeg mux it into
// the finished silent video (video stream copied, song delayed to its start and
// padded to the video's length). ffmpeg does all the audio decoding (spec §8 — no Go
// audio decode; ffmpeg is an external process). Music-only for now — blips are far
// too many to place, and SFX / multi-song windows are later slices. Any failure
// degrades to the already-written silent video, so the export never breaks.

// musicCue is one captured music change: the resolved track URL ("" = music stopped)
// and the export frame it fired on (× frameMs = its time in the video).
type musicCue struct {
	url   string
	frame int
}

// musicCapture is the video export's courtroom.AudioSink: it plays nothing and just
// records music changes (with their frame) for the post-capture mux. Driven by the
// export room on the render thread only, so the slice append needs no lock.
type musicCapture struct {
	frameRef func() int // current export frame (captured count) at call time
	cues     []musicCue
}

func (m *musicCapture) PlayMusic(url string) {
	m.cues = append(m.cues, musicCue{url: url, frame: m.frameRef()})
}
func (m *musicCapture) StopMusic()                    { m.cues = append(m.cues, musicCue{url: "", frame: m.frameRef()}) }
func (m *musicCapture) PlayShout(string)              {}
func (m *musicCapture) PlaySFX(string, time.Duration) {}
func (m *musicCapture) PlayBlip(string)               {}
func (m *musicCapture) SetBlipScale(int)              {}

// firstSong returns the first real track captured and its start delay in ms (frame ×
// frameMs), or ok=false if no music played. Slice 1 muxes this primary song as the
// bed; multi-song windows + SFX are later slices.
func (m *musicCapture) firstSong(frameMs int) (url string, delayMs int, ok bool) {
	for _, c := range m.cues {
		if c.url != "" {
			return c.url, c.frame * frameMs, true
		}
	}
	return "", 0, false
}

const (
	// maxMusicBytes caps a downloaded soundtrack so a giant or duff URL can't fill the
	// disk (hard rule §17.4). ~40 MB is many minutes of opus/mp3.
	maxMusicBytes = 40 << 20
	// musicHTTPTimeout bounds the download so a dead host can't stall the export's
	// finish goroutine forever.
	musicHTTPTimeout = 30 * time.Second
)

// downloadTempAudio fetches url to a temp file and returns its path (caller removes
// it). OFF the render thread only — it does blocking network + disk I/O. Bounded by
// size and timeout; any failure is returned so the caller degrades to the silent
// video.
func downloadTempAudio(url string) (string, error) {
	client := &http.Client{Timeout: musicHTTPTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	f, err := os.CreateTemp("", "asyncao-bed-*")
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxMusicBytes))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(f.Name())
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	return f.Name(), nil
}

// muxMusicBed (off-thread) downloads songURL and muxes it into the finished silent
// video at silentPath, writing the result to a sibling "<name>-audio.<ext>" and
// deleting the silent original on success. Returns the final path + true, or ("",
// false) to signal "keep the silent video" — ANY failure (dead URL, non-audio file,
// ffmpeg error) must NOT break the export. Writing to a sibling instead of replacing
// in place sidesteps Windows' "rename can't overwrite an existing file": on success
// the silent file is removed and the muxed file IS the deliverable; on failure the
// silent file is untouched. No App state is touched (it runs on a goroutine), so it
// stays race-free.
func muxMusicBed(silentPath, songURL string, delayMs int, format videoenc.Format) (string, bool) {
	tmp, err := downloadTempAudio(songURL)
	if err != nil {
		return "", false
	}
	defer os.Remove(tmp)
	ext := videoenc.FormatExt(format)
	finalPath := strings.TrimSuffix(silentPath, "."+ext) + "-audio." + ext
	if err := videoenc.MuxAudioBed(silentPath, tmp, finalPath, delayMs, format); err != nil {
		os.Remove(finalPath)
		return "", false
	}
	_ = os.Remove(silentPath) // the muxed file supersedes the silent original
	return finalPath, true
}
