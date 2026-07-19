package ui

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// refBases lifts the Base of every ref for terse assertions.
func refBases(refs []courtroom.AssetRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Base
	}
	return out
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNextWarmBatch pins the incremental warm submitter's pure core (the freeze
// fix): a bounded batch is peeled off the FRONT of the pending list each tick, so
// the storm of PrefetchChain submits can never fill the high lane in one go and
// block the render thread. Covers empty, exact-multiple, remainder, resident-skip,
// and the no-room (batch<=0) case.
func TestNextWarmBatch(t *testing.T) {
	mk := func(bases ...string) []courtroom.AssetRef {
		out := make([]courtroom.AssetRef, len(bases))
		for i, b := range bases {
			out[i] = courtroom.AssetRef{Base: b, Type: assets.AssetTypeCharSprite}
		}
		return out
	}
	none := func(string) bool { return false }

	t.Run("empty pending", func(t *testing.T) {
		submit, rest := nextWarmBatch(nil, warmPrefetchPerTick, none)
		if len(submit) != 0 || len(rest) != 0 {
			t.Fatalf("empty: submit=%v rest=%v, want both empty", submit, rest)
		}
	})

	t.Run("batch smaller than pending: remainder stays", func(t *testing.T) {
		pending := mk("a", "b", "c", "d", "e")
		submit, rest := nextWarmBatch(pending, 2, none)
		if !eqStrs(refBases(submit), []string{"a", "b"}) {
			t.Errorf("submit = %v, want [a b]", refBases(submit))
		}
		if !eqStrs(refBases(rest), []string{"c", "d", "e"}) {
			t.Errorf("rest = %v, want [c d e]", refBases(rest))
		}
	})

	t.Run("exact multiple across two ticks drains cleanly", func(t *testing.T) {
		pending := mk("a", "b", "c", "d")
		s1, r1 := nextWarmBatch(pending, 2, none)
		s2, r2 := nextWarmBatch(r1, 2, none)
		if !eqStrs(refBases(s1), []string{"a", "b"}) || !eqStrs(refBases(s2), []string{"c", "d"}) {
			t.Errorf("two ticks = %v then %v, want [a b] then [c d]", refBases(s1), refBases(s2))
		}
		if len(r2) != 0 {
			t.Errorf("rest after draining = %v, want empty", refBases(r2))
		}
	})

	t.Run("batch >= pending drains in one tick", func(t *testing.T) {
		pending := mk("a", "b")
		submit, rest := nextWarmBatch(pending, warmPrefetchPerTick, none)
		if !eqStrs(refBases(submit), []string{"a", "b"}) || len(rest) != 0 {
			t.Errorf("submit=%v rest=%v, want [a b] and empty", refBases(submit), refBases(rest))
		}
	})

	t.Run("resident refs are skipped (don't spend a lane slot)", func(t *testing.T) {
		pending := mk("a", "b", "c", "d")
		resident := func(base string) bool { return base == "a" || base == "c" }
		// batch=2 counts only the NON-resident refs it emits: b and d.
		submit, rest := nextWarmBatch(pending, 2, resident)
		if !eqStrs(refBases(submit), []string{"b", "d"}) {
			t.Errorf("submit = %v, want [b d] (resident a,c skipped)", refBases(submit))
		}
		if len(rest) != 0 {
			t.Errorf("rest = %v, want empty", refBases(rest))
		}
	})

	t.Run("no room this tick (batch<=0) submits nothing but keeps pending", func(t *testing.T) {
		pending := mk("a", "b")
		submit, rest := nextWarmBatch(pending, 0, none)
		if len(submit) != 0 {
			t.Errorf("submit = %v, want empty when batch<=0", refBases(submit))
		}
		if !eqStrs(refBases(rest), []string{"a", "b"}) {
			t.Errorf("rest = %v, want the untouched pending", refBases(rest))
		}
	})
}

