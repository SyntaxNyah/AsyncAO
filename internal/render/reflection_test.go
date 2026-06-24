package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestReflectionZeroAlloc pins #123: the flipped glass-floor reflection of the speaker + pair
// (plus the stage clip set/restore) renders with zero per-frame heap allocations. Off →
// skipped entirely (byte-identical).
func TestReflectionZeroAlloc(t *testing.T) {
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
	vp.SetSpriteFX(SpriteFX{Reflection: true, ReflectStrength: 30})
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	allocs := testing.AllocsPerRun(200, func() {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("reflection render allocates %.1f objects/op, want 0 (#123 zero-perf constraint)", allocs)
	}
}

// TestReflectAlpha pins the opacity mapping: unset → default, floored so "on" always shows,
// and ceilinged so a reflection never reads as a second solid sprite.
func TestReflectAlpha(t *testing.T) {
	if got := reflectAlpha(0); got != uint8(reflectDefaultPct*255/100) {
		t.Errorf("unset strength → %d, want the default %d", got, reflectDefaultPct*255/100)
	}
	if got := reflectAlpha(1); got != reflectMinAlpha {
		t.Errorf("tiny strength → %d, want the floor %d", got, reflectMinAlpha)
	}
	if got := reflectAlpha(100); got != reflectMaxAlpha {
		t.Errorf("max strength → %d, want the ceiling %d", got, reflectMaxAlpha)
	}
	if got := reflectAlpha(50); got != 127 {
		t.Errorf("strength 50 → %d, want 127", got)
	}
}
