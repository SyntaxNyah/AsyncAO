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

// The capture lists (music/sfx) are each bounded by maxAudioClips at append time
// (hard rule §17.4 — no unbounded slice). It's a coarse per-list guard, not the
// exact mux budget: muxSceneAudio applies the authoritative COMBINED keep-first
// cap (music then SFX, break at maxAudioClips), so cues past the per-list bound
// are dead weight the mux would trim anyway — dropping them here just stops the
// slices growing on a runaway scene.

// PlayMusic records the track cue; the 2.9 loop/effects flags (#15) don't affect
// the captured soundtrack (each cue is a fixed window, mixed by ffmpeg), so they
// are accepted and ignored here.
func (m *audioCapture) PlayMusic(url string, _ bool, _ int) {
	if len(m.music) >= maxAudioClips {
		return
	}
	m.music = append(m.music, musicCue{url: url, frame: m.frameRef()})
}
func (m *audioCapture) StopMusic() {
	if len(m.music) >= maxAudioClips {
		return
	}
	m.music = append(m.music, musicCue{url: "", frame: m.frameRef()})
}
func (m *audioCapture) PlayShout(base string) {
	if len(m.sfx) >= maxAudioClips {
		return
	}
	m.sfx = append(m.sfx, sfxCue{base: base, frame: m.frameRef()})
}
func (m *audioCapture) PlaySFX(base string, _ time.Duration) {
	if len(m.sfx) >= maxAudioClips {
		return
	}
	m.sfx = append(m.sfx, sfxCue{base: base, frame: m.frameRef()})
}
func (m *audioCapture) PlayBlip(string)  {}
func (m *audioCapture) SetBlipScale(int) {}

// songSegment is one music window resolved to video time: the track URL, when it
// starts (ms), and an optional trim (ms; 0 = play to the end, apad fills the tail).
type songSegment struct {
	url     string
	startMs int
	trimMs  int
}

// songSegments turns the captured music cues into ordered, non-overlapping windows:
// each track plays from its cue until the NEXT cue (a song change OR a stop), or — for
// the last track — to the video's end (endFrame). A stop cue bounds the previous
// window and contributes none of its own. frameMs converts frames to ms. A single
// song collapses to one untrimmed segment (the slice-1 bed); changes become several.
func (m *audioCapture) songSegments(frameMs, endFrame int) []songSegment {
	var segs []songSegment
	for i, c := range m.music {
		if c.url == "" {
			continue // a stop only bounds the previous window (the end calc below)
		}
		end := endFrame
		if i+1 < len(m.music) {
			end = m.music[i+1].frame
		}
		if end <= c.frame {
			continue // zero/negative window (back-to-back cues) — skip
		}
		trim := (end - c.frame) * frameMs
		if end == endFrame {
			trim = 0 // runs to the video end → no trim; apad fills any tail
		}
		segs = append(segs, songSegment{url: c.url, startMs: c.frame * frameMs, trimMs: trim})
	}
	return segs
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

// muxSceneAudio (off-thread) assembles the captured music windows + SFX into timed
// clips and muxes them into the finished silent video at silentPath, writing the
// result beside it as "<name>-audio.<ext>" and removing the silent original on
// success. Returns the final path + true, or ("", false) to keep the silent video.
// Degrades in steps: full mix (all songs + SFX) → first-song bed → silent, so the
// most important audio survives. Music tracks download once per UNIQUE url (a song
// reused in two windows shares the file); SFX bases resolve to bytes via
// mgr.ResolveRaw (the render path's candidate logic; off-thread-safe — the archive
// exporter resolves the same way). Touches no App state, so it's race-free on the
// finish goroutine.
func muxSceneAudio(mgr *assets.Manager, silentPath string, songs []songSegment, sfx []sfxPlacement, format videoenc.Format) (string, bool) {
	var temps []string
	defer func() {
		for _, t := range temps {
			os.Remove(t)
		}
	}()

	// Music segments first (so clips[0] is the first song for the degrade path): one
	// trimmed clip per window, each unique track downloaded once.
	var clips []videoenc.AudioClip
	dl := map[string]string{} // url → temp path ("" caches a failed download)
	for _, s := range songs {
		if len(clips) >= maxAudioClips {
			break
		}
		path, seen := dl[s.url]
		if !seen {
			if p, err := downloadTempAudio(s.url); err == nil {
				path = p
				temps = append(temps, p)
			}
			dl[s.url] = path
		}
		if path != "" {
			clips = append(clips, videoenc.AudioClip{Path: path, DelayMs: s.startMs, TrimMs: s.trimMs})
		}
	}
	musicClips := len(clips)

	// SFX/shouts: one-shot at their time; resolve each unique base once.
	resolved := map[string]string{}
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

	// One untrimmed song and nothing else → the simple single-input bed. Anything
	// else (multiple songs, a trimmed song, or SFX) → the multi-input mix; on failure
	// degrade to the first song as a plain bed, else silent.
	simpleBed := musicClips == 1 && len(clips) == 1 && clips[0].TrimMs == 0
	if !simpleBed {
		if err := videoenc.MuxAudioMix(silentPath, clips, finalPath, format); err == nil {
			os.Remove(silentPath)
			return finalPath, true
		}
		os.Remove(finalPath)
		if musicClips == 0 {
			return "", false
		}
	}
	bed := clips[0] // the first music segment
	if err := videoenc.MuxAudioBed(silentPath, bed.Path, finalPath, bed.DelayMs, format); err != nil {
		os.Remove(finalPath)
		return "", false
	}
	os.Remove(silentPath)
	return finalPath, true
}
