package ui

// THE FRAME HARNESS — the one fixture in internal/ui that drives a real App.Frame.
//     its order, no shortcut through drawCourtroom — and
// SDL: skips (never fails) without a video driver, via newCaptureHarness.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// frameHarnessW / frameHarnessH are the logical window the harness drives. The
	// same 1280x720 every alloc gate uses, so a rect measured there and a rect
	// measured here are the same rect.
	frameHarnessW = int32(1280)
	frameHarnessH = int32(720)
	// frameHarnessDt is the per-frame delta handed to App.Frame — one 60 Hz tick.
	// FIXED rather than wall-clock so a slow machine cannot change what a gate sees.
	frameHarnessDt = 16 * time.Millisecond
	// frameHarnessPoolWorkers is the network pool the harness's Manager owns. Two:
	// the manager refuses to be built without a pool, and no gate here fetches
	// anything (LocalMode, a t.TempDir source, autodetect off).
	frameHarnessPoolWorkers = 2
	// frameHarnessApplyFrames bounds how many frames driveUntilThemeApplied spends
	// waiting for an apply goroutine. At 16 ms of simulated time each this is a
	// generous real-time budget with a hard stop, so a stuck apply fails the gate
	// loudly instead of hanging the package.
	frameHarnessApplyFrames = 4000
)

// stageFrameDrivenApp is a themed, settled courtroom App that App.Frame can be
// called on directly.
// Everything it adds beyond stageThemedCourtroom is either a dependency Frame
// dereferences without a nil check, or a one-shot latch that would otherwise start a
// goroutine or a network read on frame 1. Nothing here changes what is DRAWN.
func stageFrameDrivenApp(t *testing.T) (*App, func()) {
	t.Helper()
	a, cleanup := stageThemedCourtroom(t)

	// --- the three dependencies App.Frame dereferences unconditionally ---
	resolver := assets.NewResolver(a.d.Prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(frameHarnessPoolWorkers)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(frameHarnessPoolWorkers)
	t.Cleanup(decoder.Close)
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver:  resolver,
		Prefs:     a.d.Prefs,
		T2:        t2,
		Disk:      disk,
		Source:    assets.NewLocalFetcher([]string{t.TempDir()}),
		LocalMode: true,
		Pool:      pool,
		Decoder:   decoder,
	})
	a.d.Pump = render.NewPump(a.d.Store, a.d.Manager, nil)
	// The REAL audio sink, built exactly as cmd/asyncao builds it. NewAudio degrades
	// to a disabled-but-functional device when the mixer cannot open one (which is
	// the headless case), and Frame drains the manager's audio channel through it
	// either way — a zero-value &render.Audio{} would nil-deref on that drain.
	a.d.Audio = render.NewAudio(a.d.Manager)
	t.Cleanup(a.d.Audio.Close) // pair the device (or the failed open) exactly as the app does

	// --- latches: a headless frame stays off the network and off the wall clock ---
	// maybeKickUpdateCheck spawns two goroutines on frame 1 (a .old cleanup and the
	// release check). Both are one-shot behind this flag, so setting it is exactly
	// what a second frame would have done, minus the network.
	a.updates.checked = true
	// updatePresence is the other frame-1 one-shot. Presence is nil in tests and the
	// call is guarded, but latching it keeps the frame free of first-frame-only work
	// so frame 1 and frame 2 are comparable.
	a.srvMeta.presenceInit = true
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			AutoDetectFormats =

			false

	})

	// no extensions.json probe interposes
	// macroBind is the one App field whose ZERO VALUE means "armed" — slot 0 of the
	// macro key-capture — which is why the real constructor spells it out
	// (app.go:3322). A hand-built fixture that leaves it at 0 reports
	// capturingKey() == true on every frame, so every input path that steps aside
	// for an armed capture (the IC focus claim, every bare-key bind) is silently
	// disabled and pollMacroBind eats the first keystroke into a preference. The
	// gates read as "the feature is broken"; the feature was never reached.
	a.budget.macroBind = macroBindIdle
	return a, cleanup
}

// macroBindIdle is App.macroBind's "no capture armed" sentinel, as app.go:3322
// constructs it. Named here because -1 in a fixture is exactly the magic number
// that made the harness lie.
const macroBindIdle = -1

