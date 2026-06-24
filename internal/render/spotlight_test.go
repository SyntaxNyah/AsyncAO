package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestSpotlightZeroAlloc pins #121: dimming the non-speaker layers (pair + desk) renders with
// zero per-frame heap allocations. benchScene has both a pair and a desk, so both dim paths
// (drawSprite's ColorMod compose + drawFill's grey-mod) run. Off → byte-identical to before.
func TestSpotlightZeroAlloc(t *testing.T) {
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
	vp.SetSpriteFX(SpriteFX{Spotlight: true, SpotlightLevel: 55})
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	allocs := testing.AllocsPerRun(200, func() {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("spotlight render allocates %.1f objects/op, want 0 (#121 zero-perf constraint)", allocs)
	}
}

// TestSpotlightBrightness pins the slider→brightness mapping: 0 = full bright, higher = darker,
// floored so the non-speaker never blacks out completely, and clamped to [floor,100].
func TestSpotlightBrightness(t *testing.T) {
	if got := spotlightBrightness(0); got != 100 {
		t.Errorf("level 0 → %d, want 100 (no dim)", got)
	}
	if got := spotlightBrightness(55); got != 45 {
		t.Errorf("level 55 → %d, want 45", got)
	}
	if got := spotlightBrightness(100); got != spotlightMinBrightness {
		t.Errorf("level 100 → %d, want the floor %d", got, spotlightMinBrightness)
	}
	if got := spotlightBrightness(200); got != spotlightMinBrightness { // over-range clamps
		t.Errorf("over-range level → %d, want the floor", got)
	}
	if got := spotlightBrightness(-5); got != 100 { // negative clamps to full
		t.Errorf("negative level → %d, want 100", got)
	}
}
