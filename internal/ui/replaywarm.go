package ui

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// H5 — replay pre-warm for FLAT (non-bundled) recordings.
//
// A bundled .aorec already plays gap-free: its assets are local files the Manager
// reads from disk, so the very first frame has its sprites and the music starts on
// time. A FLAT recording streams the SAME assets from the origin CDN, and the
// replay's typewriter pacing runs faster than a cold async fetch — so without a
// warm phase the first lines pop in sprite-by-sprite and the opening track plays
// silent until it downloads. This inserts a bounded warm BEFORE the first event:
// seed the origin's formats (so probes hit the right extension), submit the scene's
// sprite/background refs through the SAME per-tick window discipline the export
// warm uses (nextWarmBatch / warmRoom / warmSettled — reused, not forked), and
// prefetch the scene's music refs (Exact URLs, bounded count) so sound is ready.
//
// It reuses the export warm's ceilings so the two can't drift and no origin can
// hang the phase. An empty / non-http origin skips the warm entirely (the
// HasPrefix "http" guard in startReplayWarm) and plays as before — so the only
// dead-origin case that reaches the warm is a REACHABLE-scheme host that is
// actually down or 404ing. Two exits cover it: the conclusive-404 release
// (warmSettled counts a base whose whole chain returned clean 404s as done —
// requires a REACHABLE host that answers with 404s) ends such an all-404 replay
// promptly; a host that TRANSPORT-errors (DNS-gone / connection-refused / 5xx)
// never becomes IsMissing (manager.go returns "done" on the first transport error
// without reportMissing), so it does NOT release early and instead exits via the
// wall-clock backstop (gifWarmHardCap) — bounded, but not sub-second. Either way
// the phase always ends and playback begins; nothing hangs.

const (
	// replayMusicWarmCap bounds how many DISTINCT music tracks the replay pre-warm
	// prefetches (hard rule §17.4 — no unbounded work). A scene rarely opens on more
	// than a handful of tracks and only ONE plays at a time, so the first few are what
	// matter for "sound starts on time"; the rest stream on demand as the replay
	// reaches them (exactly as today). Small so a music-churn recording can't queue a
	// storm of audio fetches at replay start.
	replayMusicWarmCap = 8
)

// replayWarmState is the FLAT-replay pre-warm phase (H5). It mirrors the export
// warm's incremental-submission bookkeeping (see gifExportJob's warm fields) so the
// two share nextWarmBatch / warmRoom / warmSettled and behave identically under an
// all-404 origin. Allocated only while the phase runs (App.replayWarm), so it costs
// nothing on the live path or a bundled replay.
type replayWarmState struct {
	warmRefs     []courtroom.AssetRef // the FULL texture set (M in the overlay + settle counting)
	pendingWarm  []courtroom.AssetRef // not-yet-submitted subset; drained a bounded batch per tick
	warmInFlight map[string]struct{}  // SUBMITTED bases not yet settled (the outstanding window)
	musicRefs    []courtroom.AssetRef // Exact music URLs, prefetched once (bounded by replayMusicWarmCap)
	musicDone    bool                 // the one-shot music prefetch has fired

	created    time.Time // phase-creation wall clock: anchors gifWarmHardCap (never-settled-ref backstop)
	started    time.Time // when submission finished (submitDone) — starts the quiescence/timeout clock
	lastGain   time.Time // last time the settled count rose (quiescence detector)
	best       int       // most refs seen settled so far
	resident   int       // settled count from the last tick (for the overlay)
	submitted  int       // cumulative real submits (observability + the overlay)
	submitDone bool      // every ref has been submitted → the end-condition clock may run

	// Format-seed sub-phase: fetch <origin>/extensions.json and seed the host's
	// learned formats BEFORE submitting, exactly like the export warm, so the probes
	// walk the manifest-declared format instead of the wrong default on a .demo whose
	// recorded origin was never the session origin. seedDone (1-buffered) delivers the
	// outcome; the phase submits nothing until it lands.
	seeding    bool
	seedDone   chan seedResult
	seedOrigin string
}