// TestWarmRoom pins warmRoom's clamp: given the current OUTSTANDING count (=
// len(warmInFlight), submitted-but-not-yet-resident bases), it yields how many
// more refs may be submitted this tick without pushing the in-flight set past the
// window. The load-bearing property — that only SUBMITTED refs feed `outstanding`,
// so a warm cache can't deflate the window and re-open the block — lives in
// tickGifWarm's set bookkeeping (TestTickGifWarmPreResidentDoesNotOverfillWindow);
// here we just pin the arithmetic: full burst when empty, floor to the window, 0
// (never negative) when full or over-full, and the never-overfill invariant.
func TestWarmRoom(t *testing.T) {
	// Nothing in flight: the full per-tick burst is available.
	if got := warmRoom(0); got != warmPrefetchPerTick {
		t.Errorf("empty: room = %d, want the per-tick burst %d", got, warmPrefetchPerTick)
	}

	// Full window (none resolved yet): room must be 0 so no submit pushes past it.
	if got := warmRoom(warmInFlightWindow); got != 0 {
		t.Errorf("full window: room = %d, want 0 (must not push the lane past the window)", got)
	}

	// Over-full (never-resident submitted refs pile up — Finding 2's stall shape):
	// room clamps at 0, never negative, never re-opens.
	if got := warmRoom(warmInFlightWindow + 50); got != 0 {
		t.Errorf("over-full: room = %d, want 0 (clamped, never negative)", got)
	}

	// The invariant sweep: for every outstanding value, submitting `room` more refs
	// this tick must never push the in-flight set (outstanding+room) past the window.
	for outstanding := 0; outstanding <= warmInFlightWindow+10; outstanding++ {
		room := warmRoom(outstanding)
		if room < 0 {
			t.Fatalf("outstanding=%d: room went negative (%d)", outstanding, room)
		}
		if room > warmPrefetchPerTick {
			t.Fatalf("outstanding=%d: room %d exceeds the per-tick burst %d", outstanding, room, warmPrefetchPerTick)
		}
		if outstanding <= warmInFlightWindow && outstanding+room > warmInFlightWindow {
			t.Fatalf("outstanding=%d room=%d: post-submit in-flight %d exceeds window %d",
				outstanding, room, outstanding+room, warmInFlightWindow)
		}
	}
}

// TestTickGifLoadErrorTeardown pins the async-load error path: a load failure
// tears the export down cleanly — a.gif nil, a.gifExporting false, the error on
// the warnLine — with nothing to leak (the loading shell holds no capture target
// or encoder yet, so a failed load can't strand the scale bracket or an ffmpeg).
func TestTickGifLoadErrorTeardown(t *testing.T) {
	a := testTabApp(t)
	loadCh := make(chan gifLoadResult, gifLoadBuf)
	a.gif = &gifExportJob{loading: true, loadCh: loadCh, loadName: "broken.demo", loadPath: "broken.demo", loadKind: exportVideo}
	a.gifExporting = true
	loadCh <- gifLoadResult{err: errors.New("bad file")}

	a.tickGifLoad(a.gif)

	if a.gif != nil {
		t.Error("failed load must clear a.gif (no dangling job)")
	}
	if a.gifExporting {
		t.Error("failed load must clear a.gifExporting")
	}
	if a.warnLine == "" || !strings.Contains(a.warnLine, "bad file") {
		t.Errorf("warnLine = %q, want the load error surfaced", a.warnLine)
	}
	if a.exportSavedDevScale != 0 {
		t.Errorf("scale bracket leaked (exportSavedDevScale=%d) — the loading shell never set it", a.exportSavedDevScale)
	}
}

// TestTickGifLoadPendingKeepsOverlay pins that while the loader hasn't delivered
// yet, tickGifLoad is a non-blocking no-op: the loading job survives so the
// always-alive "Reading …" overlay keeps drawing (the fix's headline behavior —
// no frozen window with no screen).
func TestTickGifLoadPendingKeepsOverlay(t *testing.T) {
	a := testTabApp(t)
	loadCh := make(chan gifLoadResult, gifLoadBuf) // empty: loader still working
	a.gif = &gifExportJob{loading: true, loadCh: loadCh, loadName: "big.aorec", loadPath: "big.aorec"}
	a.gifExporting = true

	a.tickGifLoad(a.gif)

	if a.gif == nil || !a.gif.loading {
		t.Error("with no result yet, the loading job must survive so the overlay stays up")
	}
	if !a.gifExporting {
		t.Error("gifExporting must stay true while loading")
	}
}

