package render

import (
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

// loopArm holds the state for ONE armed loop-back seek: the stream it applies to,
// the loop window, and the wall-clock deadline when advanceLoopBack will fire the
// seek. Only one loopArm exists at a time (new playDecoded nils it); it lives on
// the Audio struct and is driven by Frame.
type loopArm struct {
	url      string    // the track this arm applies to; must match a.musicURL or it's stale
	start    float64   // loop_start in seconds
	end      float64   // loop_end in seconds
	deadline time.Time // wall-clock time when the loop-back seek fires (now + end seconds)
}

// armLoopFromCache attempts to arm a loop-back seek for the given track (#48).
// Called exactly once per playDecoded (only when loop=true), after the stream is
// already playing. If a cached sidecar exists and parsed successfully, and the
// window is wide enough, and we haven't already passed the loop-end point, an
// arm is set; advanceLoopBack (called every Frame) will fire the seek when the
// deadline arrives. If no sidecar is cached, a background fetch is triggered
// (exactly once per URL per cache TTL). Render thread only.
func (a *Audio) armLoopFromCache(url string, data []byte) {
	// Check cache first (hit = apply immediately; miss = trigger fetch).
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
		// Cache miss: trigger a background fetch. fetchLoopMetaOnce runs async and
		// writes the result (found or conclusively-missing) to a.loopMetaRes; the
		// next pollLoopMeta (called every Frame) drains it and writes the cache.
		// This playDecoded proceeds without arming; the NEXT play of this URL (or
		// a later loop iteration on a long track) will find the cache populated.
		go a.fetchLoopMetaOnce(url, data)
		return
	}

	// Cache hit, but no points found (conclusively missing): silent no-op. Most
	// tracks will hit this path (few have sidecars); logging would be spam.
	if !meta.found {
		return
	}

	// Real points exist. Refuse to arm if the window is too narrow (would be
	// skipped by a single idle Frame() gap) or if loop_start >= loop_end (the
	// parser already clamped this, but an all-zero LoopPoints also satisfies
	// found=true when a DRO .ini exists but has no loop keys — belt-and-braces).
	// Also refuse if HaveEnd is false (no loop_end was ever defined).
	p := meta.points
	if !p.HaveEnd || p.EndSec-p.StartSec < minLoopWindowSec || p.StartSec >= p.EndSec {
		return
	}

	// Check if we've already passed the loop-end point. MusicClock reads the live
	// stream's position; if it's past `end`, we missed the window (late fetch, or
	// the track was resumed past the loop point) — don't arm a seek into the past.
	if pos, _, ok := a.MusicClock(); ok && pos >= p.EndSec {
		return // already past the loop point; wait for the natural loop-around
	}

	// Arm the loop-back: set the deadline to fire when the track reaches `end`.
	// advanceLoopBack (called every Frame) polls the deadline and fires the seek.
	a.loopArm = &loopArm{
		url:      url,
		start:    p.StartSec,
		end:      p.EndSec,
		deadline: time.Now().Add(time.Duration(p.EndSec * float64(time.Second))),
	}
}

// fetchLoopMetaOnce fetches and parses a track's sidecar(s) exactly once (#48),
// writing the result (found or conclusively-missing) to a.loopMetaRes. Runs in a
// background goroutine (spawned by armLoopFromCache); never blocks the render
// thread. For AO .txt sidecars, the sample rate is sniffed from `data` (the track's
// own bytes, already in memory); for DRO .ini, the folder convention is applied.
// Direct http(s) URLs (query strings present) never get a sidecar probe — they're
// treated as conclusively-missing immediately (courtroom.IsDirectMusicURL gate).
func (a *Audio) fetchLoopMetaOnce(url string, data []byte) {
	// Direct URLs (with query strings) are never probed for sidecars — they're
	// off-domain redirects or parameterized CDN links, not asset-origin paths.
	if courtroom.IsDirectMusicURL(url) {
		a.loopMetaRes <- loopMetaResult{url: url, meta: loopMeta{found: false, fetchedAt: time.Now()}}
		return
	}

	// Sniff sample rate from the track's own bytes (already in memory from the
	// music fetch). Needed for both AO .txt (samples → seconds) and DRO .ini
	// (samples → seconds). Opus/unknown defaults to 48000 (best-effort).
	sampleRate, ok := assets.SniffAudioSampleRate(data)
	if !ok {
		sampleRate = 48000
	}

	// Try AO .txt first: same path, `.txt` appended.
	txtURL := url + ".txt"
	if _, txtData, ok := a.mgr.ResolveRaw(txtURL, assets.AssetTypeMusic); ok && len(txtData) > 0 {
		if points, ok := courtroom.ParseAOLoopSidecar(txtData, sampleRate); ok {
			// Clamp the points against the track's duration.
			start, end := courtroom.ClampLoopPoints(points.StartSec, points.EndSec, a.musicDuration())
			points.StartSec = start
			points.EndSec = end
			a.loopMetaRes <- loopMetaResult{url: url, meta: loopMeta{points: points, found: true, fetchedAt: time.Now()}}
			return
		}
		// Parse failed: fall through to try DRO (AO .txt might be corrupt; DRO
		// could still be valid). If DRO also fails, both are conclusively-missing.
	}

	// Try DRO .ini: folder convention (courtroom.DROLoopManifestURL). The filename
	// is case-insensitive matched against the .ini's section headers.
	if droURL, ok := courtroom.DROLoopManifestURL(url); ok {
		if relPath, ok := courtroom.MusicRelPath(url); ok {
			if _, droData, ok := a.mgr.ResolveRaw(droURL, assets.AssetTypeMusic); ok && len(droData) > 0 {
				if droMeta, ok := courtroom.ParseDROLoopSidecar(droData, relPath, sampleRate); ok {
					// DRO play_once suppresses arming even when the wire said loop=true.
					if droMeta.PlayOnce {
						// Mark as found but with zero points (play_once = no loop).
						a.loopMetaRes <- loopMetaResult{url: url, meta: loopMeta{found: true, fetchedAt: time.Now()}}
						return
					}
					// Clamp the points against the track's duration.
					start, end := courtroom.ClampLoopPoints(droMeta.Loop.StartSec, droMeta.Loop.EndSec, a.musicDuration())
					points := courtroom.LoopPoints{StartSec: start, EndSec: end, HaveEnd: droMeta.Loop.HaveEnd}
					a.loopMetaRes <- loopMetaResult{url: url, meta: loopMeta{points: points, found: true, fetchedAt: time.Now()}}
					return
				}
			}
		}
	}

	// Both AO and DRO conclusively missing (or parse failed): cache that fact.
	a.loopMetaRes <- loopMetaResult{url: url, meta: loopMeta{found: false, fetchedAt: time.Now()}}
}