// startReplayWarm builds the pre-warm phase for a FLAT recording and stashes it on
// App.replayWarm, or returns without arming it when there's nothing to warm (an
// empty / non-http origin — the recording plays exactly as before). Enumerates the
// scene's assets ONCE via SceneAssets (the SAME enumeration the export warm and the
// archive exporter use), splitting texture refs (submitted incrementally) from music
// refs (Exact URLs, prefetched once). Optionally kicks the format seed ahead of
// submission, reusing seedOriginFormats + the shared per-origin dedupe. Render thread
// only (it reads a.d and starts one bounded goroutine like the export seed).
func (a *App) startReplayWarm(rec *sceneRecording) {
	a.replayWarm = nil
	if rec == nil || !strings.HasPrefix(rec.Origin, "http") {
		return // empty / local origin: nothing to stream, so nothing to warm
	}
	urls := courtroom.NewURLBuilder(rec.Origin)
	evs := make([]courtroom.Event, 0, len(rec.Events))
	for _, re := range rec.Events {
		evs = append(evs, eventFromRec(re))
	}
	var texRefs, musicRefs []courtroom.AssetRef
	for _, r := range courtroom.SceneAssets(urls, rec.StartBg, evs) {
		if r.Exact {
			// Music (and any other exact-URL) refs: bounded so a music-churn recording
			// can't queue a storm of audio fetches at replay start.
			if r.Type == assets.AssetTypeMusic && len(musicRefs) < replayMusicWarmCap {
				musicRefs = append(musicRefs, r) // AssetType: Music (Exact) — prefetched once in tickReplayWarm
			}
			continue
		}
		texRefs = append(texRefs, r) // AssetType: from SceneAssets — submitted incrementally
	}
	if len(texRefs) == 0 && len(musicRefs) == 0 {
		return // empty scene / nothing enumerable: skip the phase
	}

	now := time.Now()
	w := &replayWarmState{
		warmRefs:    texRefs,
		pendingWarm: texRefs, // shares the backing array; nextWarmBatch only reslices, never writes
		musicRefs:   musicRefs,
		created:     now, // anchors gifWarmHardCap so a never-settling ref can't hang the phase
	}
	// Seed the origin's formats BEFORE submitting, gated on the SAME http(s) +
	// auto-detect + per-origin dedupe as the session and export paths — so a .demo's
	// dead-origin replay probes the manifest format and a second replay of the same
	// origin skips the (already-done) fetch.
	if a.d.Prefs.FormatAutoDetect() && !a.originSeeded(rec.Origin) {
		a.markOriginSeeded(rec.Origin)
		w.seeding = true
		w.seedOrigin = rec.Origin
		w.seedDone = make(chan seedResult, gifSeedBuf) // 1-buffered: leak-free like the export seed
		done := w.seedDone
		mgr := a.d.Manager
		prefs := a.d.Prefs
		go func() { done <- seedOriginFormats(mgr, prefs, rec.Origin) }()
	}
	a.replayWarm = w
}

// replayWarming reports whether the FLAT-replay pre-warm phase is active (the
// driver gates event-feeding on it and the overlay draws the loading line).
func (a *App) replayWarming() bool { return a.replayWarm != nil }

// endReplayWarm clears the pre-warm phase (all-ready / quiesce / hard-cap / Skip /
// replay stop). Idempotent so every teardown path can call it unconditionally.
func (a *App) endReplayWarm() { a.replayWarm = nil }

