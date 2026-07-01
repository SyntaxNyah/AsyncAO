package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestSpriteOutlineShadowZeroAlloc pins #8: a received outline + drop-shadow on BOTH the
// speaker and the pair (the worst case) renders with zero per-frame heap allocations — the
// silhouette variant is built once during AllocsPerRun's warm-up, then every frame is a map
// hit + scratch-rect blits.
func TestSpriteOutlineShadowZeroAlloc(t *testing.T) {
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
	// Outline + shadow composed with the cheap-bracket effects, on both layers, plus a
	// rotation so the silhouette blits exercise the CopyEx angle/flip path.
	scene.Speaker.Style = courtroom.SpriteStyle{Outline: true, DropShadow: true, Glitch: true, Glow: true, Rotation: 40}
	scene.Pair.Style = courtroom.SpriteStyle{Outline: true, DropShadow: true, Glitch: true, Tint: true, R: 200, G: 60, B: 60}
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	allocs := testing.AllocsPerRun(200, func() {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("outline+shadow render allocates %.1f objects/op, want 0 (#8 zero-perf constraint)", allocs)
	}
}

// TestVariantSilhouette pins the silhouette pixel transform: every non-transparent pixel
// becomes white, alpha untouched (so a per-draw ColorMod can tint it any outline/shadow
// colour).
func TestVariantSilhouette(t *testing.T) {
	pix := []byte{10, 20, 30, 255, 0, 0, 0, 0, 200, 100, 50, 128}
	applyVariant(pix, 3, 1, uint8(courtroom.VariantSilhouette))
	want := []byte{255, 255, 255, 255, 255, 255, 255, 0, 255, 255, 255, 128}
	for i := range want {
		if pix[i] != want[i] {
			t.Fatalf("silhouette pixel %d = %d, want %d (full = %v)", i, pix[i], want[i], pix)
		}
	}
}
