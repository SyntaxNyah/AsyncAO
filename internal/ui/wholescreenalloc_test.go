package ui

import (
	"image"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// The whole-screen 0-alloc gate (#28). Per-widget AllocsPerRun tests were each
// added AFTER a per-frame allocation shipped; these two gates instead catch the
// entire class up front by asserting a SETTLED drawCourtroom / drawLobby frame
// allocates nothing. They gate every UI change on this branch — the areas
// filter, hover toolbox, additive checkbox, mute chip, the drawICControls
// decomposition, and the pushClip conversions — plus the whole 6,000-line
// screens.go forever. A settled frame IS everything-resident with no
// per-frame-varying text, so the setup uploads every scene base and quiesces
// the room; a genuine leak surfaces as a non-zero count to FIX, not to loosen.

// stageSettledCourtroom builds a headless App whose courtroom is fully settled:
// a real font-loaded Ctx over a software renderer, a session + room with an
// idle (typewriter-finished) message, and every stage base resident in the
// texture store so no miss fires the (map-allocating) scene heal / warm path.
func stageSettledCourtroom(t *testing.T) (*App, func()) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	ctx, err := NewCtx(ren)
	if err != nil {
		cleanup()
		t.Skipf("Ctx unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx = ctx
	a.uiScalePct = 100
	a.screen = ScreenCourtroom
	a.serverName = "AllocGate"

	store, err := render.NewTextureStore(ren)
	if err != nil {
		cleanup()
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store
	a.d.Viewport = render.NewViewport(store)

	// A minimal, quiescent session: no visible timers/clocks (they'd change text
	// every frame), no players/areas panels that vary.
	a.sess = &courtroom.Session{}
	a.room = newRoomForTest(t)

	// Resident stage bases: upload a tiny texture for each so the draw is a pure
	// touch — a miss would fire healScenery / keepSceneAssetsWarm, whose map
	// writes allocate every frame (that's not a settled frame).
	upload := func(base string) {
		if base == "" {
			return
		}
		dec := &assets.Decoded{
			Frames: []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 2, 2))},
			Delays: []time.Duration{0},
			Width:  2, Height: 2,
		}
		if err := store.Upload(base, dec); err != nil {
			t.Fatalf("upload %s: %v", base, err)
		}
	}
	// Drive a message and settle it so the stage has a real speaker, then make
	// every base it references resident.
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(0, "Witch", "settled line")})
	a.room.SkipToIdle()
	sc := &a.room.Scene
	upload(sc.BackgroundBase)
	upload(sc.DeskBase)
	upload(sc.Speaker.Active)
	upload(sc.Speaker.IdleBase)
	upload(sc.Pair.Active)

	return a, func() {
		store.Purge()
		cleanup()
	}
}

// warm renders n real frames so lazy caches (page caches, width memo, text
// cache) fill before AllocsPerRun measures — AllocsPerRun only does ONE internal
// warm-up, which isn't enough for the first-touch caches a whole screen builds.
func warm(draw func(), n int) {
	for i := 0; i < n; i++ {
		draw()
	}
}

// TestDrawCourtroomZeroAlloc is the whole-screen gate for the live courtroom.
func TestDrawCourtroomZeroAlloc(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	warm(draw, 8)

	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled drawCourtroom allocates %.1f/op, want 0 — a per-frame allocation shipped (fix the alloc, don't loosen the gate)", n)
	}
}

// TestDrawLobbyZeroAlloc is the companion gate for the lobby (the first screen).
func TestDrawLobbyZeroAlloc(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx = ctx
	a.uiScalePct = 100
	a.screen = ScreenLobby
	a.lobbyStatus = "Servers loaded."

	const w, h = 1280, 720
	draw := func() { a.drawLobby(w, h) }
	warm(draw, 8)

	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled drawLobby allocates %.1f/op, want 0 — a per-frame allocation shipped (fix the alloc, don't loosen the gate)", n)
	}
}
