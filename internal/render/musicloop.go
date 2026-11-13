package render

import (
	"context"
	"log"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/mix"
)

const (
	// minLoopWindowSec is the shortest window: a narrower one could be skipped by one idle Frame gap (~500 ms).
	minLoopWindowSec = 1.0

	// loopMetaCacheMaxAge is how long a missing sidecar stays cached (rule 6); new uploads must appear in a session.
	loopMetaCacheMaxAge = 5 * time.Minute

	// loopMetaCacheCap bounds the sidecar cache.
	loopMetaCacheCap = 256

	// loopFetchCap bounds concurrent sidecar fetches; past it a song plays without loop points.
	loopFetchCap = 4

	unknownDurationSec = -1.0
)

// sidecarFetcher fetches one complete URL, off the render thread.
type sidecarFetcher func(ctx context.Context, url string) ([]byte, error)

// loopMeta holds a track's loop points or the fact that none exist, keyed by full music URL (rule 6).
type loopMeta struct {
	points    courtroom.LoopPoints // zero value when none found or fetch/parse failed
	found     bool                 // true = real points; false = conclusively missing
	fetchedAt time.Time            // when this entry was written; drives TTL expiry
}

// loopMetaResult is one finished sidecar fetch.
type loopMetaResult struct {
	url  string
	meta loopMeta
}

// requestLoopMeta fetches a looping song's sidecars in the background; stepLoopMeta applies them on arrival.
func (a *Audio) requestLoopMeta(url string, data []byte) {
	if !courtroom.HasLoopSidecars(url) {
		return
	}
	if meta, cached := a.loopMeta[url]; cached && time.Since(meta.fetchedAt) <= loopMetaCacheMaxAge {
		return
	}
	if _, fetching := a.loopFetches[url]; fetching || len(a.loopFetches) >= loopFetchCap {
		return
	}
	a.loopFetches[url] = struct{}{}
	go func() {
		a.loopResults <- loopMetaResult{url: url, meta: fetchLoopMeta(a.loopFetchCtx, a.fetchSidecar, url, data)}
		a.wake()
	}()
}

// stepLoopMeta stores finished sidecar fetches and applies new loop points to the song playing.
func (a *Audio) stepLoopMeta() {
	for {
		select {
		case result := <-a.loopResults:
			delete(a.loopFetches, result.url)
			previous := a.loopMeta[result.url]
			a.storeLoopMeta(result)
			if result.meta.found && (!previous.found || previous.points != result.meta.points) {
				a.loopRearmURL = result.url
			}
		default:
			a.rearmLoopPoints()
			return
		}
	}
}

// rearmLoopPoints replays the live song from where it is, because SDL3_mixer takes loop points only at play time.
func (a *Audio) rearmLoopPoints() {
	url := a.loopRearmURL
	if url == "" {
		return
	}
	if url != a.musicURL || !a.musicLoop || a.music.audio == nil {
		a.loopRearmURL = ""
		return
	}
	// Replaying during a fade-in would restart the ramp from silence.
	if time.Now().Before(a.musicFadeInEnd) {
		return
	}
	a.loopRearmURL = ""
	pos, ok := a.music.track.Position()
	if !ok {
		return
	}
	durSec, haveDur := a.music.audio.Duration()
	points, ok := a.loopPointsFor(url, durSec, haveDur, pos)
	if !ok {
		return
	}
	err := a.music.track.Play(mix.PlayOptions{
		Loops: mix.LoopForever, StartSec: pos, LoopStartSec: points.StartSec, LoopEndSec: points.EndSec,
	})
	if err != nil {
		log.Printf("render: applying loop points to %q: %v", url, err)
	}
}

func (a *Audio) storeLoopMeta(result loopMetaResult) {
	if _, replacing := a.loopMeta[result.url]; !replacing && len(a.loopMeta) >= loopMetaCacheCap {
		// No order slice, so evict arbitrarily.
		for k := range a.loopMeta {
			delete(a.loopMeta, k)
			break
		}
	}
	a.loopMeta[result.url] = result.meta
}

// loopPointsFor returns cached loop points or ok=false, without fetching.
func (a *Audio) loopPointsFor(url string, durSec float64, haveDur bool, startSec float64) (courtroom.LoopPoints, bool) {
	meta, cached := a.loopMeta[url]
	// Most tracks take this path, so logging would be spam.
	if !cached || !meta.found {
		return courtroom.LoopPoints{}, false
	}
	if !haveDur {
		durSec = unknownDurationSec
	}

	// Refuse a narrow or empty window: an all-zero LoopPoints is found=true when a DRO .ini has no loop keys.
	p := meta.points
	p.StartSec, p.EndSec = courtroom.ClampLoopPoints(p.StartSec, p.EndSec, durSec)
	if !p.HaveEnd || p.EndSec-p.StartSec < minLoopWindowSec || p.StartSec >= p.EndSec {
		log.Printf("render: loop arm REFUSED (HaveEnd=%v start=%.2fs end=%.2fs)", p.HaveEnd, p.StartSec, p.EndSec)
		return courtroom.LoopPoints{}, false
	}
	// A resume that starts past the loop end would end the song at once.
	if startSec >= p.EndSec {
		log.Printf("render: loop arm REFUSED (start %.2fs is past the loop end %.2fs)", startSec, p.EndSec)
		return courtroom.LoopPoints{}, false
	}
	log.Printf("render: loop points start=%.2fs end=%.2fs for %q", p.StartSec, p.EndSec, url)
	return p, true
}

// fetchLoopMeta fetches and parses a track's sidecars, off the render thread.
func fetchLoopMeta(ctx context.Context, fetch sidecarFetcher, url string, data []byte) loopMeta {
	// Both sidecar formats convert samples to seconds; Opus or unknown defaults to 48000 (best effort).
	sampleRate, ok := assets.SniffAudioSampleRate(data)
	if !ok {
		log.Printf("render: loop sidecar fetch for %q: sample rate sniff failed, defaulting to 48000 Hz", url)
		sampleRate = 48000
	} else {
		log.Printf("render: loop sidecar fetch for %q: sniffed sample rate = %d Hz", url, sampleRate)
	}

	// DRO .ini first (courtroom.DROLoopManifestURL), case-insensitive; a matching section wins over AO .txt.
	if droURL, ok := courtroom.DROLoopManifestURL(url); ok {
		if relPath, ok := courtroom.MusicRelPath(url); ok {
			droData, err := fetch(ctx, droURL)
			if err == nil && len(droData) > 0 {
				if droMeta, ok := courtroom.ParseDROLoopSidecar(droData, relPath, sampleRate); ok {
					// DRO play_once suppresses arming even when the wire said loop=true.
					if droMeta.PlayOnce {
						return loopMeta{found: true, fetchedAt: time.Now()}
					}
					return loopMeta{points: droMeta.Loop, found: true, fetchedAt: time.Now()}
				}
			}
		}
	}

	// AO .txt: same path with ".txt", reached only when DRO has no matching section.
	txtURL := url + ".txt"
	if data, err := fetch(ctx, txtURL); err == nil && len(data) > 0 {
		if points, ok := courtroom.ParseAOLoopSidecar(data, sampleRate); ok {
			return loopMeta{points: points, found: true, fetchedAt: time.Now()}
		}
	}

	return loopMeta{found: false, fetchedAt: time.Now()}
}
