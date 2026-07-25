package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Bases for the two-room desk swap: room A is on screen, room B is the incoming
// scene whose desk is the thing under test.
const (
	deskTestBgA   = "live://background/roomA/defenseempty"
	deskTestDeskA = "live://background/roomA/defensedesk"
	deskTestBgB   = "live://background/roomB/witnessempty"
	deskTestDeskB = "live://background/roomB/stand"
)

// deskSwapRig puts room A's background + desk on screen and returns the viewport
// bound to them, ready for a swap to room B.
func deskSwapRig(t *testing.T) (*sdl.Renderer, *TextureStore, *Viewport) {
	t.Helper()
	ren, cleanup := newHeadlessRenderer(t)
	t.Cleanup(cleanup)

	store, err := NewTextureStoreBudget(ren, 64<<20)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	v := NewViewport(store)

	if err := store.Upload(deskTestBgA, decodedFixture()); err != nil {
		t.Fatalf("upload bgA: %v", err)
	}
	if err := store.Upload(deskTestDeskA, decodedFixture()); err != nil {
		t.Fatalf("upload deskA: %v", err)
	}
	v.Update(&courtroom.Scene{BackgroundBase: deskTestBgA, DeskBase: deskTestDeskA}, 16*time.Millisecond)
	if v.deskAnim.base != deskTestDeskA {
		t.Fatalf("test setup: deskAnim must bind room A's resident desk, got %q", v.deskAnim.base)
	}
	return ren, store, v
}

// deskDraws reports whether drawFill actually blitted the desk layer. drawFill
// writes v.fillRect only on the path that reaches ren.Copy, so a zero rect after
// a cleared start means "nothing was drawn" — AO2's ui_vp_desk->hide().
func deskDraws(t *testing.T, ren *sdl.Renderer, v *Viewport, deskBase string) bool {
	t.Helper()
	vp := sdl.Rect{X: 0, Y: 0, W: 640, H: 480}
	v.fillRect = sdl.Rect{}
	v.drawFill(ren, deskBase, &v.deskAnim, vp, 100)
	return v.fillRect != sdl.Rect{}
}

// TestDeskNeverHoldsPreviousRoom is #44 itself: a background that ships no desk
// image for the current position must draw NO desk — not the previous scene's.
//
// Before the fix the scenery sticky gate (syncAnimSticky) only rebound a layer once
// the incoming base was RESIDENT. A desk base that 404s never becomes resident, so
// deskAnim stayed pinned to the previous room's desk FOREVER and drawFill kept
// blitting it (drawFill resolves anim.base, not its base argument). Since most
// sessions open at "wit", the stale desk was typically the witness desk — exactly
// what the issue reports.
//
// AO2-Client's set_scene file_exists-checks the desk and hides the layer when it is
// absent, with no fallback (../AO2-Client/src/courtroom.cpp:4628-4634, :4656-4663).
// Room B's background is deliberately NOT resident here, so the ONLY thing that can
// release the layer is the conclusive-404 signal under test.
func TestDeskNeverHoldsPreviousRoom(t *testing.T) {
	ren, store, v := deskSwapRig(t)

	// The App's warning drain relays the conclusive 404 for room B's desk.
	store.MarkMissing(deskTestDeskB)

	sceneB := &courtroom.Scene{BackgroundBase: deskTestBgB, DeskBase: deskTestDeskB}
	v.Update(sceneB, 16*time.Millisecond)

	if v.deskAnim.base != deskTestDeskB {
		t.Fatalf("a conclusively-missing desk must still REBIND the layer (got %q, want %q) — "+
			"holding %q means room A's desk keeps drawing over room B", v.deskAnim.base, deskTestDeskB, deskTestDeskA)
	}
	if _, ok := v.deskAnim.resolve(store); ok {
		t.Fatal("test setup: the missing desk must not resolve to a page")
	}
	if _, ok := v.resolveHeld(&v.deskAnim); ok {
		t.Fatal("test setup: a base that never uploaded can have no held:// bridge frame")
	}
	if deskDraws(t, ren, v, sceneB.DeskBase) {
		t.Error("a background with no desk image for this position drew a desk anyway (#44)")
	}
}