// tickReplayWarm advances the FLAT-replay pre-warm one frame (render thread). Same
// shape as tickGifWarm: (1) poll the format seed, (2) count settled refs + prune the
// in-flight set, (3) submit a bounded batch, (4) prefetch the music once submission
// is under way, (5) end on all-ready / quiescence / timeout / hard cap. Reuses
// warmSettled (a conclusive 404 counts as done) so an all-404 replay ends promptly
// instead of freezing. Nothing here blocks the render thread — submission is bounded
// and every fetch is async.
func (a *App) tickReplayWarm() {
	w := a.replayWarm
	if w == nil {
		return
	}
	now := time.Now()

	// Format-seed sub-phase: hold submission until the seed lands (or is skipped), so
	// the first probe sees the manifest format. Like tickGifSeed: republish the
	// resolver table on the render thread once the seed applied.
	if w.seeding {
		select {
		case res := <-w.seedDone:
			if res.status == seedApplied {
				a.d.Resolver.WarmFromPrefs()
			}
			w.seeding = false
		default:
			// Still seeding — but the hard cap below still applies (a dead origin's seed
			// is itself bounded by seedOriginFormats' 15 s ctx, and the wall-clock anchor
			// backstops even that), so fall through to the backstop check.
			if !w.created.IsZero() && now.Sub(w.created) > gifWarmHardCap {
				a.endReplayWarm()
			}
			return
		}
	}

	// Count settled refs (resident OR conclusively 404'd) and prune the in-flight set.
	settled := 0
	for _, r := range w.warmRefs {
		if a.warmSettled(r.Base) {
			settled++
		}
	}
	w.resident = settled
	for base := range w.warmInFlight {
		if a.warmSettled(base) {
			delete(w.warmInFlight, base)
		}
	}

	// Incremental submission, bounded to the same outstanding window as the export
	// warm so a Submit can never find a full high lane and block the render thread.
	if len(w.pendingWarm) > 0 {
		room := warmRoom(len(w.warmInFlight))
		submit, rest := nextWarmBatch(w.pendingWarm, room, a.warmSettled)
		for _, r := range submit {
			if r.Base == "" {
				continue
			}
			a.d.Manager.PrefetchChain(r.Base, r.Alts, r.Type, network.PriorityHigh) // AssetType: from SceneAssets
			if w.warmInFlight == nil {
				w.warmInFlight = make(map[string]struct{})
			}
			w.warmInFlight[r.Base] = struct{}{}
			w.submitted++
		}
		w.pendingWarm = rest
	}

	// Music: prefetch the scene's opening tracks ONCE, as soon as submission begins,
	// so sound is warm in T2 by the time playback reaches the first MC. Exact URLs
	// (music carries its own extension) → PrefetchExact, never the format-probing
	// path. Bounded to replayMusicWarmCap at enumeration time.
	if !w.musicDone {
		for _, m := range w.musicRefs {
			a.d.Manager.PrefetchExact(m.Base, assets.AssetTypeMusic, network.PriorityHigh) // AssetType: Music (Exact)
		}
		w.musicDone = true
	}

	if len(w.pendingWarm) == 0 && !w.submitDone {
		w.submitDone = true
		w.started = now
		w.lastGain = now
		w.best = settled
	}
	if settled > w.best {
		w.best = settled
		w.lastGain = now
	}

	// Absolute backstop (from phase creation): the conclusive-404 release ends a
	// REACHABLE all-404 origin in sub-second, but this last-resort ceiling is what
	// ends the phase for a ref that neither resolves nor reports a conclusive miss —
	// a TRANSPORT-error host (down / refused / 5xx) never becomes IsMissing (see the
	// header), so a dead reachable-scheme origin exits ONLY here. Music is NOT gated
	// on here (it prefetches once, above), so a slow track can't hold playback.
	if !w.created.IsZero() && now.Sub(w.created) > gifWarmHardCap {
		a.endReplayWarm()
		return
	}
	if !w.submitDone {
		return
	}
	allReady := len(w.warmRefs) > 0 && settled >= len(w.warmRefs)
	quiesced := settled > 0 && now.Sub(w.lastGain) > gifWarmQuiet
	timedOut := now.Sub(w.started) > gifWarmMax
	if allReady || quiesced || timedOut || len(w.warmRefs) == 0 {
		a.endReplayWarm()
	}
}