// loopMetaResult carries a sidecar fetch/parse result from the background goroutine
// back to the render thread. Drained by pollLoopMeta (called every Frame).
type loopMetaResult struct {
	url  string
	meta loopMeta
}

// pollLoopMeta drains sidecar fetch results (#48) and writes them to the cache.
// Called once per Frame (render thread only). Non-blocking: drains until the
// channel is empty, then returns.
func (a *Audio) pollLoopMeta() {
	for {
		select {
		case res := <-a.loopMetaRes:
			// Evict oldest if at cap (FIFO, hard rule #4).
			if len(a.loopMeta) >= loopMetaCacheCap {
				// Go map iteration order is randomized, but we don't have an order
				// slice like swapSnaps. For a 256-entry cap, a random eviction is
				// acceptable (the cache is a performance optimization, not correctness).
				for k := range a.loopMeta {
					delete(a.loopMeta, k)
					break
				}
			}
			a.loopMeta[res.url] = res.meta
		default:
			return
		}
	}
}

// advanceLoopBack checks the armed loop-back deadline and fires the seek when due
// (#48). Called once per Frame (render thread only). If no arm is set, or the arm
// is stale (applies to a different track than the one currently playing), this is
// a no-op. When the deadline arrives, the stream is seeked to loop_start via the
// precise binding (sub-second accuracy). The arm is NOT cleared after firing — it
// stays live so the loop repeats every `end - start` seconds. Only cleared when a
// new track loads (playDecoded) or the stream stops (stopMusic).
func (a *Audio) advanceLoopBack() {
	if a.loopArm == nil {
		return // no arm set
	}
	// Stale arm (applies to a different track): clear and return. This can happen
	// if a track was armed, then StopMusic was called, then a new track started —
	// stopMusic clears the arm, but a race could leave it briefly non-nil.
	if a.loopArm.url != a.musicURL {
		a.loopArm = nil
		return
	}
	// Deadline not yet due: wait.
	if time.Now().Before(a.loopArm.deadline) {
		return
	}
	// Deadline due: fire the loop-back seek. The precise binding (SeekMusicPrecise)
	// delivers sub-second accuracy; go-sdl2's stock wrapper truncates to whole seconds.
	if !a.SeekMusicPrecise(a.loopArm.start) {
		log.Printf("render: loop-back seek to %.2fs failed for %q", a.loopArm.start, a.musicURL)
	}
	// Advance the deadline by one loop period so the seek repeats. This is cheaper
	// than clearing the arm and re-arming on the next iteration (one Add vs. a
	// full armLoopFromCache + MusicClock read).
	loopPeriod := a.loopArm.end - a.loopArm.start
	a.loopArm.deadline = a.loopArm.deadline.Add(time.Duration(loopPeriod * float64(time.Second)))
}

// musicDuration returns the live stream's duration in seconds, or -1.0 if unknown
// (pre-2.6 SDL_mixer, mod/midi). Used by ClampLoopPoints to wrap loop_end. Render
// thread only; must be called with a.music != nil.
func (a *Audio) musicDuration() float64 {
	if _, dur, ok := a.MusicClock(); ok {
		return dur
	}
	return -1.0
}