// driveFrame runs ONE real frame at the harness geometry, in the event loop's own
// order: Ctx.BeginFrame, then App.Frame.
// BOTH HALVES, and the order is load-bearing rather than tidy. cmd/asyncao's loop
// A harness that called App.Frame alone would accumulate every frame's fences
// forever, so from frame two onward the menu bar's own strip fence would cover the
// band's three chips and every one of them would hit-test dead. That is not a
// It is the only way a gate advances the harness, and that is the point: a gate that
// reached for drawCourtroom instead would be back to testing the function and not the
// wiring, which is the whole failure this file exists to end.
func driveFrame(a *App) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// driveFrames runs n consecutive real frames.
func driveFrames(a *App, n int) {
	for i := 0; i < n; i++ {
		driveFrame(a)
	}
}

// driveClickAt runs one real frame carrying a committed left-click at (x, y).
func driveClickAt(a *App, x, y int32) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.mouseX, a.ctx.mouseY = x, y
	a.ctx.downX, a.ctx.downY = x, y
	a.ctx.clicked = true
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// driveTypedText runs one real frame carrying TEXT INPUT — one SDL_TEXTINPUT
// event's worth, which is what a printable keystroke actually delivers.
func driveTypedText(a *App, s string) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.typed = s
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// driveHotkey runs one real frame carrying a Ctrl chord.
// routes every non-clipboard Ctrl combination to c.hotkey and returns (#96 configurable
func driveHotkey(a *App, key sdl.Keycode) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.hotkey = key
	a.ctx.ctrlHeld = true
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// driveEnter runs one real frame carrying a RETURN keypress.
func driveEnter(a *App) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.enter = true
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// driveUntilThemeApplied kicks a theme apply and drives REAL FRAMES until
// pollThemeApply — App.Frame's own call, not a hand call — has landed it.
func driveUntilThemeApplied(t *testing.T, a *App) {
	t.Helper()
	gen := a.applyThemeAsync()
	for i := 0; i < frameHarnessApplyFrames; i++ {
		driveFrame(a)
		if a.skin.themeAppliedGen >= gen {
			return
		}
	}
	t.Fatalf("no theme apply landed in %d real frames (generation %d, applied %d) — "+
		"App.Frame is not draining the apply result", frameHarnessApplyFrames, gen, a.skin.themeAppliedGen)
}

// ---------------------------------------------------------------------------
// The harness's own encapsulation gates
// ---------------------------------------------------------------------------

// TestTheFrameHarnessReallyDrivesAFrame proves the fixture is not a decoration: one
// call to driveFrame must leave behind the marks only App.Frame makes.
//   - lastFrameDrawn is stamped at the top of Frame and nowhere else;
//   - the menu-bar latch is taken by menuBarFrame (phase 1), which sits ABOVE the
//     screen dispatch and which no drawCourtroom call reaches;
func TestTheFrameHarnessReallyDrivesAFrame(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	before := a.fxPool.themeFXFrame
	driveFrame(a)

	if a.frame.lastFrameDrawn.IsZero() {
		t.Fatal("App.Frame did not stamp lastFrameDrawn — the harness is not driving a frame at all")
	}
	if !a.menuBar.draws {
		t.Fatal("the menu bar's phase-1 latch was never taken — menuBarFrame runs above the screen " +
			"dispatch, so nothing that calls drawCourtroom can reach it")
	}
	if !a.skin.themeLay.valid {
		t.Fatal("the themed layout never validated — the frame took the classic branch and every " +
			"element/theme gate below would be measuring nothing")
	}
	if a.fxPool.themeFXFrame == before {
		t.Fatal("beginThemeFXPass never ran — the element pass hook is the one both the live " +
			"courtroom and the offscreen export share")
	}
	// And a second frame advances the pass exactly once more: the hook is per PASS,
	// and a double-open would sweep every stateful effect a frame early.
	one := a.fxPool.themeFXFrame
	driveFrame(a)
	if got := a.fxPool.themeFXFrame - one; got != 1 {
		t.Fatalf("one frame advanced the FX pass by %d, want exactly 1 — the pass hook fired %d times "+
			"in one frame, which halves themeFXIdleFrames for every one-shot", got, got)
	}
}

// ---------------------------------------------------------------------------
// Pinned wiring 1 — the ship-blocker chip reaches the screen (ledger L6)
// ---------------------------------------------------------------------------

