package ui

import (
	"image"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestKeepSceneAssetsWarm pins the anti-eviction warm's contract on all three
// paths, using nil-in-test deps as tripwires:
//   - lobby (no room, no replay): early return before touching Store/Manager;
//   - every stage base resident: pure touch — the nil Manager proves no
//     re-demand job is ever submitted for resident art (a panic here would
//     mean pool spam every tick, a perf regression);
//   - a missing base inside the throttle window: the demand is skipped (the
//     nil Manager again proves it), so a cold sprite can't spam the pool.
func TestKeepSceneAssetsWarm(t *testing.T) {
	a := testTabApp(t)
	a.keepSceneAssetsWarm() // lobby: must be a no-op without Store/Manager

	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store
	a.room = &courtroom.Courtroom{}

	upload := func(base string) {
		t.Helper()
		dec := &assets.Decoded{
			Frames: []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 2, 2))},
			Delays: []time.Duration{0},
			Width:  2, Height: 2,
		}
		if err := store.Upload(base, dec); err != nil {
			t.Fatalf("upload %s: %v", base, err)
		}
	}
	upload("warm://bg")
	upload("warm://idle")
	a.room.Scene.BackgroundBase = "warm://bg"
	a.room.Scene.Speaker.Visible = true
	a.room.Scene.Speaker.Active = "warm://idle"
	a.room.Scene.Speaker.IdleBase = "warm://idle"
	a.keepSceneAssetsWarm() // resident: touch-only (nil Manager untouched)

	// A missing base while the throttle window is closed: no demand either.
	a.room.Scene.ShowDesk = true
	a.room.Scene.DeskBase = "warm://missing-desk"
	a.sceneWarmLastDemand = time.Now()
	a.keepSceneAssetsWarm()

	// Steady state (everything resident) is on the per-frame path: pin it at
	// zero heap allocations, like every other per-frame touch.
	a.room.Scene.ShowDesk = false
	a.room.Scene.DeskBase = ""
	if n := testing.AllocsPerRun(1000, func() { a.keepSceneAssetsWarm() }); n != 0 {
		t.Fatalf("keepSceneAssetsWarm allocates %.1f/op resident, want 0", n)
	}
}

// TestSceneWarmFutilityLatch pins the churn breaker (the idle/minimized
// CPU-burn report): once a base has been re-demanded warmMaxDemandsPerBase
// times without sticking, the warm keeper stops re-demanding it — the nil
// Manager is the tripwire; a demand would panic — until the warm SET changes
// (a new message / room rebuild), which resets the counters. Without the
// latch, a settled scene whose decoded working set exceeds the T1 main tier
// churned decode→upload→evict→re-demand forever, whole cores at "idle".
func TestSceneWarmFutilityLatch(t *testing.T) {
	a := testTabApp(t)
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store
	a.room = &courtroom.Courtroom{}
	a.room.Scene.BackgroundBase = "warm://never-lands"

	// Latched past the churn cap: throttle open + missing base → no demand.
	a.sceneWarmSet = [6]string{"warm://never-lands", "", "", "", "", ""}
	a.sceneWarmDemands = map[string]sceneHealState{"warm://never-lands": {churns: warmMaxHealChurns + 1}}
	a.keepSceneAssetsWarm()

	// A warm-set change (the next message swaps the stage) resets the latch.
	// The new base is resident so the pass is touch-only; the counters clear.
	dec := &assets.Decoded{
		Frames: []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 2, 2))},
		Delays: []time.Duration{0},
		Width:  2, Height: 2,
	}
	if err := store.Upload("warm://next-message", dec); err != nil {
		t.Fatalf("upload: %v", err)
	}
	a.room.Scene.BackgroundBase = "warm://next-message"
	a.keepSceneAssetsWarm()
	if len(a.sceneWarmDemands) != 0 {
		t.Fatalf("a warm-set change must reset the futility counters, still holds %v", a.sceneWarmDemands)
	}
}

// TestHealSceneryFutilityLatch pins the drawn-path half of the churn breaker:
// healScenery shares the per-scene futility budget (sceneHealAllowed), so an
// over-tier or permanently-404 base stops being re-demanded from the
// live-scene heal too — the nil Manager is the tripwire; a demand would panic.
func TestHealSceneryFutilityLatch(t *testing.T) {
	a := testTabApp(t)
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store
	a.room = &courtroom.Courtroom{}
	a.room.Scene.BackgroundBase = "warm://never-lands"
	a.sceneWarmDemands = map[string]sceneHealState{"warm://never-lands": {churns: warmMaxHealChurns + 1}}
	a.healScenery() // latched: no demand submitted
}

