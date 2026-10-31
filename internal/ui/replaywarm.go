package ui

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// Replay pre-warm for flat (non-bundled) recordings. A bundled .aorec reads local files, but a flat one streams from
// the origin CDN faster than a cold fetch can serve, so the first lines would pop in sprite by sprite and the opening
// track would play silent. A bounded warm runs before the first event: seed the origin's formats, submit the scene's
// sprite/background refs through the export warm's window (nextWarmBatch / warmRoom / warmSettled), prefetch music. An
// empty or non-http origin skips the warm. A reachable host answering clean 404s ends it at once (warmSettled); a
// transport-erroring host never becomes IsMissing (manager.go), so it exits only on the gifWarmHardCap backstop.

const (
	// replayMusicWarmCap bounds how many distinct music tracks the pre-warm prefetches (hard rule 4);
	// the rest stream on demand.
	replayMusicWarmCap = 8
)

// replayWarmState is the flat-replay pre-warm phase. It shares nextWarmBatch / warmRoom / warmSettled with the export
// warm (see gifExportJob) and exists only while the phase runs (App.replayWarm).
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

	// Format-seed sub-phase: fetch <origin>/extensions.json and seed learned formats before submitting, so probes use
	// the manifest format for a .demo whose recorded origin was never the session origin.
	// seedDone is 1-buffered; nothing is submitted until it lands.
	seeding    bool
	seedDone   chan seedResult
	seedOrigin string
}

// startReplayWarm builds the pre-warm phase for a flat recording and stores it on App.replayWarm, leaving it nil
// when there is nothing to warm. Scene assets come from SceneAssets (shared with the export warm and archive exporter):
// textures are submitted incrementally, music refs prefetched once. Render thread only.
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
			// Music and other exact-URL refs, capped so a music-churn recording can't queue a storm of fetches.
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
	// Seed formats before submitting, under the same auto-detect and per-origin dedupe as the session and export paths.
	if a.d.Prefs.Load().AutoDetectFormats && !a.originSeeded(rec.Origin) {
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

// replayWarming reports whether the pre-warm is active; the driver holds event-feeding while it is.
func (a *App) replayWarming() bool { return a.replayWarm != nil }

// endReplayWarm clears the pre-warm phase; idempotent so every teardown path can call it.
func (a *App) endReplayWarm() { a.replayWarm = nil }

// tickReplayWarm advances the pre-warm one frame: poll the seed, count settled refs, submit a bounded batch, prefetch
// music once, and end on all-ready, quiescence, timeout or hard cap. warmSettled counts a conclusive 404 as done.
func (a *App) tickReplayWarm() {
	w := a.replayWarm
	if w == nil {
		return
	}
	now := time.Now()

	// Hold submission until the format seed lands so the first probe sees the manifest format; republish the resolver
	// then.
	if w.seeding {
		select {
		case res := <-w.seedDone:
			if res.status == seedApplied {
				a.d.Resolver.WarmFromPrefs()
			}
			w.seeding = false
		default:
			// Still seeding. The hard cap still applies: a dead origin's seed is bounded only by seedOriginFormats'
			// context.
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

	// Submission is bounded to the export warm's outstanding window so Submit never finds a full high lane.
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

	// Music: prefetch the opening tracks once as submission begins so sound is warm by the first MC.
	// Exact URLs (music carries its extension) use PrefetchExact, not the format-probing path.
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

	// Absolute backstop from phase creation: the only exit for a transport-erroring host, which never becomes
	// IsMissing. Music is not gated on, so a slow track can't hold playback.
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