// TestTickGifLoadTransitionsToWarming is the load→warming delivery proof: a valid
// recording delivered over loadCh must run the render-thread export tail
// (finishLoadedExport → startSceneExport), leaving the job in the pre-warm phase
// with the scene's assets QUEUED as pendingWarm (submitted incrementally, never
// in one storm). Uses the real capture-target/manager harness; skips headlessly
// if SDL/render targets aren't available.
func TestTickGifLoadTransitionsToWarming(t *testing.T) {
	a := testTabApp(t)
	// A native-scale bracket that's a no-op (no font-cache rebuild needed headless).
	a.ctx.textDevPct = DefaultScalePct

	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	a.ctx.Ren = ren
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store
	a.d.Viewport = render.NewViewport(store)

	// A minimal streaming-shaped Manager (a LocalFetcher over an empty dir), built
	// on the SAME renderer as the capture target: the warm refs simply won't
	// resolve, which is fine — this test asserts the PHASE transition and that the
	// refs were QUEUED (not blasted at the pool), not that they load.
	resolver := assets.NewResolver(a.d.Prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver:  resolver,
		Prefs:     a.d.Prefs,
		T2:        t2,
		Disk:      disk,
		Source:    assets.NewLocalFetcher([]string{t.TempDir()}),
		LocalMode: true,
		Pool:      pool,
		Decoder:   decoder,
	})

	// A one-message scene (no StartBg → no audio touched during the transition):
	// SceneAssets yields this speaker's bg/desk/sprite refs.
	rec := &sceneRecording{
		Version: recordingVersion,
		Origin:  "http://example.test/",
		Events: []recEvent{{
			OffsetMs: 0,
			Kind:     int(courtroom.EventMessage),
			Message:  &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Side: "def", Message: "Objection!"},
		}},
	}

	loadCh := make(chan gifLoadResult, gifLoadBuf)
	a.gif = &gifExportJob{loading: true, loadCh: loadCh, loadName: "scene.demo", loadPath: "scene.demo", loadKind: exportGIF}
	a.gifExporting = true
	loadCh <- gifLoadResult{rec: rec}

	a.tickGifLoad(a.gif)

	if a.gif == nil {
		t.Fatal("delivery must build the real export job, got nil")
	}
	if a.gif.loading {
		t.Error("job still in loading phase after delivery — the transition didn't run")
	}
	if !a.gif.warming {
		t.Error("job must enter the pre-warm phase after a successful load")
	}
	if !a.gifExporting {
		t.Error("gifExporting must stay true across the load→warming swap (no screen steals a frame)")
	}
	if len(a.gif.warmRefs) == 0 {
		t.Error("warmRefs empty — SceneAssets found nothing to warm for a message scene")
	}
	// The freeze fix: refs are PENDING (queued for incremental submit), not all
	// blasted at the pool in startSceneExport.
	if len(a.gif.pendingWarm) == 0 {
		t.Error("pendingWarm empty — refs were not queued for incremental submission")
	}
	if a.gif.warmSubmitDone {
		t.Error("warmSubmitDone true before any warm tick — the clock started too early")
	}
	// Clean up the in-flight export so the test's deferred renderer teardown is safe.
	a.gif = nil
	a.gifExporting = false
	a.endExportScaleBracket()
}

// newWarmHarness wires a headless App with a REAL TextureStore (so tickGifWarm's
// residency probe is genuine) plus a real streaming-shaped Manager over an empty
// dir. Because nothing is on disk, PrefetchChain submissions never resolve and the
// Pump is never run here, so T1 residency stays entirely under the test's control
// (seedResident). Returns the store so the test can seed residency directly. Skips
// headlessly when SDL/render targets aren't available.
func newWarmHarness(t *testing.T) (*App, *render.TextureStore) {
	t.Helper()
	a := testTabApp(t)
	a.ctx.textDevPct = DefaultScalePct

	// Drainer stop: registered FIRST so its cleanup runs LAST (t.Cleanup is
	// LIFO) — pool.Close/decoder.Close must complete while the drainer below
	// is still consuming, or a worker blocked sending a probe result into
	// decodedCh (manager.go's transient-error path — the app's frame loop
	// drains it, this harness has no frame loop) deadlocks Close's WaitGroup
	// and times the whole test binary out.
	stopDrain := make(chan struct{})
	t.Cleanup(func() { close(stopDrain) })

	ren, cleanup := newCaptureHarness(t)
	t.Cleanup(cleanup)
	a.ctx.Ren = ren
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store

	resolver := assets.NewResolver(a.d.Prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver:  resolver,
		Prefs:     a.d.Prefs,
		T2:        t2,
		Disk:      disk,
		Source:    assets.NewLocalFetcher([]string{t.TempDir()}),
		LocalMode: true,
		Pool:      pool,
		Decoder:   decoder,
	})
	// Stand-in for the app's per-frame channel drains: soak everything the
	// submitted probes emit (transient errors, decodes, warnings) so pool
	// workers can never block on a full channel. The test asserts on T1
	// residency it seeds itself; these deliveries are irrelevant to it.
	go func() {
		for {
			select {
			case d := <-a.d.Manager.Decoded():
				if d.Asset != nil {
					d.Asset.Release()
				}
			case <-a.d.Manager.Audio():
			case <-a.d.Manager.Warnings():
			case <-stopDrain:
				return
			}
		}
	}()
	return a, store
}