// TestDeskStickyStillHoldsWhileStreaming is the anti-flash guard on the #44 fix:
// a desk that genuinely exists and is merely still streaming must keep the current
// scenery on screen, exactly as before. This is the test that fails if someone
// "simplifies" the fix into unconditional non-stickiness.
func TestDeskStickyStillHoldsWhileStreaming(t *testing.T) {
	ren, _, v := deskSwapRig(t)

	// Room B: neither its bg nor its desk is resident, and nothing is known to be
	// missing — the ordinary cold-load case.
	v.Update(&courtroom.Scene{BackgroundBase: deskTestBgB, DeskBase: deskTestDeskB}, 16*time.Millisecond)

	if v.bgAnim.base != deskTestBgA {
		t.Fatalf("test setup: the non-resident bg must be held by the sticky gate, got %q", v.bgAnim.base)
	}
	if v.deskAnim.base != deskTestDeskA {
		t.Fatalf("a still-streaming desk must NOT release the current one (got %q, want %q) — "+
			"that would blink the desk off on every position flip", v.deskAnim.base, deskTestDeskA)
	}
	if !deskDraws(t, ren, v, deskTestDeskA) {
		t.Error("the held desk must keep drawing while the incoming one streams")
	}
}

// TestDeskReleasesOnceBackgroundSwapped closes the one-probe-RTT window before the
// miss is known. The desk is an OVERLAY on one specific background — AO2 loads the
// pair in a single set_scene call — so once the BACKGROUND layer has swapped to the
// new scene, holding the old room's desk over it composites two rooms. Release
// instead: a momentarily desk-less new room beats a wrong desk on it.
func TestDeskReleasesOnceBackgroundSwapped(t *testing.T) {
	ren, store, v := deskSwapRig(t)

	// Room B's background lands; its desk has not (and is not yet known missing).
	if err := store.Upload(deskTestBgB, decodedFixture()); err != nil {
		t.Fatalf("upload bgB: %v", err)
	}
	sceneB := &courtroom.Scene{BackgroundBase: deskTestBgB, DeskBase: deskTestDeskB}
	v.Update(sceneB, 16*time.Millisecond)

	if v.bgAnim.base != deskTestBgB {
		t.Fatalf("test setup: the resident new bg must bind, got %q", v.bgAnim.base)
	}
	if v.deskAnim.base != deskTestDeskB {
		t.Fatalf("once the background swapped, the desk must swap with it (got %q, want %q)",
			v.deskAnim.base, deskTestDeskB)
	}
	if deskDraws(t, ren, v, sceneB.DeskBase) {
		t.Error("room A's desk was composited over room B's background")
	}
}

// TestDeskMissingHealsOnLateUpload pins the self-heal: the conclusively-missing set
// is cleared on upload (TextureStore.clearMissing runs in uploadTier), so a desk
// that appears later — server repack, a local mount added mid-session — draws again
// with no restart.
func TestDeskMissingHealsOnLateUpload(t *testing.T) {
	ren, store, v := deskSwapRig(t)

	store.MarkMissing(deskTestDeskB)
	sceneB := &courtroom.Scene{BackgroundBase: deskTestBgB, DeskBase: deskTestDeskB}
	v.Update(sceneB, 16*time.Millisecond)
	if deskDraws(t, ren, v, sceneB.DeskBase) {
		t.Fatal("test setup: the missing desk must draw nothing first")
	}

	if err := store.Upload(deskTestDeskB, decodedFixture()); err != nil {
		t.Fatalf("upload deskB: %v", err)
	}
	if store.IsMissing(deskTestDeskB) {
		t.Error("uploading the real page must clear the conclusively-missing flag")
	}
	v.Update(sceneB, 16*time.Millisecond)
	if !deskDraws(t, ren, v, sceneB.DeskBase) {
		t.Error("a desk that arrived late must draw again (no restart required)")
	}
}
