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

	// Latched at the cap: throttle open + missing base → no demand submitted.
	a.sceneWarmSet = [6]string{"warm://never-lands", "", "", "", "", ""}
	a.sceneWarmDemands = map[string]int{"warm://never-lands": warmMaxDemandsPerBase}
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
	a.sceneWarmDemands = map[string]int{"warm://never-lands": warmMaxDemandsPerBase}
	a.healScenery() // latched: no demand submitted
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

	// Throttle open but the futility cap reached: still no demand.
	a.activeWarmLastDemand = time.Time{}
	a.activeWarmDemands = map[string]int{"warm://missing-button": warmMaxDemandsPerBase}
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
