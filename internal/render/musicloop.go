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

	unknownDurationSec = -1.0
)

// loopMeta holds a track's loop points or the fact that none exist, keyed by full music URL (rule 6).
type loopMeta struct {
	points    courtroom.LoopPoints // zero value when none found or fetch/parse failed
	found     bool                 // true = real points; false = conclusively missing
	fetchedAt time.Time            // when this entry was written; drives TTL expiry
}

// loopPointsFor returns loop points or ok=false, before play, as SDL3_mixer takes them as play options.
func (a *Audio) loopPointsFor(url string, data []byte, durSec float64, haveDur bool, startSec float64) (courtroom.LoopPoints, bool) {
	meta, cached := a.loopMeta[url]
	if cached {
		// Re-fetch after expiry, so a new sidecar eventually appears.
		if time.Since(meta.fetchedAt) > loopMetaCacheMaxAge {
			delete(a.loopMeta, url)
			cached = false
		}
	}

	if !cached {
		// Fetch synchronously to loop on the first playthrough; the .txt is small and the bytes are in memory.
		log.Printf("render: loop cache MISS for %q, fetching synchronously", url)
		if !haveDur {
			durSec = unknownDurationSec
		}
		meta = a.fetchLoopMeta(url, data, durSec)

		if len(a.loopMeta) >= loopMetaCacheCap {
			// No order slice, so evict arbitrarily.
			for k := range a.loopMeta {
				delete(a.loopMeta, k)
				break
			}
		}
		a.loopMeta[url] = meta
	}

	// Most tracks take this path, so logging would be spam.
	if !meta.found {
		return courtroom.LoopPoints{}, false
	}

	// Refuse a narrow or empty window: an all-zero LoopPoints is found=true when a DRO .ini has no loop keys.
	p := meta.points
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

// fetchLoopMeta fetches and parses a track's sidecars; durSec clamps loop_end (unknownDurationSec when unknown).
func (a *Audio) fetchLoopMeta(url string, data []byte, durSec float64) loopMeta {
	// Direct URLs are off-domain redirects or CDN links, not asset-origin paths.
	if courtroom.IsDirectMusicURL(url) {
		return loopMeta{found: false, fetchedAt: time.Now()}
	}

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
			droData, err := a.mgr.FetchRawLayered(context.Background(), droURL)
			if err == nil && len(droData) > 0 {
				if droMeta, ok := courtroom.ParseDROLoopSidecar(droData, relPath, sampleRate); ok {
					// DRO play_once suppresses arming even when the wire said loop=true.
					if droMeta.PlayOnce {
						return loopMeta{found: true, fetchedAt: time.Now()}
					}
					start, end := courtroom.ClampLoopPoints(droMeta.Loop.StartSec, droMeta.Loop.EndSec, durSec)
					points := courtroom.LoopPoints{StartSec: start, EndSec: end, HaveEnd: droMeta.Loop.HaveEnd}
					log.Printf("render: DRO .ini loop points for %q: start=%.2fs end=%.2fs haveEnd=%v", url, points.StartSec, points.EndSec, points.HaveEnd)
					return loopMeta{points: points, found: true, fetchedAt: time.Now()}
				}
			}
		}
	}

	// AO .txt: same path with ".txt", reached only when DRO has no matching section.
	txtURL := url + ".txt"
	if data, err := a.mgr.FetchRawLayered(context.Background(), txtURL); err == nil && len(data) > 0 {
		if points, ok := courtroom.ParseAOLoopSidecar(data, sampleRate); ok {
			start, end := courtroom.ClampLoopPoints(points.StartSec, points.EndSec, durSec)
			points.StartSec = start
			points.EndSec = end
			return loopMeta{points: points, found: true, fetchedAt: time.Now()}
		}
	}

	return loopMeta{found: false, fetchedAt: time.Now()}
}
