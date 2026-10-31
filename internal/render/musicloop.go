package render

import (
	"context"
	"log"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

const (
	// minLoopWindowSec is the shortest window: a narrower one could be skipped by one idle Frame gap (~500 ms).
	minLoopWindowSec = 1.0

	// loopMetaCacheMaxAge is how long a missing sidecar stays cached (rule 6); new uploads must appear in a session.
	loopMetaCacheMaxAge = 5 * time.Minute

	// loopMetaCacheCap bounds the sidecar cache.
	loopMetaCacheCap = 256

	// loopFetchCap bounds concurrent sidecar fetches; past it a song starts without waiting.
	loopFetchCap = 4

	// loopFetchWait is how long a looping song waits for its sidecars; the previous song keeps playing meanwhile.
	loopFetchWait = time.Second

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

// holdForLoopMeta keeps a looping song's bytes until its sidecars are known, since SDL3_mixer takes loop points
// as play options and the fetch must not block the render thread. False means start now.
func (a *Audio) holdForLoopMeta(url string, data []byte, p pendingPlay) bool {
	if !p.loop || courtroom.IsDirectMusicURL(url) {
		return false
	}
	if meta, cached := a.loopMeta[url]; cached && time.Since(meta.fetchedAt) <= loopMetaCacheMaxAge {
		return false
	}
	if _, fetching := a.loopFetches[url]; !fetching {
		if len(a.loopFetches) >= loopFetchCap {
			return false
		}
		a.loopFetches[url] = struct{}{}
		go func() {
			a.loopResults <- loopMetaResult{url: url, meta: fetchLoopMeta(a.loopFetchCtx, a.fetchSidecar, url, data)}
			a.wake()
		}()
	}
	p.heldBytes, p.holdUntil = data, time.Now().Add(loopFetchWait)
	a.pending[url] = p
	time.AfterFunc(loopFetchWait, a.wake) // a parked frame loop must still start the song on time
	return true
}

// stepHeldMusic stores finished sidecar fetches and starts held songs whose sidecars arrived or whose wait ran out.
func (a *Audio) stepHeldMusic() {
	for {
		select {
		case result := <-a.loopResults:
			delete(a.loopFetches, result.url)
			a.storeLoopMeta(result)
		default:
			a.startDueHeldMusic()
			return
		}
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

func (a *Audio) startDueHeldMusic() {
	for url, p := range a.pending {
		if p.heldBytes == nil {
			continue
		}
		if _, fetching := a.loopFetches[url]; fetching && time.Now().Before(p.holdUntil) {
			continue
		}
		delete(a.pending, url)
		a.startMusic(url, p.heldBytes, p.loop, p.effects, p.seekSec)
		return // at most one music entry is ever pending
	}
}

// loopPointsFor returns cached loop points or ok=false, without fetching; a late fetch serves the next play.
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
