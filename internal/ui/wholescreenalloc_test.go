package ui

import (
	"image"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
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
	// The FIVE remaining panel zooms, seeded EXACTLY the way loadPrefs does (app.go)
	// and with no normalization on top: the fixture must reproduce a real install,
	// not a tidied-up one. (This used to carry an `if a.oocPct == 0 { = logPct }`
	// patch, which quietly papered over a shipped first-run bug — OOCScalePct had no
	// entry in the config defaults block — AND hid the divergent-zoom allocation the
	// sibling gate below now pins. Both are fixed; the patch is gone.)
	a.oocPct = a.d.Prefs.OOCScale()
	a.musicPct, a.playerPct, a.areaPct, a.modDashPct = a.logPct, a.logPct, a.logPct, a.logPct
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

// TestDrawCourtroomThemeFontsZeroAlloc is the same gate with a THEME's
// per-element font table applied (#39): distinct sizes on the chatbox, the IC
// log, the OOC log, the music list and the area list, plus bold on two of them.
// It proves the per-element accessors add nothing per frame — elemPct is
// arithmetic on a fixed array, the element fontSets are built once and then
// cached, and setPairs (walked by setOf / deviceTextFont) returns a stack array
// rather than a fresh slice.
func TestDrawCourtroomThemeFontsZeroAlloc(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()

	// Sizes deliberately DIFFER per element: one shared fontSet would rebuild —
	// and purgeTextCache — every frame, which would read as a huge alloc count.
	a.themeFonts.e[elemShowname] = themeElemFont{pct: themeFontPct(14), bold: true}
	a.themeFonts.e[elemMessage] = themeElemFont{pct: themeFontPct(13)}
	a.themeFonts.e[elemICChatlog] = themeElemFont{pct: themeFontPct(11)}
	a.themeFonts.e[elemServerChatlog] = themeElemFont{pct: themeFontPct(9)}
	a.themeFonts.e[elemMusicList] = themeElemFont{pct: themeFontPct(8), bold: true}
	a.themeFonts.e[elemMusicName] = themeElemFont{pct: themeFontPct(10)}
	a.themeFonts.e[elemAreaList] = themeElemFont{pct: themeFontPct(9)}

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	settle(draw)

	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled themed-font drawCourtroom allocates %.1f/op, want 0 — the per-element font path leaks (fix the alloc, don't loosen the gate)", n)
	}
}

// ao2DefaultDesignRects is AO2's stock `default` theme courtroom_design.ini,
// transcribed verbatim from AO2-Client/bin/base/themes/default/courtroom_design.ini
// (courtroom :3, viewport :11, ic_chatlog :14, ms_chatlog/server_chatlog :17/:20,
// ooc_chat_message :24, ooc_chat_name :27, music_list :38, emotes :62,
// hold_it/objection/take_that :91-93, custom_objection :100, pos_dropdown :105,
// the judge strip — defense/prosecution ± :126-129 and witness_testimony /
// cross_examination :132-133 — call_mod :138, ao2_ic_chat_name :170,
// pair_button :205, ao2_chatbox :243, showname :250, message :274,
// ao2_ic_chat_message :284, evidence_button :304).
//
// It is a TABLE, not a pile of magic numbers: the point of the themed gate is
// that the geometry it measures is the geometry real players run, so inventing
// rects would gate a layout nobody has. `courtroom` + `viewport` are the two
// keys themeLayout() requires (theme_layout.go), and the rest are picked to make
// every always-drawn themed region resolve: the merged/split chatlogs, the OOC
// inputs, the music/areas/players panel, the whole IC control row, the shout and
// judge button strips, the emote grid, and the chatbox with its chatbox-relative
// showname/message children.
var ao2DefaultDesignRects = map[string]theme.Rect{
	"courtroom":           {X: 0, Y: 0, W: 714, H: 579},
	"viewport":            {X: 0, Y: 0, W: 256, H: 192},
	"ic_chatlog":          {X: 260, Y: 0, W: 231, H: 220},
	"ms_chatlog":          {X: 490, Y: 1, W: 224, H: 277},
	"server_chatlog":      {X: 490, Y: 1, W: 224, H: 277},
	"ooc_chat_message":    {X: 492, Y: 281, W: 222, H: 19},
	"ooc_chat_name":       {X: 492, Y: 300, W: 85, H: 19},
	"music_list":          {X: 490, Y: 342, W: 224, H: 236},
	"emotes":              {X: 5, Y: 253, W: 490, H: 98},
	"hold_it":             {X: 10, Y: 221, W: 76, H: 28},
	"objection":           {X: 90, Y: 221, W: 76, H: 28},
	"take_that":           {X: 170, Y: 221, W: 76, H: 28},
	"custom_objection":    {X: 250, Y: 221, W: 76, H: 28},
	"pos_dropdown":        {X: 222, Y: 380, W: 60, H: 20},
	"witness_testimony":   {X: 290, Y: 380, W: 85, H: 42},
	"cross_examination":   {X: 290, Y: 425, W: 85, H: 42},
	"defense_plus":        {X: 183, Y: 476, W: 9, H: 9},
	"defense_minus":       {X: 5, Y: 476, W: 9, H: 9},
	"prosecution_plus":    {X: 183, Y: 492, W: 9, H: 9},
	"prosecution_minus":   {X: 5, Y: 492, W: 9, H: 9},
	"call_mod":            {X: 104, Y: 547, W: 64, H: 23},
	"pair_button":         {X: 104, Y: 425, W: 42, H: 42},
	"evidence_button":     {X: 627, Y: 322, W: 85, H: 18},
	"ao2_ic_chat_name":    {X: 200, Y: 444, W: 78, H: 23},
	"ao2_ic_chat_message": {X: 0, Y: 192, W: 256, H: 23},
	"ao2_chatbox":         {X: 0, Y: 114, W: 256, H: 78},
	"showname":            {X: 1, Y: 0, W: 46, H: 15},
	"message":             {X: 10, Y: 13, W: 242, H: 57},
}

// stageThemedCourtroom is stageSettledCourtroom plus the theme geometry that
// makes screens.go dispatch to drawCourtroomThemed instead of the classic path.
// The classic gates never reach that function: their fixture leaves themeRects
// empty, so themeLayout() returns lay.valid=false and drawCourtroom takes the
// classic branch — which is exactly how the themed composite shipped ungated.
//
// asyncao_toolbox is added on TOP of the stock table (AsyncAO's own optional key,
// consumed in drawCourtroomThemed) so the compact-toolbox region is anchored at a
// theme rect rather than the classic slot default — that arm is only reachable
// under a theme, so the classic gates cannot cover it either.
func stageThemedCourtroom(t *testing.T) (*App, func()) {
	t.Helper()
	a, cleanup := stageSettledCourtroom(t)
	a.themeRects = make(map[string]theme.Rect, len(ao2DefaultDesignRects)+1)
	for k, r := range ao2DefaultDesignRects {
		a.themeRects[k] = r
	}
	// toolboxDesignRect: the compact toolbox strip parked in the one genuinely EMPTY
	// band of the stock 714×579 design space — x 5…125, y 354…378, between the emote
	// grid's bottom edge (emotes = 5,253,490,98 ends at y 351) and pos_dropdown
	// (y 380). It must be clear of the table above, because the toolbox publishes an
	// overlay fence over exactly this rect and a fence landing on a live widget would
	// change what the frame hit-tests — the gate would then be measuring a different
	// screen than a real theme draws. (It used to sit at y 300, which is INSIDE
	// `emotes`.) What the gate measures is that the themed arm of the toolbox
	// (a.toolboxThemeRectOn) draws without allocating.
	toolboxDesignRect := theme.Rect{X: 5, Y: 354, W: 120, H: 24}
	a.themeRects["asyncao_toolbox"] = toolboxDesignRect
	// A SEPARATE copy, exactly like the live theme apply (app.go): the layout
	// editor's Reset writes themeRectsOrig back into themeRects, so aliasing one
	// map would silently make Reset a no-op for anyone who extends this fixture.
	a.themeRectsOrig = cloneRects(a.themeRects)
	a.d.Prefs.SetThemeLayout(true) // default-ON today; pinned here so a default flip can't silently un-gate this
	a.themeLay.valid = false       // force the first frame to build the cache from the rects above
	return a, cleanup
}

// TestDrawCourtroomThemedZeroAlloc is the whole-screen gate for the THEMED
// courtroom — the layout an AO2 theme with courtroom_design.ini geometry
// actually renders, and the one branch TestDrawCourtroomZeroAlloc above can
// never reach. It exists because drawCourtroomThemed could not have passed a
// gate until the &court / &box / &cell cgo pointer arguments became the shared
// c.cgoRect scratch: a pointer argument escapes through cgo, so &local
// heap-allocated on every themed frame (see the cgoRect contract in ui.go).
func TestDrawCourtroomThemedZeroAlloc(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	// Prove the fixture really took the themed branch before measuring anything:
	// a silently-classic frame would make this a duplicate of the gate above and
	// leave the themed path uncovered again.
	draw()
	if !a.themeLay.valid {
		t.Fatal("themeLayout did not validate — the fixture's rects never reached the themed branch")
	}
	if !a.toolboxThemeRectOn {
		t.Fatal("drawCourtroom took the CLASSIC branch — only drawCourtroomThemed arms toolboxThemeRectOn")
	}
	settle(draw)

	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled themed drawCourtroom allocates %.1f/op, want 0 — a per-frame allocation shipped (fix the alloc, don't loosen the gate)", n)
	}
}

// TestDrawCourtroomThemedDivergentZoomZeroAlloc is the same themed gate with the
// panel zooms DIVERGED — one Ctrl+wheel over the OOC log and one over the music
// list, both landing inside [MinLogScalePercent, MaxLogScalePercent], i.e. a
// perfectly ordinary saved state.
//
// It exists because equal zooms hid a whole class of bug. The themed frame draws
// the IC log, the OOC log AND the music list at once; while all three asked for the
// same percent, one shared fontSet answered all three and never rebuilt. Diverge
// any of them and that set was rebuilt — with a full purgeTextCache and text-atlas
// teardown — on EVERY FRAME. The zoom knobs are per panel by design, so "they
// happen to match" is not an invariant anything may rely on.
func TestDrawCourtroomThemedDivergentZoomZeroAlloc(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	// Two legal, in-range divergences in opposite directions from the IC log.
	a.oocPct = a.logPct + divergentZoomStepPct
	a.musicPct = a.logPct - divergentZoomStepPct
	for _, pct := range []int{a.logPct, a.oocPct, a.musicPct} {
		if pct < config.MinLogScalePercent || pct > config.MaxLogScalePercent {
			t.Fatalf("fixture zoom %d%% is outside the user-reachable range [%d, %d]",
				pct, config.MinLogScalePercent, config.MaxLogScalePercent)
		}
	}

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	draw()
	if !a.themeLay.valid || !a.toolboxThemeRectOn {
		t.Fatal("the fixture did not reach the themed branch")
	}
	settle(draw)

	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled themed drawCourtroom with divergent panel zooms allocates %.1f/op, want 0 — "+
			"two panels are sharing one fontSet and rebuilding it every frame (fix the alloc, don't equalise the fixture)", n)
	}
}

// divergentZoomStepPct is how far the sibling gate above pushes two panel zooms
// apart. 20 points is a couple of Ctrl+wheel notches — big enough that the two
// scales cannot round to one font size, small enough to stay inside the user
// clamps at the 100% default (75 … 250 today).
const divergentZoomStepPct = 20

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
	// noteScreenTransition rides inside the gated closure: its settled-frame
	// early-return (screen == drawnScreen, both ScreenLobby here) is the guard
	// the lobby-entry auto-refresh leans on, so hoisting per-frame work above
	// that early-return — a clock read, the due-check — would trip this gate
	// instead of shipping unmeasured.
	draw := func() { a.noteScreenTransition(); a.drawLobby(w, h) }
	settle(draw)

	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled drawLobby allocates %.1f/op, want 0 — a per-frame allocation shipped (fix the alloc, don't loosen the gate)", n)
	}
}
