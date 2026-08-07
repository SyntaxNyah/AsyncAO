package ui

// THE FRAME HARNESS — the one fixture in internal/ui that drives a real App.Frame.
//
// WHY IT EXISTS. `grep 'a\.Frame(' internal/ui/*_test.go` had no hits, and that one
// mechanical fact is the largest single cause of uncaught seams in this package: the
// functions were tested and the WIRING was not. Every chip's input and paint call
// site, the poll cluster, the menu bar's two phases, the element pass hook and the
// theme apply's landing all live inside App.Frame, so a test that calls
// drawCourtroom (as every gate here did) reaches none of them. Deleting
// `a.handleThemeErrChipInput(winW)`, `a.pollThemeApply()` or
// `a.beginThemeFXPass()`'s only call site left the suite green.
//
// WHAT IT IS. stageThemedCourtroom — the fixture the alloc gates already use, so
// the same App, the same renderer, the same settled scene — plus the three
// dependencies App.Frame dereferences unconditionally (Manager, Pump, Audio) and the
// latches that keep a headless frame off the network and off the clock. Nothing is
// stubbed: the frame that runs is the frame the client runs.
//
// RULE 11, applied to the harness itself:
//
//   - SINGLE RESPONSIBILITY. Two functions with one concern each and no "and":
//     stageFrameDrivenApp BUILDS an App a frame can run on; driveFrame RUNS one. They
//     are separate because their lifetimes differ — a fixture is built once and a
//     frame is driven many times, and a gate that needs ten frames must not rebuild
//     the world ten times.
//   - OPEN–CLOSED. OPEN to any gate that needs a real frame; the named concrete
//     future call sites are the ledger's remaining frame-only rows (the base
//     browser's memo protocol, the band-chip anchor chain, W7's editor pass). CLOSED
//     against the existing suite: it adds a fixture and changes no assertion anywhere,
//     and stageThemedCourtroom itself is untouched, so every alloc gate built on it
//     measures the same frame it measured before.
//   - SUBSTITUTABILITY. The harness's whole contract is "a.Frame is the real one".
//     driveFrame therefore calls App.Frame and nothing else — no re-implementation of
//     its order, no shortcut through drawCourtroom — and
//     TestFrameGatesDriveTheRealFrame is the mechanical proof that no gate built on
//     this fixture quietly swaps one for the other.
//   - INTERFACE SEGREGATION. driveFrame takes (t, a, n): a count, not a bag of knobs.
//     Anything a gate wants to vary it varies on the App it was handed, which is the
//     same surface production code varies.
//   - DEPENDENCY DIRECTION. The harness depends on internal/ui's own entry point and
//     on the same internal/assets + internal/render constructors cmd/asyncao uses. It
//     reaches into nothing, and it defines no type the production code must know about.
//
// SDL: skips (never fails) without a video driver, via newCaptureHarness.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
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
//
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
	a.updateChecked = true
	// updatePresence is the other frame-1 one-shot. Presence is nil in tests and the
	// call is guarded, but latching it keeps the frame free of first-frame-only work
	// so frame 1 and frame 2 are comparable.
	a.presenceInit = true
	a.d.Prefs.SetFormatAutoDetect(false) // no extensions.json probe interposes
	return a, cleanup
}

// driveFrame runs ONE real frame at the harness geometry, in the event loop's own
// order: Ctx.BeginFrame, then App.Frame.
//
// BOTH HALVES, and the order is load-bearing rather than tidy. cmd/asyncao's loop
// calls BeginFrame before it polls events, and BeginFrame is what clears the
// per-frame input snapshot AND ZEROES THE OVERLAY FENCE REGISTRY — "a pass that
// returns early still starts the next frame with a clear registry" (overlayfence.go).
// A harness that called App.Frame alone would accumulate every frame's fences
// forever, so from frame two onward the menu bar's own strip fence would cover the
// band's three chips and every one of them would hit-test dead. That is not a
// property of the client; it is a property of half a loop.
//
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
//
// The input is seeded BETWEEN BeginFrame and App.Frame, which is exactly where
// HandleEvent puts it in the live loop: BeginFrame clears the snapshot (and
// re-reads the cursor from SDL, which is (0,0) under the dummy driver), so anything
// stamped before it is erased. downX/downY are seeded too, because the kit's
// committed-click test (ClickedIn) requires the press to have STARTED on the rect —
// a release that drifted in does not count.
func driveClickAt(a *App, x, y int32) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.mouseX, a.ctx.mouseY = x, y
	a.ctx.downX, a.ctx.downY = x, y
	a.ctx.clicked = true
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// driveHotkey runs one real frame carrying a Ctrl chord.
//
// ON c.hotkey, NEVER c.keyPressed, and that is the whole reason it exists: HandleEvent
// routes every non-clipboard Ctrl combination to c.hotkey and returns (#96 configurable
// hotkeys), so a gate that stamped keyPressed would be driving a key the client never
// delivers and would pass against a dispatcher that does not exist. Seeded between
// BeginFrame (which zeroes hotkey and re-reads the modifier state from SDL) and
// App.Frame, exactly where the event loop seeds it; ctrlHeld is set for the same
// fidelity, because BeginFrame derives it from SDL's mod state, which the dummy driver
// leaves clear.
func driveHotkey(a *App, key sdl.Keycode) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.hotkey = key
	a.ctx.ctrlHeld = true
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// driveUntilThemeApplied kicks a theme apply and drives REAL FRAMES until
// pollThemeApply — App.Frame's own call, not a hand call — has landed it.
//
// This is what makes every apply-tier gate below a call-site gate: awaitThemeApply
// (panelfonts_test.go) calls a.pollThemeApply() itself, so deleting the call from
// App.Frame leaves it green. Here the only thing that can land the result is the
// frame.
func driveUntilThemeApplied(t *testing.T, a *App) {
	t.Helper()
	gen := a.applyThemeAsync()
	for i := 0; i < frameHarnessApplyFrames; i++ {
		driveFrame(a)
		if a.themeAppliedGen >= gen {
			return
		}
	}
	t.Fatalf("no theme apply landed in %d real frames (generation %d, applied %d) — "+
		"App.Frame is not draining the apply result", frameHarnessApplyFrames, gen, a.themeAppliedGen)
}