// TestSidecarRefusalChipReachesTheScreenAndTakesItsClick is the frame-driven half of
// the refusal surface, and it closes the gap the ledger names: themeErrChipRect could
// `return sdl.Rect{}, false` unconditionally and the suite stayed green, because the
// only geometry gate t.Skip()s on exactly that condition and the visibility gate
// asserts nothing but themeErrChipActive().
// So this drives real frames and asks the two questions a user would:
func TestSidecarRefusalChipReachesTheScreenAndTakesItsClick(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	// A clean install paints no chip at all — the "cries wolf" half, measured through
	// the same real frame so it cannot pass for the wrong reason.
	driveFrame(a)
	if textCacheHasLabel(a.ctx, themeErrChipLabel) {
		t.Fatal("the refusal chip painted on a fixture with no theme error")
	}

	a.fxPool.themeSidecarErr = themeSidecarRefusalLine("harness", t.TempDir(), theme.ErrSidecarCap)
	a.fxPool.themeSidecarTip = themeSidecarRefusalTip("harness", theme.ErrSidecarCap)
	if a.fxPool.themeSidecarErr == "" || a.fxPool.themeSidecarTip == "" {
		t.Fatal("the fixture produced no refusal strings — nothing below is measuring the chip")
	}
	driveFrame(a)

	if !a.menuBarPaints() {
		t.Fatal("the menu bar is not painting in a plain courtroom frame — the band's three chips " +
			"have no home, and the geometry below cannot be measured (this is the condition the " +
			"old gate skipped on; it must be a failure, not a skip)")
	}
	r, ok := a.themeErrChipRect(frameHarnessW)
	if !ok {
		t.Fatalf("themeErrChipRect refused a rect while the refusal is live and the bar is painting — "+
			"the ship-blocker chip is invisible (titles end at %d, window %d)",
			a.menuBarTitlesRight(), frameHarnessW)
	}
	if !textCacheHasLabel(a.ctx, themeErrChipLabel) {
		t.Fatalf("the chip has a rect %+v but its label never reached the screen — drawMenuBar is "+
			"not painting it", r)
	}

	// The click, through App.Frame's own pre-screen claim.
	a.debugPanel.open = false
	cx, cy := frameHarnessRectCentre(r)
	driveClickAt(a, cx, cy)
	if !a.fxPool.themeSidecarErrAck {
		t.Error("a click on the chip did not acknowledge the refusal — App.Frame is not claiming its " +
			"input before the screens see it")
	}
	if !a.debugPanel.open {
		t.Error("the click did not open the debug log, which is where the full refusal line lives")
	}
	// Acknowledged: it stops painting, so the dismissal is real.
	driveFrame(a)
	a.ctx.purgeTextCache()
	driveFrame(a)
	if textCacheHasLabel(a.ctx, themeErrChipLabel) {
		t.Error("the acknowledged chip is still painting — it cannot be dismissed")
	}
}

// ---------------------------------------------------------------------------
// Pinned wiring 2 — the clock pool lands with the theme (ledger L5)
// ---------------------------------------------------------------------------

// clockHarnessSpeedPct is the fixture theme's clock-1 speed. Off nominal in a way no
// default produces, and inside [theme.SpeedMinPct, theme.SpeedMaxPct] so the reader's
// own clamp cannot be what the gate is reading.
const clockHarnessSpeedPct = int16(250)

// TestThemeApplyLoadsTheClockPoolThroughAFrame pins app.go's
// `a.applyThemeClocks(res.sidecar)` — a call the suite could delete and stay green,
// because both clock gates call applyThemeClocks themselves and the shared fixture
// assigns a.themeSidecar by hand without ever running pollThemeApply.
func TestThemeApplyLoadsTheClockPoolThroughAFrame(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	root := t.TempDir()
	dir := filepath.Join(root, theme.ThemesDirName, "clockharness")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, theme.DesignFileName),
		[]byte("courtroom = 0, 0, 714, 579\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// One [clock.1] group, and NOTHING else this gate reads. Written as a file and
	// parsed by the real reader, so a row this test believes it wrote is a row the
	// reader would actually produce.
	sidecar := "[clock.1]\nspeed_pct = " + formatClockHarnessSpeed() + "\n"
	if err := os.WriteFile(filepath.Join(dir, theme.SidecarFileName), []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}

	// The pool starts at the stock answer, so "the apply loaded it" is a real change.
	if got := a.fxPool.themeClocks[1].speedPct; got == clockHarnessSpeedPct {
		t.Fatalf("clock 1 already reads %d before the apply — the gate would pass with the call deleted", got)
	}
	a.d.Prefs.SetTheme("clockharness", root)
	driveUntilThemeApplied(t, a)

	if a.elemBake.themeSidecar == nil {
		t.Fatal("the apply landed no sidecar — the fixture theme did not load, so nothing below " +
			"is measuring the clock tier")
	}
	if got := a.fxPool.themeClocks[1].speedPct; got != clockHarnessSpeedPct {
		t.Fatalf("clock 1 runs at %d%% after the apply, want the theme's authored %d%% — "+
			"pollThemeApply is not calling applyThemeClocks, so every [clock.N] group in every "+
			"theme is inert and each one silently runs at the shared anchor's rate",
			got, clockHarnessSpeedPct)
	}
	// Clock 0 is the shared anchor and is never declared: it must come back nominal,
	// or a theme declaring one group would re-time every element that named none.
	if got := a.fxPool.themeClocks[0].speedPct; got != themeSpeedNominal {
		t.Errorf("clock 0 runs at %d%%, want the nominal %d%% — the shared anchor is not the anchor",
			got, themeSpeedNominal)
	}
	// The apply also drops the stateful-effect pool, because every slot's generation
	// went stale with the bake. A slot surviving one would freeze the incoming
	// theme's one-shots at frame 0 until the idle sweep collected it.
	for i := range a.fxPool.themeFX {
		if a.fxPool.themeFX[i].owner != fxOwnerNone {
			t.Fatalf("FX slot %d survived a theme apply owning element %d — the incoming theme's "+
				"one-shots are refused until the sweep runs", i, a.fxPool.themeFX[i].owner-1)
		}
	}
}