// seedResident makes base T1-resident by uploading a 1×1 page under its key, so
// tickGifWarm's Store.Contains(base) reports true without running the async
// pipeline. Mirrors what the upload Pump does on the render thread.
func seedResident(t *testing.T, store *render.TextureStore, base string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	// Width/Height matter: buildPage sizes the SDL textures from the Decoded
	// metadata, not the frame bounds — zero values fail CreateTexture.
	d := &assets.Decoded{Frames: []*image.RGBA{img}, Delays: []time.Duration{0}, Width: 1, Height: 1}
	if err := store.Upload(base, d); err != nil {
		t.Fatalf("seed %q resident: %v", base, err)
	}
	if !store.Contains(base) {
		t.Fatalf("seed %q resident: Store.Contains still false after Upload", base)
	}
}

// mkWarmRefs builds n distinct char-sprite refs (base sprite-<i>).
func mkWarmRefs(n int) []courtroom.AssetRef {
	refs := make([]courtroom.AssetRef, n)
	for i := range refs {
		refs[i] = courtroom.AssetRef{Base: fmt.Sprintf("sprite-%d", i), Type: assets.AssetTypeCharSprite}
	}
	return refs
}

// TestTickGifWarmPreResidentDoesNotOverfillWindow drives the REAL tickGifWarm with
// a warm cache: most warm refs are ALREADY T1-resident before the phase starts
// (re-export / export-after-view / assets from the live session), and are placed at
// the FRONT of the list — the arrangement that broke the old arithmetic. It pins
// the Finding-1 fix: the in-flight window must count only refs THIS phase actually
// submitted, never the pre-resident ones.
//
// Ground truth in THIS harness: no SUBMITTED ref ever becomes resident (only the
// pre-resident ones are seeded, and the upload Pump never runs), so true high-lane
// occupancy == cumulative warmSubmitted. The invariant is therefore simply
// warmSubmitted ≤ warmInFlightWindow after every tick — an assertion INDEPENDENT of
// the code's own formula (the old circular check passed for the bug too). The buggy
// resident−residentPending model drives outstanding negative once the front
// residents drop out of pendingWarm, keeps room at the full burst, and blows
// warmSubmitted past the window within a few ticks; the correct set-based model
// plateaus at the window.
func TestTickGifWarmPreResidentDoesNotOverfillWindow(t *testing.T) {
	a, store := newWarmHarness(t)

	// A large scene, a big pre-resident majority (P well over the window), FRONT-loaded.
	const total = 200
	const preResident = 120 // > warmInFlightWindow, the case that broke the arithmetic
	refs := mkWarmRefs(total)
	for i := 0; i < preResident; i++ {
		seedResident(t, store, refs[i].Base)
	}

	a.gif = &gifExportJob{
		warming:     true,
		warmRefs:    refs,
		pendingWarm: append([]courtroom.AssetRef(nil), refs...),
		warmCreated: time.Now(), // real anchor; far under the hard cap so it can't interfere
	}

	// Many ticks — more than enough for the buggy model to overshoot. Nothing
	// submitted resolves here, so the correct model plateaus warmSubmitted at the
	// window and pendingWarm never fully drains (that plateau IS the fix).
	for tick := 0; tick < 40 && a.gif.warming; tick++ {
		a.tickGifWarm(a.gif)
		if a.gif.warmSubmitted > warmInFlightWindow {
			t.Fatalf("tick %d: warmSubmitted %d exceeds window %d — pre-resident refs re-opened room and the high lane can now block",
				tick, a.gif.warmSubmitted, warmInFlightWindow)
		}
		// The set is the outstanding count; it must equal warmSubmitted here (no
		// submitted ref resolves) and stay within the window.
		if len(a.gif.warmInFlight) > warmInFlightWindow {
			t.Fatalf("tick %d: warmInFlight size %d exceeds window %d", tick, len(a.gif.warmInFlight), warmInFlightWindow)
		}
	}
}

