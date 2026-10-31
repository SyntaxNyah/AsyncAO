package ui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/videoenc"
)

// Video audio mux: an export is silent at capture time, so an audioCapture sink records music changes and SFX with
// their export frame (playing nothing, since the export runs faster than realtime), and finishVideoExport has ffmpeg
// mix them into the finished video (video stream copied). Blips are excluded; any failure degrades from full mix to
// music-only bed to silent.

// musicCue / sfxCue are captured audio events: what fired and the export frame it fired on. A music url "" is a stop.
type musicCue struct {
	url   string
	frame int
}
type sfxCue struct {
	base  string // extensionless asset base — resolved to bytes via Manager.ResolveRaw
	frame int
}

// audioCapture is the video export's courtroom.AudioSink: it plays nothing and records music and SFX/shout cues for the
// mux. It is driven on the render thread only, so the slice appends need no lock.
// droppedAtCapture counts cues turned away by the per-list maxAudioClips guard; finishVideoExport reports it.
type audioCapture struct {
	frameRef         func() int // current export frame (captured count) at call time
	music            []musicCue
	sfx              []sfxCue
	droppedAtCapture int
}

// The capture lists are each bounded by maxAudioClips (hard rule 4). This is a coarse guard; muxSceneAudio applies the
// authoritative combined cap.

// PlayMusic records the track cue; the loop/effects flags don't affect the captured soundtrack and are ignored.
func (m *audioCapture) PlayMusic(url string, _ bool, _ int) {
	if len(m.music) >= maxAudioClips {
		m.droppedAtCapture++
		return
	}
	m.music = append(m.music, musicCue{url: url, frame: m.frameRef()})
}
func (m *audioCapture) StopMusic() {
	if len(m.music) >= maxAudioClips {
		m.droppedAtCapture++
		return
	}
	m.music = append(m.music, musicCue{url: "", frame: m.frameRef()})
}
func (m *audioCapture) PlayShout(base string) {
	if len(m.sfx) >= maxAudioClips {
		m.droppedAtCapture++
		return
	}
	m.sfx = append(m.sfx, sfxCue{base: base, frame: m.frameRef()})
}
func (m *audioCapture) PlaySFX(base string, _ time.Duration) {
	if len(m.sfx) >= maxAudioClips {
		m.droppedAtCapture++
		return
	}
	m.sfx = append(m.sfx, sfxCue{base: base, frame: m.frameRef()})
}
func (m *audioCapture) PlayBlip(string)  {}
func (m *audioCapture) SetBlipScale(int) {}

// frameToMs converts a captured export frame to its millisecond offset in the finished video.
// It multiplies before dividing: a truncated frame*(1000/fps) offset drifts linearly with frame count, desyncing long
// exports. frame*1000 overflows int32, so this assumes a 64-bit int.
func frameToMs(frame, fps int) int {
	if fps < 1 {
		fps = 1
	}
	return frame * 1000 / fps
}

// songSegment is one music window resolved to video time: the track URL, start (ms) and optional trim (0 = to the end).
type songSegment struct {
	url     string
	startMs int
	trimMs  int
}

// songSegments turns the captured music cues into ordered, non-overlapping windows: each track plays until the next cue
// (a song change or a stop) or, for the last track, to endFrame. A stop bounds the previous window and adds none.
// The trim is the difference of two frameToMs offsets so window boundaries stay exact.
func (m *audioCapture) songSegments(fps, endFrame int) []songSegment {
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
		trim := frameToMs(end, fps) - frameToMs(c.frame, fps)
		if end == endFrame {
			trim = 0 // runs to the video end → no trim; apad fills any tail
		}
		segs = append(segs, songSegment{url: c.url, startMs: frameToMs(c.frame, fps), trimMs: trim})
	}
	return segs
}

// sfxPlacement is one captured SFX resolved to its time in the video.
type sfxPlacement struct {
	base    string
	delayMs int
}

// sfxPlacements snapshots the captured SFX cues as (base, delayMs); called on the render thread before the off-thread
// mux, so the goroutine never touches the sink.
func (m *audioCapture) sfxPlacements(fps int) []sfxPlacement {
	out := make([]sfxPlacement, 0, len(m.sfx))
	for _, c := range m.sfx {
		out = append(out, sfxPlacement{base: c.base, delayMs: frameToMs(c.frame, fps)})
	}
	return out
}

const (
	// maxMusicBytes caps a downloaded soundtrack (hard rule 4).
	maxMusicBytes = 40 << 20
	// musicHTTPTimeout bounds the download so a dead host cannot stall the export's finish goroutine.
	musicHTTPTimeout = 30 * time.Second
	// maxAudioClips bounds the ffmpeg mux inputs; past it, later SFX are dropped from the mix.
	maxAudioClips = 64
)

// downloadTempAudio fetches url to a temp file and returns its path (the caller removes it).
// It does blocking network and disk I/O, so it runs off the render thread only.
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

// fetchTempAudio lands a music track in a temp file for the mux. http(s) URLs use the bounded direct download; other
// schemes (local:// from a mounted folder or archive) go through Manager.FetchRaw, which the raw client cannot do.
// maxMusicBytes applies on both paths.
func fetchTempAudio(mgr *assets.Manager, url string) (string, error) {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return downloadTempAudio(url)
	}
	ctx, cancel := context.WithTimeout(context.Background(), musicHTTPTimeout)
	defer cancel()
	data, err := mgr.FetchRaw(ctx, url)
	if err != nil {
		return "", err
	}
	if len(data) > maxMusicBytes {
		return "", fmt.Errorf("track exceeds the music size cap (%d bytes)", maxMusicBytes)
	}
	return writeTempBytes(data)
}

// writeTempBytes writes already-fetched bytes (a resolved SFX) to a temp file and returns its path (caller removes it).
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

// muxSceneAudio assembles the captured music windows and SFX into timed clips and muxes them into the silent video at
// silentPath, writing "<name>-audio.<ext>" beside it and removing the silent original on success.
// It returns the final path and true, or ("", false) to keep the silent video. Each unique music url downloads once;
// SFX bases resolve via mgr.ResolveRaw. It touches no App state, so it is safe on the finish goroutine.
func muxSceneAudio(mgr *assets.Manager, silentPath string, songs []songSegment, sfx []sfxPlacement, format videoenc.Format) (string, bool) {
	var temps []string
	defer func() {
		for _, t := range temps {
			os.Remove(t)
		}
	}()

	// Music segments first so clips[0] is the first song for the degrade path; each unique track downloads once.
	var clips []videoenc.AudioClip
	dl := map[string]string{} // url → temp path ("" caches a failed download)
	for _, s := range songs {
		if len(clips) >= maxAudioClips {
			break
		}
		path, seen := dl[s.url]
		if !seen {
			if p, err := fetchTempAudio(mgr, s.url); err == nil {
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

	// SFX/shouts are one-shot at their time; each unique base resolves once.
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

	// One untrimmed song and nothing else uses the single-input bed; anything else uses the multi-input mix, and a
	// failure degrades to the first song as a plain bed, else silent.
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
