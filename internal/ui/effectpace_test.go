package ui

import (
	"image"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/veandco/go-sdl2/sdl"
)

// A live screen-effect overlay must hold the frame pacer up, and must let go when it
// ends. Since v1.55 nothing animates for free: a draw site that moved this frame has to
// SAY so (the census doctrine — count draw sites, never bare state), and an effect that
// never registers plays at whatever the idle knob says. The report was ~10 fps overlays.
//
// This drives the REAL draw site (App.renderViewportZoomed, the one call every stage
// layout goes through) rather than re-calling Viewport.AmbientAnimating itself, because
// a census test that re-implements the census proves nothing about the wiring. Only the
// one-line end-of-frame promotion (frameAnimChrome → drawnAnimChrome, App.Frame's defer)
// is done by hand, because Frame needs a whole screen.

// overlayPace uploads a 2-frame page and stages it as the scene's screen effect.
func overlayPace(t *testing.T, store *render.TextureStore, a *App, base string, delay time.Duration, loop bool) {
	t.Helper()
	dec := &assets.Decoded{
		Frames:   []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 4, 4)), image.NewRGBA(image.Rect(0, 0, 4, 4))},
		Delays:   []time.Duration{delay, delay},
		Animated: true,
		Width:    4, Height: 4,
	}
	if err := store.Upload(base, dec); err != nil {
		t.Fatalf("upload %s: %v", base, err)
	}
	a.room.Scene.Overlay = courtroom.SceneOverlay{Base: base, Loop: loop, Gen: a.room.Scene.Overlay.Gen + 1}
	a.d.Viewport.Update(&a.room.Scene, 0) // bind the page (fresh frame: the full delay is due)
}

func TestALiveEffectOverlayHoldsTheFrameRate(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx.Ren = ren
	a.room = &courtroom.Courtroom{}
	a.d.Viewport = render.NewViewport(store)
	a.d.Prefs.SetFPSCap(60)
	a.d.Prefs.SetIdleFPS(10) // the reported setting: an idle floor of 10 fps
	a.lastInputAt = time.Now().Add(-time.Hour)
	a.lastMotionAt = a.lastInputAt
	vp := sdl.Rect{X: 0, Y: 0, W: 64, H: 48}

	// Baseline: an empty stage draws and censuses NOTHING (the zero-cost-closed half —
	// a census that reports on a still screen is the idle-CPU-burn bug).
	a.frameAnimChrome = false
	a.renderViewportZoomed(vp)
	if a.frameAnimChrome {
		t.Fatal("a static stage must not report animating")
	}

	// A live LOOPING effect on stage: the draw site reports it.
	overlayPace(t, store, a, "theme://fx-loop", 80*time.Millisecond, true)
	a.frameAnimChrome = false
	a.renderViewportZoomed(vp)
	if !a.frameAnimChrome {
		t.Fatal("a live screen-effect overlay must report animating from its draw site — " +
			"without it the effect plays at the idle rate (the ~10 fps report)")
	}

	// …and the pacer acts on it: no static skip, and the anim-chrome tier, not idle.
	a.drawnAnimChrome = a.frameAnimChrome // App.Frame's end-of-frame promotion
	if a.SkipFrame(true, false) {
		t.Error("a frame that drew a live effect must not be skipped")
	}
	if got, idle := a.FramePace(true), time.Second/10; got >= idle {
		t.Errorf("pace with a live effect = %v, want well under the %v idle rate", got, idle)
	}
	if a.lastPacerTier != pacerAnimChrome {
		t.Errorf("pacer tier = %v, want the anim-chrome census tier", pacerTierLabel(a.lastPacerTier))
	}

	// The other direction — the release. A ONE-SHOT effect that has played out is a
	// still picture: it must stop holding frames, or an effect leaves the client at
	// full rate for as long as its last frame is displayed.
	overlayPace(t, store, a, "theme://fx-once", 20*time.Millisecond, false)
	for i := 0; i < 10; i++ { // run the clip past its end
		a.d.Viewport.Update(&a.room.Scene, 20*time.Millisecond)
	}
	a.frameAnimChrome = false
	a.renderViewportZoomed(vp)
	if a.frameAnimChrome {
		t.Error("a finished one-shot overlay must release the census — it is a still picture now")
	}

	// And clearing the effect entirely releases it too.
	a.room.Scene.Overlay = courtroom.SceneOverlay{Gen: a.room.Scene.Overlay.Gen}
	a.d.Viewport.Update(&a.room.Scene, 0)
	a.frameAnimChrome = false
	a.renderViewportZoomed(vp)
	if a.frameAnimChrome {
		t.Error("with no overlay on stage nothing may report animating")
	}
}

// TestAParkedLoopWakesForAnEffect covers the half a draw-site census structurally
// cannot: the event-driven loop parked on its OS event wait takes no pass at all, so
// SkipFrame's refusal never runs. Only NextWakeDelay decides when a frame comes back,
// and it used to know about carets, clocks and the idle tick — nothing about the stage.
// Both directions are pinned: the TRIGGER rings immediately (noteOverlayStart, the
// NotifyPreanimStarted precedent), and a running clip's next frame is a scheduled wake
// instead of an idle-rate one.
func TestAParkedLoopWakesForAnEffect(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx.Ren = ren
	a.room = &courtroom.Courtroom{}
	a.d.Viewport = render.NewViewport(store)
	a.d.Prefs.SetIdleFPS(config.FPSOff) // idle renders OFF: only real deadlines wake
	a.lastInputAt = time.Now().Add(-time.Hour)
	a.lastMotionAt = a.lastInputAt

	// Static stage, no damage: the park is the Background-only housekeeping floor.
	a.uiDirty = false
	if _, render := a.NextWakeDelay(true); render {
		t.Fatal("a static stage with idle renders off must schedule NO redraw wake")
	}

	// An effect is TRIGGERED while nothing is drawing — exactly what happens when the
	// room advances from Background. The Gen bump must ring the doorbell.
	a.room.Scene.Overlay = courtroom.SceneOverlay{Base: "theme://fx-loop", Loop: true, Gen: 1}
	a.noteOverlayStart()
	if !a.RenderNeeded() {
		t.Error("an effect trigger seen from Background must mark damage, or the first frame waits " +
			"out a whole idle period (and forever at idle=off)")
	}
	a.uiDirty = false // the frame it forced has now been drawn

	// With the clip resident and bound, the parked loop must wake for its NEXT frame,
	// at the clip's own cadence — not at the idle knob, which is off here.
	overlayPace(t, store, a, "theme://fx-loop", 80*time.Millisecond, true)
	wait, wantRender := a.NextWakeDelay(true)
	if !wantRender {
		t.Fatal("a live effect must schedule a redraw wake — parked, nothing else brings a frame back")
	}
	if wait > 80*time.Millisecond {
		t.Errorf("wake in %v, want no later than the clip's own 80ms flip", wait)
	}
}
