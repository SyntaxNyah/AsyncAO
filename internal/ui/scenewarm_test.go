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