// formatClockHarnessSpeed writes the fixture's speed as the INI would. A function
// rather than a literal beside the constant so the two cannot drift.
func formatClockHarnessSpeed() string {
	return itoa32(int32(clockHarnessSpeedPct))
}

// ---------------------------------------------------------------------------
// Pinned wiring 3 — nothing paints an element outside a pass (ledger L34)
// ---------------------------------------------------------------------------

// TestEveryElementPaintHappensInsideAnOpenPass pins the contract that has no
// compile-time expression: beginThemeFXPass rides INSIDE refreshElementConditionsFrom,
// so a band that painted without one would run on the previous pass's ReduceMotion
// answer, on slots nobody swept and on last frame's resolved conditions.
// It is frame-driven because the ordering only exists inside a frame: the pass opens
// at the top of drawCourtroomThemed's element work and the three bands paint after it,
// which is a fact about App.Frame's dispatch and about nothing smaller.
func TestEveryElementPaintHappensInsideAnOpenPass(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	driveFrame(a) // one frame first: themeLayoutIn replaces the whole cache on its cold path
	lay := &a.skin.themeLay
	seedBakedElements(a, lay, elementPassGateCount, seedElementArt(t, a))
	if lay.elN != elementPassGateCount {
		t.Fatalf("seeded %d elements, want %d", lay.elN, elementPassGateCount)
	}

	// Record the pass stamp AT EACH PAINT, which is the only place the question can
	// be asked honestly.
	var stamps []uint64
	saved := elemPainters
	for k := range elemPainters {
		elemPainters[k] = func(app *App, _ *bakedElement) { stamps = append(stamps, app.fxPool.themeFXFrame) }
	}
	t.Cleanup(func() { elemPainters = saved })

	before := a.fxPool.themeFXFrame
	driveFrame(a)
	elemPainters = saved

	if len(stamps) == 0 {
		t.Fatal("no element painted in a themed frame carrying elements — the band hooks are not " +
			"wired into the frame, so this gate would pass vacuously")
	}
	for i, got := range stamps {
		if got != before+1 {
			t.Fatalf("element paint %d ran at pass stamp %d, want %d (the stamp before the frame, "+
				"plus this frame's one pass) — an element painted OUTSIDE an open pass reads the "+
				"previous pass's ReduceMotion latch, unswept FX slots and stale conditions",
				i, got, before+1)
		}
	}
}

// elementPassGateCount is how many elements the pass gate seeds. Small on purpose:
// it is an ORDERING gate, not a density one — the cap-full case is the alloc gates'
// job (TestDrawElementBandsZeroAllocWith96Elements).
const elementPassGateCount = 6

// ---------------------------------------------------------------------------
// Shared helper
// ---------------------------------------------------------------------------

// itoa32 formats an int32 without pulling strconv into a file that otherwise needs
// no formatting. Test-only.
func itoa32(v int32) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// frameHarnessRectCentre is the middle of r, as the input pass would hit-test it.
// Used by the chip gates above and by anything later that clicks band chrome.
func frameHarnessRectCentre(r sdl.Rect) (int32, int32) { return r.X + r.W/2, r.Y + r.H/2 }
