package render

import (
	"context"
	"log"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

const (
	// minLoopWindowSec is the shortest loop-end minus loop-start interval (#48)
	// we'll arm. A narrower window could be skipped in its entirety by a single
	// idle Frame() gap (~500 ms default worst case), so a 1-second floor refuses
	// windows shorter than double that. Authors who need sub-second precision
	// loops (techno/trance stabs) should use a DAW's own seamless-loop export,
	// not sidecar points — this is for area ambience and courtroom tracks.
	minLoopWindowSec = 1.0

	// loopMetaCacheMaxAge is how long a conclusively-missing sidecar (fetch 404,
	// parse failure) stays cached (hard rule #6: never re-probe a 404 inside TTL).
	// Most tracks will never have a sidecar, so this must be long enough that a
	// short area loop doesn't spam the network, but short enough that a newly-
	// uploaded sidecar doesn't stay invisible for an entire session. 5 minutes
	// balances both: a 10-second area loop triggers at most 30 fetches across
	// that window (acceptable), and a fixed sidecar appears within one area-
	// switch cycle on a typical server.
	loopMetaCacheMaxAge = 5 * time.Minute

	// loopMetaCacheCap bounds the sidecar cache (hard rule #4). At 256 entries,
	// even a server with 200 music tracks (large) plus 50+ areas (implausible)
	// fits; eviction is FIFO (oldest entry dropped when cap is hit).
	loopMetaCacheCap = 256

	// unknownDurationSec is ClampLoopPoints' "length unknown" sentinel.
	unknownDurationSec = -1.0
)

// loopMeta records a track's loop points (when found) or the fact that none exist
// (conclusively missing: fetch 404, parse failure, or direct http(s) URL). The
// cache is keyed by the music URL (full, host-included) and consulted exactly once
// per playDecoded call — never again while the same stream is live. Entries expire
// after loopMetaCacheMaxAge (hard rule #6).
type loopMeta struct {
	points    courtroom.LoopPoints // zero value when none found or fetch/parse failed
	found     bool                 // true = real points; false = conclusively missing
	fetchedAt time.Time            // when this entry was written; drives TTL expiry
}

// loopPointsFor returns a looping song's custom loop points (#48), or ok=false
// when it has none worth arming. Called once per startMusic, BEFORE the song
// plays: SDL3_mixer takes loop points as play options and then loops gaplessly
// at the decoder level. On a cache miss it fetches+parses the sidecar
// synchronously (first playthrough). durSec is the song's length (haveDur false
// when unknown); startSec is where playback will begin. Render thread only.
func (a *Audio) loopPointsFor(url string, data []byte, durSec float64, haveDur bool, startSec float64) (courtroom.LoopPoints, bool) {
	// Check cache first (hit = use immediately; miss = trigger fetch).
	meta, cached := a.loopMeta[url]
	if cached {
		// Expire stale entries (hard rule #6: never re-probe inside TTL, but DO
		// re-fetch after expiry so a newly-uploaded sidecar eventually appears).
		if time.Since(meta.fetchedAt) > loopMetaCacheMaxAge {
			delete(a.loopMeta, url)
			cached = false
		}
	}

	if !cached {
		// Cache miss: fetch and parse synchronously so we can arm on the FIRST
		// playthrough. The .txt file is small (typically <100 bytes) and the music
		// bytes are already in memory for sample rate sniffing, so this is fast.
		log.Printf("render: loop cache MISS for %q, fetching synchronously", url)
		if !haveDur {
			durSec = unknownDurationSec
		}
		meta = a.fetchLoopMeta(url, data, durSec)

		if len(a.loopMeta) >= loopMetaCacheCap {
			// Evict one entry. We don't have an order slice, so evict arbitrarily.
			for k := range a.loopMeta {
				delete(a.loopMeta, k)
				break
			}
		}
		a.loopMeta[url] = meta
	}

	// No points found (conclusively missing): silent no-op. Most tracks take
	// this path (few have sidecars); logging would be spam.
	if !meta.found {
		return courtroom.LoopPoints{}, false
	}

	// Refuse to arm if the window is too narrow, if loop_start >= loop_end (the
	// parser already clamped this, but an all-zero LoopPoints also satisfies
	// found=true when a DRO .ini exists but has no loop keys), or if no loop end
	// was ever defined.
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

// fetchLoopMeta fetches and parses a track's sidecar(s) synchronously, returning
// the result immediately. Called by loopPointsFor on cache miss so loop points work
// on the first playthrough. For AO .txt sidecars, the sample rate is sniffed from `data`
// (the track's own bytes, already in memory); for DRO .ini, the folder convention is
// applied. Direct http(s) URLs (query strings present) never get a sidecar probe.
// durSec is the song's length (unknownDurationSec when unknown), used to clamp
// loop_end.
func (a *Audio) fetchLoopMeta(url string, data []byte, durSec float64) loopMeta {
	// Direct URLs (with query strings) are never probed for sidecars — they're
	// off-domain redirects or parameterized CDN links, not asset-origin paths.
	if courtroom.IsDirectMusicURL(url) {
		return loopMeta{found: false, fetchedAt: time.Now()}
	}

	// Sniff sample rate from the track's own bytes (already in memory from the
	// music fetch). Needed for both AO .txt (samples → seconds) and DRO .ini
	// (samples → seconds). Opus/unknown defaults to 48000 (best-effort).
	sampleRate, ok := assets.SniffAudioSampleRate(data)
	if !ok {
		log.Printf("render: loop sidecar fetch for %q: sample rate sniff failed, defaulting to 48000 Hz", url)
		sampleRate = 48000
	} else {
		log.Printf("render: loop sidecar fetch for %q: sniffed sample rate = %d Hz", url, sampleRate)
	}

	// DRO .ini first: the folder convention (courtroom.DROLoopManifestURL), filename
	// matched case-insensitively against the manifest section headers. A matching
	// section wins outright and the per-track AO .txt sidecar is never probed.
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

	// AO .txt fallback: same path, ".txt" appended. Reached only when DRO has no
	// matching section (no manifest, no match, or fetch/parse failed).
	txtURL := url + ".txt"
	if data, err := a.mgr.FetchRawLayered(context.Background(), txtURL); err == nil && len(data) > 0 {
		if points, ok := courtroom.ParseAOLoopSidecar(data, sampleRate); ok {
			start, end := courtroom.ClampLoopPoints(points.StartSec, points.EndSec, durSec)
			points.StartSec = start
			points.EndSec = end
			return loopMeta{points: points, found: true, fetchedAt: time.Now()}
		}
	}

	// Both DRO and AO conclusively missing (or parse failed): cache that fact.
	return loopMeta{found: false, fetchedAt: time.Now()}
}
