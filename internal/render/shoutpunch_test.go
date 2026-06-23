package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestPunchScale pins the #12 shout-punch curve: zero at/after the end and for a
// non-positive clock, bounded to the peak, and monotonically decaying across the window.
func TestPunchScale(t *testing.T) {
	if got := punchScale(0); got != 0 {
		t.Errorf("punchScale(0) = %v, want 0", got)
	}
	if got := punchScale(-time.Second); got != 0 {
		t.Errorf("punchScale(neg) = %v, want 0", got)
	}
	full := punchScale(shoutPunchDuration)
	if full <= 0 || full > shoutPunchPeak+1e-9 {
		t.Errorf("punchScale(full) = %v, want (0, %v]", full, shoutPunchPeak)
	}
	prev := full
	for d := shoutPunchDuration; d >= 0; d -= shoutPunchDuration / 8 {
		s := punchScale(d)
		if s > prev+1e-9 {
			t.Errorf("punchScale not monotone: %v at %v > prev %v", s, d, prev)
		}
		prev = s
	}
}

// TestViewportShoutPunch pins the edge-detect (the pop fires once when a shout appears, then
// only decays while it's held) and the zero-alloc render path (#12 must not degrade the hot
// loop).
func TestViewportShoutPunch(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	for _, base := range []string{"bg", "desk", "spk", "pair"} {
		if err := store.Upload(base, decodedFixture()); err != nil {
			t.Fatal(err)
		}
	}
	vp := NewViewport(store)
	scene := benchScene(store)

	scene.ShoutBase = ""
	vp.Update(scene, 16*time.Millisecond)
	if vp.punchLeft != 0 {
		t.Fatal("punch fired with no shout on stage")
	}
	scene.ShoutBase = "spk" // a shout appears → fire the pop
	vp.Update(scene, 16*time.Millisecond)
	if vp.punchLeft <= 0 {
		t.Fatal("punch did not fire on the shout edge")
	}
	before := vp.punchLeft // holding the shout only decays, never re-fires
	vp.Update(scene, 16*time.Millisecond)
	if vp.punchLeft >= before {
		t.Error("punch re-fired while the shout was held (should only decay)")
	}

	vp.SetSpriteFX(SpriteFX{ShoutPunch: true})
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}
	allocs := testing.AllocsPerRun(200, func() {
		vp.punchLeft = shoutPunchDuration // keep the pop active across the measurement
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("shout-punch render allocates %.1f/op, want 0 (#12 zero-perf constraint)", allocs)
	}
}
