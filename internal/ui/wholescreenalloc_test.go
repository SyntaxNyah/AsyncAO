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
// added AFTER a per-frame allocation shipped; these two gates instead catch a
// whole class up front by asserting a SETTLED drawCourtroom / drawLobby frame
// allocates nothing.
//
// SCOPE (stated honestly, not "whole screen"): the courtroom gate stages a
// settled &courtroom.Session{} at the app's real default layout scales, so it
// exercises the ALWAYS-DRAWN composite — the viewport/scene at real size, the IC
// input row / drawICControls decomposition, the log-panel chrome and its pushClip
// conversions — AND the IC-log raster: 2–3 settled lines are appended through the
// live pushIC path so drawICLogList's per-line loop (drawLogLineNamed → labelEmoji
// → the text-texture cache), the most alloc-prone whole-screen path, is measured
// rather than skipped on an empty log. It does NOT cover the data-dependent
// branches that only render with populated live state: the areas filter, the
// players/mute chip, the additive checkbox, or the name-colour/bold name-split
// branch of drawLogLineNamed (off by default). Those are left out because their
// setup would inject per-frame-varying text (clocks, timers, live rosters) that
// isn't a settled frame; gating them needs its own fixture. The lobby gate
// covers the phone-book screen. A genuine leak in what IS covered surfaces as a
// non-zero count to FIX, not to loosen.

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
	// Seed the layout scales exactly as the live app does (app.go: loadPrefs).
	// testTabApp never runs that path, so vpPct/logPct/... default to Go's zero
	// — a 0% viewport is 0×0, which collapses the whole right column (rcol.H=0)
	// and leaves the IC-log list a degenerate few px high. That silently robbed
	// the gate of the log raster (empty list, no rows drawn). With the real
	// default scales (viewport 66%) a 1280×720 frame gives the log list a
	// realistic positive height, so drawICLogList actually rasters its lines.
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = a.d.Prefs.LayoutScales()
	// Pin a fixed frame clock so the append-time IC stamps (and anything else
	// that reads a.now()) are deterministic across the measured frames.
	a.frameNow = time.Date(2026, 7, 12, 16, 11, 0, 0, time.UTC)

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

	// Populate the IC scrollback through the SAME append path the live app uses
	// (pushIC), so drawICLogList's per-line raster loop — drawLogLineNamed /
	// labelEmoji / the text-texture cache — is actually measured, not skipped on
	// an empty log. ASCII only: a non-Latin/emoji line would latch the CJK/emoji
	// font chain (bumping fontChainGen) and rebuild the wrap cache every frame,
	// which is a legitimate one-off cost but not a SETTLED frame. A few short
	// lines wrap to a handful of rows that fit the list at this geometry.
	a.pushIC("Witch: settled line one", 0, false, -1, "Witch")
	a.pushIC("Judge: the court is now in session", 3, false, -1, "Judge")
	a.pushIC("Witch: a second settled line for the raster", 0, false, -1, "Witch")
	// Caught-up-at-bottom is the faithful settled state — and it skips BOTH the
	// "↓ N new" and "↓ Latest" pills, whose fmt.Sprintf / TextWidth would
	// otherwise allocate every frame (that's a scrolled-up frame, not settled).
	a.icStick = true
	a.icReadMark = len(a.icLog)

	return a, func() {
		store.Purge()
		cleanup()
	}
}

// settle renders probe batches until one allocates NOTHING, so one-off cache
// growth (text atlas, width memos, fieldSeq capacity) and the staged app's
// initial background asset work (the demand pump negative-caching its misses)
// finish before the strict gate measures. testing.AllocsPerRun counts GLOBAL
// mallocs — background goroutines included — so the gate can only read exact
// zero once the whole app is quiescent, not just the draw. Bounded: a scene
// that never settles falls through and the strict assert reports the
// persistent count loudly instead of spinning forever.
func settle(draw func()) {
	// settleBatches × settleFrames ≈ 600 headless frames, far past any one-off
	// warm-up; a real per-frame leak never reads 0 so the loop exits quickly.
	const settleBatches = 30
	const settleFrames = 20
	for i := 0; i < settleBatches; i++ {
		if testing.AllocsPerRun(settleFrames, draw) == 0 {
			return
		}
	}
}

// TestDrawCourtroomZeroAlloc is the whole-screen gate for the live courtroom.
func TestDrawCourtroomZeroAlloc(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	settle(draw)

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
	settle(draw)

	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled drawLobby allocates %.1f/op, want 0 — a per-frame allocation shipped (fix the alloc, don't loosen the gate)", n)
	}
}
