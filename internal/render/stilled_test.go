package render

import (
	"testing"
)

// TestReduceAnimatedExcept pins the "non-active characters are a still" memory
// cap: every resident animation outside the active set collapses to its first
// frame, while the active speaker's animation stays full.
func TestReduceAnimatedExcept(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 16<<20) // 12 MiB main + 4 MiB shield
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	activeBase := "srv/characters/uma/(a)active"
	inactiveBase := "srv/characters/uma/(a)inactive"
	if err := store.Upload(activeBase, sizedFixture(512, 3)); err != nil {
		t.Fatalf("active upload: %v", err)
	}
	if err := store.Upload(inactiveBase, sizedFixture(512, 3)); err != nil {
		t.Fatalf("inactive upload: %v", err)
	}
	if !store.ContainsAnimated(activeBase) || !store.ContainsAnimated(inactiveBase) {
		t.Fatal("both pages must be animated-resident before reduction")
	}

	if n := store.ReduceAnimatedExcept(map[string]bool{activeBase: true}); n != 1 {
		t.Fatalf("ReduceAnimatedExcept reduced %d pages, want 1", n)
	}

	if p, ok := store.Get(activeBase); !ok || len(p.Frames) != 3 || p.Stilled {
		t.Errorf("active page not preserved: ok=%v frames=%d stilled=%v", ok, len(p.Frames), p.Stilled)
	}
	if p, ok := store.Get(inactiveBase); !ok || len(p.Frames) != 1 || !p.Stilled {
		t.Errorf("inactive page not stilled: ok=%v frames=%d stilled=%v", ok, len(p.Frames), p.Stilled)
	}

	// ContainsAnimated is the manager's re-decode probe: a still reads as absent.
	if store.ContainsAnimated(inactiveBase) {
		t.Error("ContainsAnimated must report false for a stilled page")
	}
	if !store.ContainsAnimated(activeBase) {
		t.Error("ContainsAnimated must report true for the active page")
	}
	// Contains is the wait-gate probe: a still must still read as resident.
	if !store.Contains(inactiveBase) {
		t.Error("Contains must report true for a stilled page (wait-gate)")
	}

	// A second pass is idempotent.
	if n := store.ReduceAnimatedExcept(map[string]bool{activeBase: true}); n != 0 {
		t.Errorf("second reduction reduced %d pages, want 0", n)
	}
}

// TestReduceAnimatedExceptOversized covers the overflow map: an animated page too
// large for the main tier reduces to a still there too, shrinking its byte
// accounting in place.
func TestReduceAnimatedExceptOversized(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 16<<20) // 12 MiB main
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	// 4 × 1024² RGBA = 16 MiB > 12 MiB main → parks in the oversized map.
	const base = "srv/characters/uma/(a)big"
	if err := store.Upload(base, sizedFixture(1024, 4)); err != nil {
		t.Fatalf("oversized upload: %v", err)
	}
	if got := store.OversizedBytes(); got != 16<<20 {
		t.Fatalf("OversizedBytes before reduction = %d, want %d", got, 16<<20)
	}

	if n := store.ReduceAnimatedExcept(nil); n != 1 {
		t.Fatalf("ReduceAnimatedExcept reduced %d pages, want 1", n)
	}
	if p, ok := store.Get(base); !ok || len(p.Frames) != 1 || !p.Stilled {
		t.Errorf("oversized page not stilled: ok=%v frames=%d stilled=%v", ok, len(p.Frames), p.Stilled)
	}
	// One 1024² frame of 4 remains (16 MiB / 4 frames).
	if got := store.OversizedBytes(); got != 4<<20 {
		t.Errorf("OversizedBytes after reduction = %d, want %d", got, 4<<20)
	}
}
