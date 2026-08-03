package ui

// Local-pack layer lifecycle: deciding WHEN to build the mount index, publishing
// it to the Manager, and invalidating exactly the textures a source change makes
// stale.
//
// The whole file is render-thread code except the one goroutine that runs the
// blocking directory/archive walk — indexing is disk I/O and may never happen on
// the render path (rule 2). The walk posts to a cap-1 channel drained in the
// frame loop, copying the casing-probe idiom (its channel AND its single-flight
// latch — without the latch, rapid mount edits or reconnect flapping spawn
// concurrent multi-mount walks, which is exactly the unbounded-goroutine case
// rule 4 forbids).

import (
	"log"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// mountIndexResult carries a finished index back to the render thread. key is
// the mount set it was built FROM, so a result for a set the user has since
// changed is recognisably stale on arrival.
type mountIndexResult struct {
	idx  *assets.MountIndex
	errs []error
	key  string
}

// wantMountLayer reports the mount set the layer SHOULD be built from, or nil
// when layering is off. It is the single source of truth for "is the feature on",
// and it routes through LayeredAssets so the exclusive Local-only mode always
// wins and the ambiguous both-flags state has one defined meaning.
func (a *App) wantMountLayer() []string {
	if a.d.Prefs == nil || a.d.Manager == nil {
		return nil
	}
	// A local-mode Manager's SOURCE already is the mount folders; layering a mount
	// origin over a mount origin would double-serve. The Manager is mode-locked at
	// construction while the pref can invert under it, so both are checked.
	if a.d.Manager.LocalMode() {
		return nil
	}
	on, mounts := a.d.Prefs.LayeredAssets()
	if !on {
		return nil
	}
	return mounts
}

// mountSetKey identifies a mount set for change detection. Order is part of the
// identity because mounts are searched first-hit-wins, so reordering them changes
// which file wins and must therefore rebuild.
func mountSetKey(mounts []string) string {
	return strings.Join(mounts, "\x00")
}

// applyMountLayer reconciles the live layer with what the preferences ask for. It
// is cheap and idempotent, so it can be called from every path that might change
// either the mount set or the set of live asset origins.
//
// THREE TRANSITIONS, and the leave case is the subtle one:
//
//   - enter / rebuild (the mount set changed): clear the layer synchronously so
//     nothing serves from a stale index, bump the generation, and kick ONE walk.
//   - leave (layering turned off): capture the OUTGOING layer FIRST, publish nil,
//     then invalidate using the captured value. The invalidation predicate needs a
//     layer that is by then already nil, which is why rescanLocalPacks takes it as
//     an argument and never reads the live one.
//   - origins only (a tab connected or was activated): republish a fresh wrapper
//     around the SAME index. No re-walk — the index holds rels, not URLs.
func (a *App) applyMountLayer() {
	if a.d.Manager == nil {
		return
	}
	mounts := a.wantMountLayer()

	// LEAVE.
	if len(mounts) == 0 {
		if outgoing := a.d.Manager.MountLayer(); outgoing != nil {
			a.d.Manager.SetMountLayer(nil)
			a.rescanLocalPacks(outgoing)
			if idx := outgoing.Index(); idx != nil {
				idx.Retire() // drop the owner reference; in-flight readers keep their handles
			}
		}
		a.mountIndex = nil
		return
	}

	// ENTER / REBUILD: compare what we WANT against what the live index was
	// actually BUILT from — never against a key stamped when a walk was kicked.
	// Stamping on kick opens a re-kick hole: rapid mount edits advance the stamp,
	// the in-flight walk lands stale and is dropped, and the reconcile then sees
	// "stamp already matches" and never kicks again, so the pack silently never
	// appears. Deriving the identity from the index itself cannot drift.
	want := mountSetKey(mounts)
	have := ""
	if a.mountIndex != nil {
		have = mountSetKey(a.mountIndex.Mounts())
	}
	if want != have {
		// Publishing nil first means nothing serves from an index built for a
		// different mount set while the new walk runs.
		if outgoing := a.d.Manager.MountLayer(); outgoing != nil {
			a.d.Manager.SetMountLayer(nil)
			a.rescanLocalPacks(outgoing)
			if idx := outgoing.Index(); idx != nil {
				idx.Retire()
			}
		}
		a.mountIndex = nil
		a.startMountIndexBuild(mounts, want)
		return
	}

	// ORIGINS ONLY: same mounts, possibly a different set of live tabs.
	a.publishMountLayer(mounts)
}

// startMountIndexBuild kicks the blocking walk on a goroutine, at most one at a
// time. The latch is what keeps a user tidying their mount list — or a reconnect
// loop — from spawning a walk per click.
func (a *App) startMountIndexBuild(mounts []string, key string) {
	if a.mountIndexing {
		return // a walk is already running; its result is gen-checked on arrival
	}
	// Lazily created rather than assumed: a send on a nil channel blocks FOREVER,
	// so an App built without the main constructor would leak this goroutine and
	// never publish. Both this and pollMountIndex run on the render thread, so
	// creating it here races nothing.
	if a.mountIndexRes == nil {
		a.mountIndexRes = make(chan mountIndexResult, 1)
	}
	a.mountIndexing = true
	go func() {
		idx, errs := assets.BuildMountIndex(mounts)
		// Cap-1 channel: drain any undelivered older result before sending, or a
		// second producer would block forever on a full buffer.
		select {
		case <-a.mountIndexRes:
		default:
		}
		a.mountIndexRes <- mountIndexResult{idx: idx, errs: errs, key: key}
	}()
}

// pollMountIndex lands a finished index. Render thread, called once per frame
// beside pollCasingProbe.
func (a *App) pollMountIndex() {
	select {
	case res := <-a.mountIndexRes:
		a.mountIndexing = false
		if res.idx == nil || res.key != mountSetKey(a.wantMountLayer()) {
			// The mount set changed (or layering was turned off) while this walk ran:
			// the result is stale. Retire it so its archive handles close.
			if res.idx != nil {
				res.idx.Retire()
			}
			// Something newer is wanted; reconcile again now that the latch is free.
			a.applyMountLayer()
			return
		}
		a.mountIndex = res.idx
		a.mountIndexErrs = res.errs
		for _, err := range res.errs {
			log.Printf("ui: local pack mount unusable: %v", err)
		}
		mounts := a.wantMountLayer()
		if len(mounts) == 0 { // turned off between the kick and the landing
			res.idx.Retire()
			a.mountIndex = nil
			return
		}
		layer := a.publishMountLayer(mounts)
		// Only NOW is there something to invalidate against: drop the resident
		// textures this pack can re-answer so the current speaker updates on the
		// next frame instead of whenever it happens to evict.
		a.rescanLocalPacks(layer)
		files, _, truncated := res.idx.Stats()
		a.pushDebug("local packs indexed: " + strconv.Itoa(files) + " file(s)" + map[bool]string{true: " (cap reached)"}[truncated])
	default:
	}
}

// publishMountLayer wraps the current index with every live asset origin and
// installs it. Returns the published layer.
//
// Origins come from each tab's URL BUILDER, never from a session's raw asset URL:
// the server's ASS field is stored verbatim and often omits its trailing slash,
// and only the builder normalizes it. An unnormalized origin yields a rel with a
// leading slash that can never match an index key, so the pack would go silently
// inert while Settings cheerfully reported thousands of indexed files.
func (a *App) publishMountLayer(mounts []string) *assets.MountLayer {
	origins := make([]string, 0, len(a.tabs)+1)
	if o := a.urls.Origin(); o != "" {
		origins = append(origins, o)
	}
	for _, t := range a.tabs {
		if t == nil {
			continue
		}
		if o := t.state.urls.Origin(); o != "" {
			origins = append(origins, o)
		}
	}
	layer := assets.NewMountLayer(a.mountIndex, mounts, origins)
	a.d.Manager.SetMountLayer(layer)
	return layer
}

// rescanLocalPacks drops the render-side state a source change invalidates.
//
// layer is passed IN rather than read from the Manager because the "layering was
// just turned off" path publishes nil before invalidating — the predicate needs
// the OUTGOING layer to know which textures that pack was answering.
//
// It deliberately does NOT rebuild the room. buildRoom re-arms the live-roster
// poll, which queues an OOC /gas — the documented server flood-kick vector — so a
// user tidying their mount list would emit one per click. It also does not bump
// the pool epoch: that marks EVERY queued job stale, including live high-priority
// message work.
func (a *App) rescanLocalPacks(layer *assets.MountLayer) {
	if a.d.Store == nil {
		return
	}
	// A base that 404'd on the server is exactly what a pack is added to supply,
	// and a base whose server copy failed to DECODE is the most plausible reason to
	// deploy one at all — the prefetch gate consults both BEFORE the mount layer is
	// reached, so neither would ever re-resolve without this.
	a.d.Store.ForgetMissing()
	a.d.Store.ForgetFailed()
	if layer == nil {
		return
	}
	if n := a.d.Store.RemoveWhere(layer.Covers); n > 0 {
		a.pushDebug("local packs: dropped " + strconv.Itoa(n) + " resident texture(s) to re-resolve")
	}
}

// RescanLocalPacks is the Settings button: re-walk the folders from scratch,
// forgetting the quarantine (a rescan is the user asserting their files changed,
// which is the only realistic moment a damaged file gets fixed).
func (a *App) RescanLocalPacks() {
	// Dropping the index is what forces the rebuild branch: identity is derived
	// from the live index, so with none there is nothing to match. It also drops
	// the quarantine with it, which is the point — this is the user telling us the
	// files on disk changed.
	if a.mountIndex != nil {
		a.mountIndex.Retire()
		a.mountIndex = nil
	}
	a.applyMountLayer()
}