// ---------------------------------------------------------------------------
// The harness's own encapsulation gates
// ---------------------------------------------------------------------------

// TestTheFrameHarnessReallyDrivesAFrame proves the fixture is not a decoration: one
// call to driveFrame must leave behind the marks only App.Frame makes.
//
// Each assertion names a DIFFERENT region of the frame, so the gate fails loudly if
// the harness is ever quietly rewired to a screen draw:
//
//   - lastFrameDrawn is stamped at the top of Frame and nowhere else;
//   - the menu-bar latch is taken by menuBarFrame (phase 1), which sits ABOVE the
//     screen dispatch and which no drawCourtroom call reaches;
//   - themeFXFrame is advanced by beginThemeFXPass, which rides the element pass;
//   - the themed branch really ran, so the gates below are measuring the layout a
//     theme renders rather than the classic fallback.
func TestTheFrameHarnessReallyDrivesAFrame(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	before := a.themeFXFrame
	driveFrame(a)

	if a.lastFrameDrawn.IsZero() {
		t.Fatal("App.Frame did not stamp lastFrameDrawn — the harness is not driving a frame at all")
	}
	if !a.menuBar.draws {
		t.Fatal("the menu bar's phase-1 latch was never taken — menuBarFrame runs above the screen " +
			"dispatch, so nothing that calls drawCourtroom can reach it")
	}
	if !a.themeLay.valid {
		t.Fatal("the themed layout never validated — the frame took the classic branch and every " +
			"element/theme gate below would be measuring nothing")
	}
	if a.themeFXFrame == before {
		t.Fatal("beginThemeFXPass never ran — the element pass hook is the one both the live " +
			"courtroom and the offscreen export share")
	}
	// And a second frame advances the pass exactly once more: the hook is per PASS,
	// and a double-open would sweep every stateful effect a frame early.
	one := a.themeFXFrame
	driveFrame(a)
	if got := a.themeFXFrame - one; got != 1 {
		t.Fatalf("one frame advanced the FX pass by %d, want exactly 1 — the pass hook fired %d times "+
			"in one frame, which halves themeFXIdleFrames for every one-shot", got, got)
	}
}

