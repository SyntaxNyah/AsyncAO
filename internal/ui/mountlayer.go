package ui

// Local-pack layer lifecycle: when to build the mount index, publishing it to the Manager, and invalidating the
// textures a source change makes stale.
// Render-thread code except the one goroutine running the blocking walk (hard rule 2). It posts to a cap-1 channel,
// with a single-flight flag so rapid edits or reconnects can't spawn concurrent walks (hard rule 4).

import (
	"log"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// mountIndexResult carries a finished index to the render thread. key is the mount set it was built from, so a result
// for a changed set is recognisably stale.
type mountIndexResult struct {
	idx  *assets.MountIndex
	errs []error
	key  string
}

// wantMountLayer reports the mount set the layer should be built from, or nil when layering is off. It routes through
// LayeredAssets so Local-only mode always wins.
func (a *App) wantMountLayer() []string {
	if a.d.Prefs == nil || a.d.Manager == nil {
		return nil
	}
	// A local-mode Manager's source already is the mount folders, and layering would double-serve. The Manager is
	// mode-locked while the pref can invert, so check both.
	if a.d.Manager.LocalMode() {
		return nil
	}
	on, mounts := a.layeredAssets()
	if !on {
		return nil
	}
	return mounts
}

// mountSetKey identifies a mount set for change detection. Order counts: mounts are first-hit-wins, so reordering must
// rebuild.
func mountSetKey(mounts []string) string {
	return strings.Join(mounts, "\x00")
}

// applyMountLayer reconciles the live layer with the preferences; cheap and idempotent. Enter or rebuild: clear the
// layer so nothing serves from a stale index, then kick one walk.
// Leave: capture the outgoing layer first, since publishing nil comes before invalidating and rescanLocalPacks needs
// it. Origins only: republish a wrapper around the same index, with no walk, because it holds rels, not URLs.
func (a *App) applyMountLayer() {
	if a.d.Manager == nil {
		return
	}
	mounts := a.wantMountLayer()

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

	// Compare against what the live index was built from, not a key stamped at kick: edits would advance the stamp, the
	// in-flight walk would land stale, and nothing would kick again.
	want := mountSetKey(mounts)
	have := ""
	if a.mountIndex != nil {
		have = mountSetKey(a.mountIndex.Mounts())
	}
	if want != have {
		// Publish nil first so nothing serves from an index for another mount set during the walk.
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

	// Same mounts, possibly a different set of live tabs.
	a.publishMountLayer(mounts)
}

// startMountIndexBuild kicks the blocking walk on a goroutine, at most one at a time.
func (a *App) startMountIndexBuild(mounts []string, key string) {
	if a.mountIndexing {
		return // a walk is already running; its result is gen-checked on arrival
	}
	// Created lazily: a send on a nil channel blocks forever. This and pollMountIndex both run on the render thread, so
	// this races nothing.
	if a.mountIndexRes == nil {
		a.mountIndexRes = make(chan mountIndexResult, 1)
	}
	a.mountIndexing = true
	ch := a.mountIndexRes
	go func() {
		idx, errs := assets.BuildMountIndex(mounts)
		// Cap-1 channel: drain any undelivered older result, or a second producer would block forever.
		select {
		case <-ch:
		default:
		}
		ch <- mountIndexResult{idx: idx, errs: errs, key: key}
	}()
}

// pollMountIndex lands a finished index, on the render thread once per frame.
func (a *App) pollMountIndex() {
	select {
	case res := <-a.mountIndexRes:
		a.mountIndexing = false
		if res.idx == nil || res.key != mountSetKey(a.wantMountLayer()) {
			// The mount set changed or layering turned off during the walk. Retire the result so its archive handles
			// close.
			if res.idx != nil {
				res.idx.Retire()
			}
			// Something newer is wanted; reconcile again now that the flag is free.
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
		// Only now is there something to invalidate against: drop textures this pack can re-answer so the speaker
		// updates next frame.
		a.rescanLocalPacks(layer)
		files, _ := res.idx.Stats()
		a.pushDebug("local packs indexed: " + strconv.Itoa(files) + " file(s)")
	default:
	}
}

// publishMountLayer wraps the index with every live origin and installs it. Origins come from each tab's URL builder:
// the raw ASS field often lacks its trailing slash, and the resulting rel could never match an index key.
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

// rescanLocalPacks drops render-side state a source change invalidates. layer is passed in because leaving publishes
// nil first.
// It doesn't rebuild the room: buildRoom re-arms the live-roster poll, whose OOC /gas is a known server flood-kick
// vector. It doesn't bump the pool epoch either: that marks all queued jobs stale, including live message work.
func (a *App) rescanLocalPacks(layer *assets.MountLayer) {
	// First, and outside the Store guard: parsed INIs are UI state, not textures.
	a.forgetParsedINIs()
	if a.d.Store == nil {
		return
	}
	// A 404'd base or one whose server copy failed to decode is what a pack is added to supply; the prefetch check
	// consults both before the mount layer.
	a.d.Store.ForgetMissing()
	a.d.Store.ForgetFailed()
	if layer == nil {
		return
	}
	if n := a.d.Store.RemoveWhere(layer.Covers); n > 0 {
		a.pushDebug("local packs: dropped " + strconv.Itoa(n) + " resident texture(s) to re-resolve")
	}
}

// forgetParsedINIs drops everything derived from a char.ini so a source change shows without a reconnect; those parsed
// copies are in URL-keyed maps no texture invalidation touches.
// It re-reads the active character's ini directly rather than rebuilding the room, for rescanLocalPacks' flood-kick
// reason.
func (a *App) forgetParsedINIs() {
	a.charMetaCache = nil // remote speakers: refetched on their next message
	// Or the next hover over the same character is dropped as already showing, keeping the old emote list.
	a.previewChar, a.previewAnims, a.previewLabels = "", nil, nil
	a.previewEmoteIdx = 0
	a.iniWarmed = "" // the hover warm's latch, same reason
	if a.sess != nil && a.activeCharName() != "" {
		a.loadCharINI() // the emote strip in front of the user, reloaded in place
	}
}

// RescanLocalPacks is the Settings button: re-walk the folders, forgetting the quarantine, since a rescan asserts the
// files changed.
func (a *App) RescanLocalPacks() {
	// Dropping the index forces the rebuild branch, because identity derives from the live index, and drops the
	// quarantine with it.
	if a.mountIndex != nil {
		a.mountIndex.Retire()
		a.mountIndex = nil
	}
	a.applyMountLayer()
}
