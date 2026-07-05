package render

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestAnimGenCensus pins the stage-change generation the compositor damages
// the viewport by: a static stage holds it still across Updates; a scene
// visibility flip, a hold-previous age-out, a texture streaming in, and a
// frame flip each bump it exactly when they change pixels; a running ramp
// (crossfade) bumps it every Update while it runs and stops with it.
func TestAnimGenCensus(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	vp := NewViewport(store)
	scene := &courtroom.Scene{}
	step := func() uint64 { vp.Update(scene, 16*time.Millisecond); return vp.AnimGen() }

	// Empty, static stage: no bumps however often Update runs.
	g := step()
	if g2 := step(); g2 != g {
		t.Fatalf("static empty stage bumped AnimGen %d → %d", g, g2)
	}

	// A speaker appearing (visibility flip, sprite not yet resident) is a
	// visible change — once, then stable while the cold gap persists.
	scene.Speaker.Visible = true
	scene.Speaker.Active = "ghost"
	g2 := step()
	if g2 == g {
		t.Fatal("speaker visibility flip must bump AnimGen")
	}
	if g3 := step(); g3 != g2 {
		t.Fatalf("cold-gap stage must hold AnimGen still, bumped %d → %d", g2, g3)
	}

	// Hold-previous age-out: coldFor crossing the max-age knob blanks the
	// stand-in — one bump, then stable again.
	vp.SetHoldMaxAge(50 * time.Millisecond)
	vp.Update(scene, 200*time.Millisecond) // well past the age threshold
	g3 := vp.AnimGen()
	if g3 == g2 {
		t.Fatal("hold-previous age-out must bump AnimGen")
	}
	if g4 := step(); g4 != g3 {
		t.Fatalf("aged-out stage must hold AnimGen still, bumped %d → %d", g3, g4)
	}

	// The sprite streaming in (resolve goes nil → page) is the cold-load
	// "art pops in" moment — must bump so the region redraws.
	if err := store.Upload("ghost", decodedFixture()); err != nil {
		t.Fatal(err)
	}
	g4 := step()
	if g4 == g3 {
		t.Fatal("a texture streaming in must bump AnimGen")
	}

	// The fixture is a 2-frame/50ms loop: a 16 ms step doesn't flip (no
	// bump), a 60 ms step does.
	g5 := step()
	if g5 != g4 {
		t.Fatalf("sub-delay step must not bump (no flip yet): %d → %d", g4, g5)
	}
	vp.Update(scene, 60*time.Millisecond)
	if g6 := vp.AnimGen(); g6 == g5 {
		t.Fatal("a frame flip must bump AnimGen")
	}

	// A running crossfade is continuous: every Update bumps while fadeLeft
	// drains, and the bumping stops when it finishes.
	vp.SetCrossfade(300 * time.Millisecond)
	vp.speakerAnim.fadeLeft = 100 * time.Millisecond
	if !vp.RampActive() {
		t.Fatal("crossfade in flight must report RampActive")
	}
	r1 := step()
	r2 := step()
	if r1 == r2 {
		t.Fatal("a running ramp must bump AnimGen every Update")
	}
	vp.speakerAnim.fadeLeft = 0
	vp.SetCrossfade(0)
	if vp.RampActive() {
		t.Fatal("RampActive must clear with the fade")
	}
	settled := vp.AnimGen()
	vp.Update(scene, 10*time.Millisecond) // sub-delay: no flip, no ramp
	if vp.AnimGen() != settled {
		t.Fatalf("AnimGen must settle once the ramp ends")
	}
}