// TestFrameGatesDriveTheRealFrame is the harness's anti-bypass gate.
//
// A fixture whose users can quietly stop using it is not a seam. Every file that
// stages the frame harness must also drive it through driveFrame / driveFrames /
// driveUntilThemeApplied — i.e. through App.Frame — because the moment one of them
// reaches for a.drawCourtroom instead, it is back to testing the function while the
// wiring goes uncovered, which is exactly the state this file was written to end.
func TestFrameGatesDriveTheRealFrame(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob the package tests: %v (%d files)", err, len(files))
	}
	const stager = "stageFrameDrivenApp"
	drivers := []string{"driveFrame", "driveFrames", "driveUntilThemeApplied"}
	fset := token.NewFileSet()
	staged := 0
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		uses, drives := false, false
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if id.Name == stager {
				uses = true
			}
			for _, d := range drivers {
				if id.Name == d {
					drives = true
				}
			}
			return true
		})
		if !uses {
			continue
		}
		staged++
		if !drives {
			t.Errorf("%s stages the frame harness but never drives a frame (%s) — a gate on this "+
				"fixture that calls a screen draw directly is testing the function, not the wiring",
				name, strings.Join(drivers, " / "))
		}
	}
	if staged == 0 {
		t.Fatalf("no test file stages %s — the harness has no users, so it pins nothing", stager)
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
//
// So this drives real frames and asks the two questions a user would:
//
//  1. IS IT THERE — the chip's label reaches the label cache during App.Frame, which
//     means drawMenuBar's `a.drawThemeErrChip(w)` really ran with a rect it accepted.
//  2. DOES IT WORK — a click at its centre, resolved by App.Frame's own
//     handleThemeErrChipInput call (pre-screen, above the strip's fence), opens the
//     debug log and acknowledges the refusal.
//
// Mutations it answers to: `return sdl.Rect{}, false` at the top of themeErrChipRect;
// deleting `a.drawThemeErrChip(w)` from drawMenuBar; deleting
// `a.handleThemeErrChipInput(winW)` from App.Frame.
func TestSidecarRefusalChipReachesTheScreenAndTakesItsClick(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	// A clean install paints no chip at all — the "cries wolf" half, measured through
	// the same real frame so it cannot pass for the wrong reason.
	driveFrame(a)
	if textCacheHasLabel(a.ctx, themeErrChipLabel) {
		t.Fatal("the refusal chip painted on a fixture with no theme error")
	}

	a.themeSidecarErr = themeSidecarRefusalLine("harness", t.TempDir(), theme.ErrSidecarCap)
	a.themeSidecarTip = themeSidecarRefusalTip("harness", theme.ErrSidecarCap)
	if a.themeSidecarErr == "" || a.themeSidecarTip == "" {
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
	a.showDebugPanel = false
	cx, cy := frameHarnessRectCentre(r)
	driveClickAt(a, cx, cy)
	if !a.themeSidecarErrAck {
		t.Error("a click on the chip did not acknowledge the refusal — App.Frame is not claiming its " +
			"input before the screens see it")
	}
	if !a.showDebugPanel {
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
//
// `[clock.N]` was in the same parsed-documented-never-invoked state `[overrides]`
// shipped in for four waves, and this is the shape that catches it:
// TestThemeApplyRunsTheOverridesTier's, one level up — the whole path from the
// preference to the pool, landed by a REAL FRAME.
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
	if got := a.themeClocks[1].speedPct; got == clockHarnessSpeedPct {
		t.Fatalf("clock 1 already reads %d before the apply — the gate would pass with the call deleted", got)
	}
	a.d.Prefs.SetTheme("clockharness", root)
	driveUntilThemeApplied(t, a)

	if a.themeSidecar == nil {
		t.Fatal("the apply landed no sidecar — the fixture theme did not load, so nothing below " +
			"is measuring the clock tier")
	}
	if got := a.themeClocks[1].speedPct; got != clockHarnessSpeedPct {
		t.Fatalf("clock 1 runs at %d%% after the apply, want the theme's authored %d%% — "+
			"pollThemeApply is not calling applyThemeClocks, so every [clock.N] group in every "+
			"theme is inert and each one silently runs at the shared anchor's rate",
			got, clockHarnessSpeedPct)
	}
	// Clock 0 is the shared anchor and is never declared: it must come back nominal,
	// or a theme declaring one group would re-time every element that named none.
	if got := a.themeClocks[0].speedPct; got != themeSpeedNominal {
		t.Errorf("clock 0 runs at %d%%, want the nominal %d%% — the shared anchor is not the anchor",
			got, themeSpeedNominal)
	}
	// The apply also drops the stateful-effect pool, because every slot's generation
	// went stale with the bake. A slot surviving one would freeze the incoming
	// theme's one-shots at frame 0 until the idle sweep collected it.
	for i := range a.themeFX {
		if a.themeFX[i].owner != fxOwnerNone {
			t.Fatalf("FX slot %d survived a theme apply owning element %d — the incoming theme's "+
				"one-shots are refused until the sweep runs", i, a.themeFX[i].owner-1)
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
//
// It is frame-driven because the ordering only exists inside a frame: the pass opens
// at the top of drawCourtroomThemed's element work and the three bands paint after it,
// which is a fact about App.Frame's dispatch and about nothing smaller.
//
// The mutation it answers to: delete `a.beginThemeFXPass()` from
// refreshElementConditionsFrom. The stamp then never advances, so every painted
// element is seen at a pass number that did not move.
func TestEveryElementPaintHappensInsideAnOpenPass(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()

	driveFrame(a) // one frame first: themeLayoutIn replaces the whole cache on its cold path
	lay := &a.themeLay
	seedBakedElements(a, lay, elementPassGateCount, seedElementArt(t, a))
	if lay.elN != elementPassGateCount {
		t.Fatalf("seeded %d elements, want %d", lay.elN, elementPassGateCount)
	}

	// Record the pass stamp AT EACH PAINT, which is the only place the question can
	// be asked honestly.
	var stamps []uint64
	saved := elemPainters
	for k := range elemPainters {
		elemPainters[k] = func(app *App, _ *bakedElement) { stamps = append(stamps, app.themeFXFrame) }
	}
	t.Cleanup(func() { elemPainters = saved })

	before := a.themeFXFrame
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
