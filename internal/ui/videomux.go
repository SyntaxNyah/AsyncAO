package ui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/videoenc"
)

// Video audio mux (#99): a video export is silent at capture time; this gives it the
// scene's soundtrack afterward — the primary background song (slice 1) PLUS each
// one-shot SFX / interjection cry at its moment (slice 2). During capture an
// audioCapture sink records every music change and SFX with the export-frame it
// fired on (playing nothing — the export runs faster than realtime, so live audio
// would be garbled). finishVideoExport then assembles those into timed clips and has
// ffmpeg mix them into the finished silent video (video stream copied). ffmpeg
// decodes everything (spec §8 — no Go audio decode; it's an external process). Blips
// stay excluded (hundreds per scene = an unmanageable filter graph), and mid-scene
// SONG CHANGES still take only the first track (multi-song windows are a later
// slice). ANY failure degrades — full mix → music-only bed → silent — so the export
// never breaks.

// musicCue / sfxCue are captured audio events: what fired and the export frame it
// fired on (× frameMs = its time in the video). A music url of "" marks a stop.
type musicCue struct {
	url   string
	frame int
}
type sfxCue struct {
	base  string // extensionless asset base — resolved to bytes via Manager.ResolveRaw
	frame int
}

// audioCapture is the video export's courtroom.AudioSink: it plays nothing and just
// records music + SFX/shout cues for the post-capture mux. Driven by the export room
// on the render thread only, so the slice appends need no lock.
type audioCapture struct {
	frameRef func() int // current export frame (captured count) at call time
	music    []musicCue
	sfx      []sfxCue
}

func (m *audioCapture) PlayMusic(url string) {
	m.music = append(m.music, musicCue{url: url, frame: m.frameRef()})
}
func (m *audioCapture) StopMusic() { m.music = append(m.music, musicCue{url: "", frame: m.frameRef()}) }
func (m *audioCapture) PlayShout(base string) {
	m.sfx = append(m.sfx, sfxCue{base: base, frame: m.frameRef()})
}
func (m *audioCapture) PlaySFX(base string, _ time.Duration) {
	m.sfx = append(m.sfx, sfxCue{base: base, frame: m.frameRef()})
}
func (m *audioCapture) PlayBlip(string)  {}
func (m *audioCapture) SetBlipScale(int) {}

// firstSong returns the first real track + its start delay (frame × frameMs), or
// ok=false if no music played. Slice-1 bed; multi-song windows are a later slice.
func (m *audioCapture) firstSong(frameMs int) (url string, delayMs int, ok bool) {
	for _, c := range m.music {
		if c.url != "" {
			return c.url, c.frame * frameMs, true
		}
	}
	return "", 0, false
}

// sfxPlacement is one captured SFX resolved to its time in the video.
type sfxPlacement struct {
	base    string
	delayMs int
}

// sfxPlacements snapshots the captured SFX cues as (base, delayMs) — called on the
// render thread before the off-thread mux, so the goroutine never touches the sink.
func (m *audioCapture) sfxPlacements(frameMs int) []sfxPlacement {
	out := make([]sfxPlacement, 0, len(m.sfx))
	for _, c := range m.sfx {
		out = append(out, sfxPlacement{base: c.base, delayMs: c.frame * frameMs})
	}
	return out
}

const (
	// maxMusicBytes caps a downloaded soundtrack so a giant or duff URL can't fill the
	// disk (hard rule §17.4). ~40 MB is many minutes of opus/mp3.
	maxMusicBytes = 40 << 20
	// musicHTTPTimeout bounds the download so a dead host can't stall the export's
	// finish goroutine forever.
	musicHTTPTimeout = 30 * time.Second
	// maxAudioClips bounds the ffmpeg mux inputs (hard rule §17.4): a frantic scene can
	// fire dozens of SFX and one -i per placement would build an unwieldy command. Past
	// this many timed clips (music bed + SFX) later SFX are dropped from the mix.
	maxAudioClips = 64
)

// downloadTempAudio fetches url to a temp file and returns its path (caller removes
// it). OFF the render thread only — it does blocking network + disk I/O. Bounded by
// size and timeout; any failure is returned so the caller degrades.
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

// writeTempBytes writes already-fetched bytes (a resolved SFX) to a temp file and
// returns its path (caller removes it).
func writeTempBytes(data []byte) (string, error) {
	f, err := os.CreateTemp("", "asyncao-sfx-*")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	closeErr := f.Close()
	if werr != nil || closeErr != nil {
		os.Remove(f.Name())
		if werr != nil {
			return "", werr
		}
		return "", closeErr
	}
	return f.Name(), nil
}

// muxSceneAudio (off-thread) assembles the captured music + SFX into timed clips and
// muxes them into the finished silent video at silentPath, writing the result beside
// it as "<name>-audio.<ext>" and removing the silent original on success. Returns the
// final path + true, or ("", false) to keep the silent video. Degrades in steps: full
// mix (music + SFX) → music-only bed → silent, so the most important audio survives.
// mgr.ResolveRaw resolves each SFX base to its bytes (the same candidate logic the
// render path uses; off-thread-safe — the archive exporter resolves the same way).
// Touches no App state, so it's race-free on the finish goroutine.
func muxSceneAudio(mgr *assets.Manager, silentPath, musicURL string, musicDelayMs int, sfx []sfxPlacement, format videoenc.Format) (string, bool) {
	var temps []string
	defer func() {
		for _, t := range temps {
			os.Remove(t)
		}
	}()

	var clips []videoenc.AudioClip
	if musicURL != "" {
		if p, err := downloadTempAudio(musicURL); err == nil {
			temps = append(temps, p)
			clips = append(clips, videoenc.AudioClip{Path: p, DelayMs: musicDelayMs}) // the bed is always clips[0]
		}
	}
	hasMusic := len(clips) == 1

	resolved := map[string]string{} // base → temp path ("" caches a miss; dedupe resolution)
	for _, s := range sfx {
		if len(clips) >= maxAudioClips {
			break
		}
		path, seen := resolved[s.base]
		if !seen {
			if _, data, ok := mgr.ResolveRaw(s.base, assets.AssetTypeSFX); ok && len(data) > 0 {
				if p, err := writeTempBytes(data); err == nil {
					path = p
					temps = append(temps, p)
				}
			}
			resolved[s.base] = path
		}
		if path != "" {
			clips = append(clips, videoenc.AudioClip{Path: path, DelayMs: s.delayMs})
		}
	}
	if len(clips) == 0 {
		return "", false
	}

	ext := videoenc.FormatExt(format)
	finalPath := strings.TrimSuffix(silentPath, "."+ext) + "-audio." + ext
	musicCount := 0
	if hasMusic {
		musicCount = 1
	}

	// SFX present → the multi-input mix (music bed + each SFX). On failure, degrade to
	// the music bed if we have one, else keep the silent video.
	if len(clips) > musicCount {
		if err := videoenc.MuxAudioMix(silentPath, clips, finalPath, format); err == nil {
			os.Remove(silentPath)
			return finalPath, true
		}
		os.Remove(finalPath)
		if !hasMusic {
			return "", false
		}
	}
	// Music-only bed (no SFX, or the mix failed back to just the music).
	if err := videoenc.MuxAudioBed(silentPath, clips[0].Path, finalPath, clips[0].DelayMs, format); err != nil {
		os.Remove(finalPath)
		return "", false
	}
	os.Remove(silentPath)
	return finalPath, true
}
