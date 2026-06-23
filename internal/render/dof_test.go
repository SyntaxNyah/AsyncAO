package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestDepthOfFieldZeroAlloc pins #11: the background soft-focus + dim renders with zero
// per-frame heap allocations (cached bg page + a scratch rect). Off → drawFill as before.
func TestDepthOfFieldZeroAlloc(t *testing.T) {
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
	vp.SetSpriteFX(SpriteFX{DoF: true})
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	allocs := testing.AllocsPerRun(200, func() {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("depth-of-field render allocates %.1f objects/op, want 0 (#11 zero-perf constraint)", allocs)
	}
}
