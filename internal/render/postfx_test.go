package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestPostFXZeroAlloc pins #10: all three overlays on render with zero per-frame heap
// allocations — the textures build once during AllocsPerRun's warm-up, then every frame is
// cached blits. (A disabled PostFX is an early return, byte-identical to before.)
func TestPostFXZeroAlloc(t *testing.T) {
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
	defer vp.PurgePostFX()
	scene := benchScene(store)
	vp.SetPostFX(PostFX{Vignette: true, Scanlines: true, Grain: true})
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	allocs := testing.AllocsPerRun(200, func() {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("post-FX render allocates %.1f objects/op, want 0 (#10 zero-perf constraint)", allocs)
	}
}

// TestPostFXActive pins the off-switch: an empty PostFX is inactive (so applyPostFX returns
// before any texture work).
func TestPostFXActive(t *testing.T) {
	if (PostFX{}).Active() {
		t.Error("zero PostFX should be inactive")
	}
	if !(PostFX{Grain: true}).Active() {
		t.Error("PostFX with grain should be active")
	}
}
