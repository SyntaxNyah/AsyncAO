package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestEntranceSlide pins the #9 curve: 0 at/after settle, negative (slides from the left)
// during, and monotonically returning to 0.
func TestEntranceSlide(t *testing.T) {
	if got := entranceSlide(0, 800); got != 0 {
		t.Errorf("entranceSlide(0) = %d, want 0", got)
	}
	full := entranceSlide(entranceDuration, 800)
	if full >= 0 {
		t.Errorf("entranceSlide(full) = %d, want negative (slide from the left)", full)
	}
	prev := full
	for d := entranceDuration; d >= 0; d -= entranceDuration / 8 {
		s := entranceSlide(d, 800)
		if s < prev { // |offset| shrinks → s increases toward 0
			t.Errorf("entranceSlide not easing toward 0: %d at %v < prev %d", s, d, prev)
		}
		prev = s
	}
}

// TestViewportEntrance pins the edge-detect (a NEW speaker arms the slide, the same speaker
// doesn't, the first-ever speaker doesn't) and the 0-alloc render path.
func TestViewportEntrance(t *testing.T) {
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

	scene.Speaker.Name = "Phoenix" // first speaker of the session: no slide
	vp.Update(scene, 16*time.Millisecond)
	if vp.entranceLeft != 0 {
		t.Fatal("the first speaker should not slide in")
	}
	scene.Speaker.Name = "Edgeworth" // a NEW character takes the stage → slide
	vp.Update(scene, 16*time.Millisecond)
	if vp.entranceLeft <= 0 {
		t.Fatal("a new speaker did not arm the entrance slide")
	}
	before := vp.entranceLeft // same speaker again: only decays
	vp.Update(scene, 16*time.Millisecond)
	if vp.entranceLeft >= before {
		t.Error("the same speaker re-armed the entrance (should only decay)")
	}

	vp.SetSpriteFX(SpriteFX{Entrance: true})
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}
	allocs := testing.AllocsPerRun(200, func() {
		vp.entranceLeft = entranceDuration // keep the slide active across the measurement
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("entrance render allocates %.1f/op, want 0 (#9 zero-perf constraint)", allocs)
	}
}