// TestSceneHealBudget pins the hardened futility budget's two bounds and its
// resets (the v1.56.0 latch conflated them: a merely-SLOW load burned the
// whole budget against one in-flight fetch, and a blankpost repeating the
// same emote never reset it):
//   - asks WITHOUT a landing bound at warmMaxHealAsks (slow origin / 404);
//   - a LANDING resets the asks (markSceneResident) and arms the churn edge;
//   - landed-then-evicted-again cycles bound at warmMaxHealChurns;
//   - an idle→ceremony phase transition resets everything, warm set unchanged.
func TestSceneHealBudget(t *testing.T) {
	a := testTabApp(t)

	// Slow-load shape: repeated asks with no landing stay allowed up to the
	// asks bound, then latch.
	for i := 0; i < warmMaxHealAsks; i++ {
		if !a.sceneHealAllowed("warm://slow") {
			t.Fatalf("ask %d of %d must be allowed (no landing yet ≠ futile)", i+1, warmMaxHealAsks)
		}
	}
	if a.sceneHealAllowed("warm://slow") {
		t.Fatal("asks past the bound with no landing must latch")
	}

	// The landing re-arms the budget — a slow load can never strand a base.
	a.markSceneResident("warm://slow")
	if !a.sceneHealAllowed("warm://slow") {
		t.Fatal("a landing must reset the asks bound")
	}

	// Churn shape: land → demand (churn 1) … until the churn bound latches,
	// regardless of landings in between.
	for i := 1; i < warmMaxHealChurns; i++ { // one churn already counted above
		a.markSceneResident("warm://slow")
		if !a.sceneHealAllowed("warm://slow") {
			t.Fatalf("churn %d of %d must still be allowed", i+1, warmMaxHealChurns)
		}
	}
	a.markSceneResident("warm://slow")
	if a.sceneHealAllowed("warm://slow") {
		t.Fatal("landed-then-evicted cycles past the churn bound must latch")
	}

	// A new ceremony resets the budget even when the warm SET is unchanged —
	// the blankpost-same-emote corner. Two passes: the first syncs the warm
	// set (consuming its own clear), the second sees ONLY the idle→ceremony
	// phase edge. The throttle is held shut so scene misses never demand
	// (the nil Manager is the tripwire).
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store
	a.sceneWarmLastDemand = time.Now()
	a.room = newRoomForTest(t)
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(1, "Witch", "a new message begins")})
	if a.room.Phase() == courtroom.PhaseIdle {
		t.Fatal("test setup: the message never started a ceremony")
	}
	a.sceneWarmPhase = a.room.Phase() // no phase edge on the sync pass
	a.keepSceneAssetsWarm()           // syncs sceneWarmSet (its clear consumed here)
	a.sceneWarmDemands = map[string]sceneHealState{"warm://latched": {churns: warmMaxHealChurns + 1}}
	a.sceneWarmPhase = courtroom.PhaseIdle // as if the previous epoch ended idle
	a.keepSceneAssetsWarm()                // warm set unchanged: only the idle→ceremony edge can clear
	if len(a.sceneWarmDemands) != 0 {
		t.Fatalf("a new ceremony must reset the heal budget, still holds %v", a.sceneWarmDemands)
	}
}

// TestActiveWarmThrottleAndLatch pins keepActiveAssetsWarm's re-demand
// discipline (the old path submitted a pool job per 50 ms Background tick,
// forever, for a conclusively-404'd emote button): inside the throttle window
// no demand fires, at the futility cap none ever does — the nil Manager is
// the tripwire for both — and a char/emote change resets the counters.
func TestActiveWarmThrottleAndLatch(t *testing.T) {
	a := testTabApp(t)
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store

	// Throttle window closed: no demand for a missing base.
	a.activeWarmLastDemand = time.Now()
	a.warmBase("warm://missing-button", assets.AssetTypeEmoteButton)

	// Throttle open but the ask budget spent: still no demand.
	a.activeWarmLastDemand = time.Time{}
	a.activeWarmDemands = map[string]int{"warm://missing-button": warmMaxHealAsks}
	a.warmBase("warm://missing-button", assets.AssetTypeEmoteButton)

	// A char/emote change resets the counters (keepActiveAssetsWarm's gate).
	// The warmed icon is resident so the pass is touch-only.
	a.room = &courtroom.Courtroom{}
	a.sess = &courtroom.Session{}
	a.iniChar = "TestChar" // activeCharName() override — no picked char needed
	a.urls = courtroom.NewURLBuilder("http://warmtest/")
	a.emoteIdx = -1 // out of range: only the char icon warms
	dec := &assets.Decoded{
		Frames: []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 2, 2))},
		Delays: []time.Duration{0},
		Width:  2, Height: 2,
	}
	if err := store.Upload(a.urls.CharIcon("TestChar"), dec); err != nil {
		t.Fatalf("upload: %v", err)
	}
	a.activeWarmChar, a.activeWarmIdx = "SomeoneElse", 2
	a.keepActiveAssetsWarm()
	if len(a.activeWarmDemands) != 0 {
		t.Fatalf("a char/emote change must reset the futility counters, still holds %v", a.activeWarmDemands)
	}
}
