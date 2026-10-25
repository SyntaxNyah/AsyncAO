package render

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestRebindSceneryForcesNewBaseAcrossSwitch pins the multi-tab background fix:
// the SHARED viewport's scenery layers (bg + desk) are sticky (syncAnimSticky
// keeps the last good scenery until the incoming base is resident), which is the
// right hold WITHIN one session but LEAKS across a tab switch — the viewport would
// keep painting the previous tab's background under the newly-activated tab whose
// own bg is not yet resident (the "both tabs show the same background" bug).
// RebindScenery, called at the room-rebuild seam (buildRoom / pinToSplit), force-
// binds the layers to the new scene's bases BYPASSING the residency gate, so the
// swap shows the CORRECT new base at once. This test reproduces the leak (a plain
// Update with a non-resident new bg holds the old base) and proves the rebind
// fixes it.
func TestRebindSceneryForcesNewBaseAcrossSwitch(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	store, err := NewTextureStoreBudget(ren, 64<<20)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	v := NewViewport(store)

	const (
		bgA   = "live://background/tabA/defenseempty"
		deskA = "live://background/tabA/defensedesk"
		bgB   = "live://background/tabB/prosecutorempty"
		deskB = "live://background/tabB/prosecutordesk"
	)

	// Tab A: its bg is resident, and an Update binds the shared viewport to it.
	if err := store.Upload(bgA, decodedFixture()); err != nil {
		t.Fatalf("upload bgA: %v", err)
	}
	sceneA := &courtroom.Scene{BackgroundBase: bgA, DeskBase: deskA}
	v.Update(sceneA, 16*time.Millisecond)
	if v.bgAnim.base != bgA {
		t.Fatalf("after tab A Update, bgAnim.base = %q, want %q", v.bgAnim.base, bgA)
	}

	// Switch to tab B whose bg is NOT resident (it was evicted / never warmed while
	// backgrounded — the common case). A plain Update alone can't swap the scenery:
	// syncAnimSticky holds the OLD base because the new one isn't resident. This is
	// exactly the wrong-background-shown state the bug reports.
	sceneB := &courtroom.Scene{BackgroundBase: bgB, DeskBase: deskB}
	v.Update(sceneB, 16*time.Millisecond)
	if v.bgAnim.base != bgA {
		t.Fatalf("pre-rebind sanity: a non-resident new bg must be HELD by the sticky gate "+
			"(base=%q, want the old %q) — otherwise this test proves nothing", v.bgAnim.base, bgA)
	}

	// The fix: the room-rebuild seam force-rebinds the scenery to tab B's bases.
	v.RebindScenery(bgB, deskB)
	if v.bgAnim.base != bgB {
		t.Errorf("RebindScenery must force the new bg base even when not resident, base=%q want %q", v.bgAnim.base, bgB)
	}
	if v.deskAnim.base != deskB {
		t.Errorf("RebindScenery must force the new desk base, base=%q want %q", v.deskAnim.base, deskB)
	}

	// A subsequent Update on tab B's scene keeps the rebound base (the no-op guard
	// means matching bases don't reset, and the sticky gate never reverts to bgA).
	v.Update(sceneB, 16*time.Millisecond)
	if v.bgAnim.base != bgB {
		t.Errorf("after rebind, Update on tab B's scene must keep bgB, got %q", v.bgAnim.base)
	}
}