// TestTickGifWarmDrainsAsSubmittedRefsResolve is the progress companion: as
// SUBMITTED refs become T1-resident (their fetch landed), the in-flight set prunes
// them, room reopens, and the rest of pendingWarm is submitted — the phase makes
// progress instead of stalling at the window. Simulates resolution by seeding each
// just-submitted base resident between ticks.
func TestTickGifWarmDrainsAsSubmittedRefsResolve(t *testing.T) {
	a, store := newWarmHarness(t)

	const total = 100 // > warmInFlightWindow, so it can't all submit in one window
	refs := mkWarmRefs(total)
	a.gif = &gifExportJob{
		warming:     true,
		warmRefs:    refs,
		pendingWarm: append([]courtroom.AssetRef(nil), refs...),
		warmCreated: time.Now(),
	}

	// Each tick: run the warm tick, then "resolve" everything currently in flight by
	// seeding those bases resident, so the NEXT tick prunes them and reopens room.
	for tick := 0; tick < 50 && a.gif.warming; tick++ {
		a.tickGifWarm(a.gif)
		if a.gif.warmSubmitted > total {
			t.Fatalf("tick %d: warmSubmitted %d exceeds the ref count %d (double-submitted)", tick, a.gif.warmSubmitted, total)
		}
		for base := range a.gif.warmInFlight {
			if !store.Contains(base) {
				seedResident(t, store, base)
			}
		}
	}

	// With room reopening as refs resolve, every ref is eventually submitted and the
	// phase ends (all-ready, since we made them all resident).
	if len(a.gif.pendingWarm) != 0 {
		t.Errorf("pendingWarm not drained (%d left) — room never reopened as refs resolved", len(a.gif.pendingWarm))
	}
	if a.gif.warmSubmitted != total {
		t.Errorf("warmSubmitted = %d, want %d (every ref submitted exactly once)", a.gif.warmSubmitted, total)
	}
	if a.gif.warming {
		t.Error("warm phase never ended even though every ref became resident")
	}
}

// TestTickGifWarmHardCapEndsNeverResidentScene pins the Finding-2 fix: a scene
// whose assets never become T1-resident (a missing-origin imported .demo where
// every ref 404s) must still finish the warm phase. Once ≥warmInFlightWindow
// submitted refs are never resident, room pins at 0, pendingWarm can't drain,
// warmSubmitDone never latches, and the gifWarmMax clock never starts — so without
// an independent anchor the phase hangs forever. The gifWarmHardCap wall-clock
// ceiling (measured from job creation) forces the phase to end regardless.
func TestTickGifWarmHardCapEndsNeverResidentScene(t *testing.T) {
	a, _ := newWarmHarness(t) // never seed anything: the never-resident stall shape

	// More refs than the window, NONE ever seeded resident: the stall shape.
	refs := mkWarmRefs(warmInFlightWindow + 40)
	a.gif = &gifExportJob{
		warming:     true,
		warmRefs:    refs,
		pendingWarm: append([]courtroom.AssetRef(nil), refs...),
		// Stamp creation just past the hard cap so the first tick's backstop fires
		// (the test doesn't wait 20 s of wall clock).
		warmCreated: time.Now().Add(-gifWarmHardCap - time.Second),
	}

	a.tickGifWarm(a.gif)

	if a.gif.warming {
		t.Fatal("hard cap did not end the warm phase for a never-resident scene — it would hang forever")
	}
	if a.warnLine != "Rendering…" {
		t.Errorf("warnLine = %q, want the render banner after the warm phase ends", a.warnLine)
	}
}

// TestTickGifWarmHardCapDoesNotFireEarly guards the other side: a fresh job (created
// now) must NOT be guillotined by the hard cap on its first ticks — the backstop is
// a last-resort ceiling, not the normal exit. A healthy warm ends via all-ready or
// quiescence long before gifWarmHardCap.
func TestTickGifWarmHardCapDoesNotFireEarly(t *testing.T) {
	a, _ := newWarmHarness(t)

	refs := mkWarmRefs(warmInFlightWindow + 40) // enough that room can pin at 0
	a.gif = &gifExportJob{
		warming:     true,
		warmRefs:    refs,
		pendingWarm: append([]courtroom.AssetRef(nil), refs...),
		warmCreated: time.Now(), // fresh: the hard cap is 20 s away
	}

	// Several ticks in quick succession: none should end the phase via the hard cap
	// (nothing resident → no all-ready/quiesce either, so warming stays true).
	for i := 0; i < 5; i++ {
		a.tickGifWarm(a.gif)
	}
	if !a.gif.warming {
		t.Fatal("a fresh warm phase ended too early — the hard cap must be a last resort, not the normal path")
	}
}
