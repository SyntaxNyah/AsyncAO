package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// #35 — window restore + auto-resize to the theme's design size.
//
// Two independent halves are pinned here:
//
//  1. CenteredWindowPos, the multi-monitor recentring fix. The bare
//     sdl.WINDOWPOS_CENTERED is SDL_WINDOWPOS_CENTERED_DISPLAY(0), so every
//     recenter used to teleport the window onto the primary display.
//  2. The one-shot that arms the automatic snap-to-canvas. The same theme-apply
//     pipeline is re-entered at boot, by healTheme's eviction repair, by the
//     texture-filter Purge recovery and whenever a server binds a theme; none of
//     those may move the window, so an explicit arming — not a name comparison —
//     is the contract, it NAMES the generation of the apply it belongs to, and it
//     must be consumed exactly once.
//
// The un-maximize half of the fix is NOT covered here: SDL's dummy video driver
// implements no MaximizeWindow, so SDL_WINDOW_MAXIMIZED can never be raised
// headlessly. It rests on the SDL sources quoted at Ctx.UnmaximizeWindow and
// needs a human at a real display.

// designCanvas is one theme's declared courtroom_design.ini geometry, in the
// shape pollThemeApply receives it. viewport is a quarter-size stage purely so
// themeLayout() has its second required key and survives minThemedElementPx.
func designCanvas(w, h int) map[string]theme.Rect {
	return map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: w, H: h},
		"viewport":  {X: 0, Y: 0, W: w / 4, H: h / 4},
	}
}

// stageThemeCanvas gives a headless App an already-applied theme canvas.
func stageThemeCanvas(a *App, w, h int) {
	rects := designCanvas(w, h)
	a.themeRectsOrig = rects
	a.themeRects = rects
	a.themeLay.valid = false
}

// startThemeApply allocates a generation the way applyThemeAsync does, WITHOUT
// running the off-thread load. Tests use it to model an apply that is in flight.
func startThemeApply(a *App) uint64 { return a.themeGen.Add(1) }

// landThemeApplyGen drives ONE real pollThemeApply landing for an already
// allocated generation, so the one-shot is pinned where it actually runs.
// Everything else on themeApply is left zero: with no images and no sounds the
// landing touches neither the (nil) TextureStore nor the (nil) room.
func landThemeApplyGen(a *App, gen uint64, layout map[string]theme.Rect) {
	a.themeRes.Store(&themeApply{gen: gen, name: "fixture", layout: layout})
	a.pollThemeApply()
}

// landThemeApply is the common case: allocate a generation and land it at once.
func landThemeApply(a *App, layout map[string]theme.Rect) uint64 {
	gen := startThemeApply(a)
	landThemeApplyGen(a, gen, layout)
	return gen
}

// TestCenteredWindowPosTargetsTheGivenDisplay pins the SDL magic value the
// recentring fix depends on. SDL_SetWindowPosition reads the target display out
// of the LOW 16 BITS of the position (SDL 2.32.10 src/video/SDL_video.c:
// `int displayIndex = (x & 0xFFFF);`), and SDL_video.h defines the bare
// SDL_WINDOWPOS_CENTERED as SDL_WINDOWPOS_CENTERED_DISPLAY(0). So "centered"
// literally means "on the primary monitor" unless the index is encoded — which
// is the bug: WindowDisplayUsable clamps the size against the display the window
// is ON, and the old recenter then dropped it on display 0 anyway.
func TestCenteredWindowPosTargetsTheGivenDisplay(t *testing.T) {
	if got := CenteredWindowPos(0); got != int32(sdl.WINDOWPOS_CENTERED) {
		t.Errorf("CenteredWindowPos(0) = %#x, want the bare sdl.WINDOWPOS_CENTERED %#x", got, int32(sdl.WINDOWPOS_CENTERED))
	}
	for _, di := range []int{1, 2, 7} {
		got := CenteredWindowPos(di)
		if uint32(got)&0xFFFF0000 != uint32(sdl.WINDOWPOS_CENTERED_MASK) {
			t.Errorf("CenteredWindowPos(%d) = %#x, lost the CENTERED mask", di, got)
		}
		if uint32(got)&0xFFFF != uint32(di) {
			t.Errorf("CenteredWindowPos(%d) = %#x, encodes display %d", di, got, uint32(got)&0xFFFF)
		}
		if got == int32(sdl.WINDOWPOS_CENTERED) {
			t.Errorf("CenteredWindowPos(%d) collapsed onto display 0 — the bug this fixes", di)
		}
	}
	// A negative index can only come from a failed SDL query; it must degrade to
	// the primary display, never produce a corrupt position word.
	if got := CenteredWindowPos(-1); got != int32(sdl.WINDOWPOS_CENTERED) {
		t.Errorf("CenteredWindowPos(-1) = %#x, want the primary-display value %#x", got, int32(sdl.WINDOWPOS_CENTERED))
	}
	// The guard has to be two-sided. SDL_WINDOWPOS_CENTERED_DISPLAY(X) ORs X in
	// WITHOUT masking it, and SDL reads the display back out of the low 16 bits
	// only, so an index that overflows that field corrupts the 0x2FFF0000 mask
	// itself: the value stops reading as "centered" and SDL places the window at a
	// garbage absolute position off every monitor. This function is EXPORTED and
	// called from cmd/asyncao, so its input is not confined to SDL's own bounded
	// GetDisplayIndex.
	for _, di := range []int{sdlWindowPosDisplayMax + 1, 0x20000, 1 << 30} {
		got := CenteredWindowPos(di)
		if uint32(got)&0xFFFF0000 != uint32(sdl.WINDOWPOS_CENTERED_MASK) {
			t.Errorf("CenteredWindowPos(%#x) = %#x, corrupted the CENTERED mask", di, got)
		}
		if got != int32(sdl.WINDOWPOS_CENTERED) {
			t.Errorf("CenteredWindowPos(%#x) = %#x, want the primary-display fallback %#x", di, got, int32(sdl.WINDOWPOS_CENTERED))
		}
	}
	// The last encodable index must still encode: the clamp is at the field's
	// edge, not one short of it.
	if got := CenteredWindowPos(sdlWindowPosDisplayMax); uint32(got)&0xFFFF != uint32(sdlWindowPosDisplayMax) {
		t.Errorf("CenteredWindowPos(%#x) = %#x, dropped the largest encodable index", sdlWindowPosDisplayMax, got)
	}
}

// TestThemeDesignWindowSizeReadsTheCourtroomRect pins the target of the resize.
// `courtroom` is the theme's canvas: stock AO2 feeds it to set_courtroom_size()
// and the window simply IS the canvas. It is also the only rect present on 100%
// of the reference corpus, unlike char_select.
func TestThemeDesignWindowSizeReadsTheCourtroomRect(t *testing.T) {
	a := testTabApp(t)
	if _, _, ok := a.themeDesignWindowSize(); ok {
		t.Error("no theme loaded must report no design size (the Settings entry hides)")
	}
	stageThemeCanvas(a, 714, 579)
	w, h, ok := a.themeDesignWindowSize()
	if !ok || w != 714 || h != 579 {
		t.Errorf("design size = (%d,%d,%v), want the stock 714x579", w, h, ok)
	}
	// A theme declaring a degenerate canvas must not be offered: resizing to 0
	// would only be clamped back up to the minimum window and confuse the user.
	a.themeRectsOrig = map[string]theme.Rect{"courtroom": {X: 0, Y: 0, W: 0, H: 579}}
	if _, _, ok := a.themeDesignWindowSize(); ok {
		t.Error("a zero-width courtroom rect must report no design size")
	}
}

// TestThemeDesignResizeDeliversOneToOne is the product-level assertion behind
// the whole feature: after the snap the layout engine draws the theme at scale
// exactly 1.0 with no leftover bars — pixel-for-pixel stock AO2, no settings touched.
//
// CHANGED BY #14. The snap target is now the canvas PLUS themeDesignWindowInset (the
// menu bar + docked server-tab strip band that sits above it), and the canvas is
// centred in the room under that band. The old expectations — persisted window ==
// 714x579 and offY == 0 — would now mean the courtroom got only 579-band px for a
// 579px canvas and had to scale DOWN, i.e. the affordance would no longer deliver
// the 1:1 it promises. The assertion below is the same promise re-stated against the
// band: scale exactly 1, no horizontal bars, and the vertical origin sitting exactly
// on the band with nothing left over.
func TestThemeDesignResizeDeliversOneToOne(t *testing.T) {
	a := testTabApp(t)
	// One session, strip undragged: the live band then equals themeDesignWindowInset,
	// which is the state the snap target is computed for (a real courtroom).
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	stageThemeCanvas(a, 714, 579)
	if !a.applyThemeDesignWindowSize() {
		t.Fatal("applyThemeDesignWindowSize reported no design size")
	}
	w, h := a.d.Prefs.WindowSize()
	if w != 714 || h != 579+int(themeDesignWindowInset) {
		t.Fatalf("persisted window = %dx%d, want the theme's 714x579 plus the %d px chrome band",
			w, h, themeDesignWindowInset)
	}
	if band := a.topChromeH(); band != themeDesignWindowInset {
		t.Fatalf("live chrome band = %d, want %d — the fixture is not in the state the snap targets", band, themeDesignWindowInset)
	}
	lay := a.themeWindowLayout(int32(w), int32(h))
	if !lay.valid {
		t.Fatal("themeWindowLayout did not validate")
	}
	if lay.scaleX != 1 || lay.scaleY != 1 {
		t.Errorf("scale = (%v,%v), want 1:1 after snapping to the design size", lay.scaleX, lay.scaleY)
	}
	if lay.offX != 0 || lay.offY != themeDesignWindowInset {
		t.Errorf("canvas origin = (%d,%d), want (0,%d) — flush under the chrome band, no bars",
			lay.offX, lay.offY, themeDesignWindowInset)
	}
	if want := (sdl.Rect{X: 0, Y: themeDesignWindowInset, W: 714, H: 579}); lay.area != want {
		t.Errorf("canvas = %+v, want %+v", lay.area, want)
	}
}

// TestThemeDesignResizeClampsToTheMinimumWindow pins that the tiny end of the
// corpus (themes whose canvas is smaller than config.MinWindowW/H) cannot drive
// the window below the floor. Those themes letterbox permanently, which is the
// correct reproduction: upscaling is the one thing stock AO2 never does.
func TestThemeDesignResizeClampsToTheMinimumWindow(t *testing.T) {
	a := testTabApp(t)
	stageThemeCanvas(a, 256, 256) // the corpus's `Viewport` theme
	if !a.applyThemeDesignWindowSize() {
		t.Fatal("applyThemeDesignWindowSize reported no design size")
	}
	if w, h := a.d.Prefs.WindowSize(); w != config.MinWindowW || h != config.MinWindowH {
		t.Errorf("persisted window = %dx%d, want the %dx%d floor", w, h, config.MinWindowW, config.MinWindowH)
	}
}

// TestThemeResizeOneShotFiresOnceThenClears is the core contract: arm the
// generation of one apply, land it, and the window moves; every later landing
// leaves it alone. Without the clear, healTheme (which re-enters the same
// pipeline on a texture eviction, mid-scene) would yank the window whenever it
// fired.
func TestThemeResizeOneShotFiresOnceThenClears(t *testing.T) {
	a := testTabApp(t)
	gen := startThemeApply(a)
	a.themeResizeArmGen = gen
	landThemeApplyGen(a, gen, designCanvas(714, 579))
	if a.themeResizeArmGen != 0 {
		t.Error("the one-shot survived its landing — a later heal would resize again")
	}
	// The target is the canvas PLUS the chrome band above it (themeDesignWindowInset,
	// #14) — that is what leaves the canvas itself at 1:1.
	wantH := 579 + int(themeDesignWindowInset)
	if w, h := a.d.Prefs.WindowSize(); w != 714 || h != wantH {
		t.Fatalf("armed landing gave window %dx%d, want 714x%d", w, h, wantH)
	}
	// A second theme lands DISARMED — a server binding a theme, a heal, a
	// filter-purge recovery. The window must not follow it.
	landThemeApply(a, designCanvas(1280, 720))
	if w, h := a.d.Prefs.WindowSize(); w != 714 || h != wantH {
		t.Errorf("an unarmed landing resized to %dx%d — boot/heal/server binds must never move the window", w, h)
	}
}

// TestThemeResizeArmingIsNotSpentByAnOlderApply is the race the generation
// exists for, and the reason the arming cannot be a bare bool.
//
// themePage heals the theme from ANY theme-art draw — including the Settings
// screen's own live preview — so a heal apply carrying the PREVIOUS theme can be
// in flight at the exact moment the user opens the theme dropdown. With a bare
// flag whichever apply landed first spent it: the stale one won, the window
// snapped to the old theme's canvas, and the theme the user actually picked
// landed already-consumed and never snapped at all. The same window let an
// ensureThemeForSession server bind spend it, which is the one thing the feature
// promises never to do.
func TestThemeResizeArmingIsNotSpentByAnOlderApply(t *testing.T) {
	a := testTabApp(t)
	// A heal (or a server bind) starts first, carrying the OLD theme's canvas.
	stale := startThemeApply(a)
	// The user then picks a theme: a newer apply, and the arming names IT.
	picked := startThemeApply(a)
	a.themeResizeArmGen = picked

	// The stale apply wins the race and lands first.
	landThemeApplyGen(a, stale, designCanvas(1280, 720))
	if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
		t.Errorf("a stale apply resized to %dx%d — that is the PREVIOUS theme's canvas", w, h)
	}
	if a.themeResizeArmGen != picked {
		t.Fatalf("a stale apply consumed the arming (%d, want %d) — the user's pick would never snap", a.themeResizeArmGen, picked)
	}

	// The armed apply lands second and still snaps, to ITS canvas (plus the chrome
	// band above it — themeDesignWindowInset, #14).
	landThemeApplyGen(a, picked, designCanvas(714, 579))
	wantH := 579 + int(themeDesignWindowInset)
	if w, h := a.d.Prefs.WindowSize(); w != 714 || h != wantH {
		t.Errorf("the armed apply gave window %dx%d, want the picked theme's 714x%d", w, h, wantH)
	}
	if a.themeResizeArmGen != 0 {
		t.Error("the arming survived its own landing")
	}
}

// TestThemeResizeArmingIgnoresANewerApply covers the other side of the race: an
// apply started AFTER the arming (a heal fired a moment later, or a server
// binding its own theme) must not borrow it. Firing there would resize to
// whatever that later apply carries, which for a server bind is precisely the
// "never when a server binds its own theme" guarantee.
func TestThemeResizeArmingIgnoresANewerApply(t *testing.T) {
	a := testTabApp(t)
	picked := startThemeApply(a)
	a.themeResizeArmGen = picked
	newer := startThemeApply(a)

	landThemeApplyGen(a, newer, designCanvas(1280, 720))
	if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
		t.Errorf("a newer (unarmed) apply resized to %dx%d", w, h)
	}
	// It is now unreachable — pollThemeApply's stale guard drops `picked` — but it
	// must stay inert rather than latch onto some future landing. Generations only
	// increase, so a stranded arming can never match again; prove that by landing
	// another apply and checking the window still has not moved.
	landThemeApply(a, designCanvas(800, 600))
	if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
		t.Errorf("a stranded arming leaked onto a later landing (%dx%d)", w, h)
	}
}

// TestThemeResizeNeverFiresUnarmed covers the boot path explicitly: a fresh App
// has never had a user pick, so the very first apply (app startup re-applying
// the SAVED theme) must leave the window exactly where the user left it. This is
// the case a "did the theme name change?" heuristic gets wrong, because the
// applied name starts empty and therefore always looks like a change.
func TestThemeResizeNeverFiresUnarmed(t *testing.T) {
	a := testTabApp(t)
	landThemeApply(a, designCanvas(714, 579))
	if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
		t.Errorf("boot apply persisted window %dx%d, want it untouched (0x0 = never set)", w, h)
	}
	if a.themeAppliedName != "fixture" {
		t.Fatalf("the landing did not actually run (themeAppliedName = %q)", a.themeAppliedName)
	}
}

// TestThemeResizeGuardsStillConsumeTheOneShot pins that every guard rail eats
// the flag. A pick that could not resize must NOT leave the trigger armed to go
// off later on an unrelated re-apply — that would be a window jump mid-scene,
// which is worse than the bug being fixed.
func TestThemeResizeGuardsStillConsumeTheOneShot(t *testing.T) {
	armAndLand := func(a *App, layout map[string]theme.Rect) {
		gen := startThemeApply(a)
		a.themeResizeArmGen = gen
		landThemeApplyGen(a, gen, layout)
	}
	t.Run("fullscreen", func(t *testing.T) {
		a := testTabApp(t)
		a.d.Prefs.SetWindowFullscreen(true)
		armAndLand(a, designCanvas(714, 579))
		if a.themeResizeArmGen != 0 {
			t.Error("the one-shot survived the fullscreen guard")
		}
		if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
			t.Errorf("resized to %dx%d while fullscreen — never fight the user", w, h)
		}
	})
	t.Run("themed layout off", func(t *testing.T) {
		// With the theme's courtroom geometry switched off the classic layout is on
		// screen, so the theme's canvas is not what is being drawn and snapping the
		// window to it would just make the classic layout cramped.
		a := testTabApp(t)
		a.d.Prefs.SetThemeLayout(false)
		armAndLand(a, designCanvas(714, 579))
		if a.themeResizeArmGen != 0 {
			t.Error("the one-shot survived the themed-layout guard")
		}
		if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
			t.Errorf("resized to %dx%d with the theme layout off", w, h)
		}
	})
	t.Run("theme declares no canvas", func(t *testing.T) {
		a := testTabApp(t)
		armAndLand(a, map[string]theme.Rect{"viewport": {X: 0, Y: 0, W: 256, H: 192}})
		if a.themeResizeArmGen != 0 {
			t.Error("the one-shot survived a theme with no courtroom rect")
		}
		if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
			t.Errorf("resized to %dx%d for a theme that declares no canvas", w, h)
		}
	})
}

// TestThemeScanFromUserGatesTheAutoResize pins the trap in the import landing:
// pollThemeScan re-applies the theme for ANY scan whose root resolved, and the
// Settings screen fires one automatically the first time it opens. Only a scan
// the user asked for may arm the resize, or merely opening Settings would move
// the window of everyone who has a custom theme folder saved.
func TestThemeScanFromUserGatesTheAutoResize(t *testing.T) {
	a := testTabApp(t)
	t.Cleanup(resetThemeScanState)

	settings.themeRes <- themeScan{names: []string{"fixture"}, root: `C:\themes-root`, fromUser: false}
	a.pollThemeScan()
	if a.themeResizeArmGen != 0 {
		t.Error("the Settings screen's automatic first-open scan armed the resize")
	}

	settings.themeRes <- themeScan{names: []string{"fixture"}, root: `C:\themes-root`, fromUser: true}
	a.pollThemeScan()
	armed := a.themeResizeArmGen
	if armed == 0 {
		t.Fatal("a user-driven folder pick/drop did NOT arm the resize — real imports would be missed")
	}

	// Arming must never be undone by an automatic scan that lands afterwards.
	settings.themeRes <- themeScan{names: []string{"fixture"}, root: `C:\themes-root`, fromUser: false}
	a.pollThemeScan()
	if a.themeResizeArmGen != armed {
		t.Errorf("an automatic scan landing moved the arming (%d → %d) — the user's pick would be lost", armed, a.themeResizeArmGen)
	}
}

// resetThemeScanState returns the package-level Settings screen state to zero.
// It is package-level (not per-test) state, so every test that pokes it must
// clean up or it leaks into the next one.
func resetThemeScanState() {
	settings.themeList, settings.themeDir, settings.themeName = nil, "", ""
	settings.themeBusy, settings.themeScanUserWant = false, false
	select {
	case <-settings.themeRes:
	default:
	}
}

// TestThemeScanIntentSurvivesABusyScan pins MINOR E: scanThemes single-flights on
// themeBusy, and it used to discard the whole request — the fromUser bit
// included. Clicking "Apply & rescan" (or landing a folder pick) while the
// Settings screen's automatic first-open scan was still running therefore lost
// the auto-snap for that action, silently and with no way to retry except doing
// it again. The intent is latched instead, so the in-flight scan's landing
// honours it.
func TestThemeScanIntentSurvivesABusyScan(t *testing.T) {
	a := testTabApp(t)
	t.Cleanup(resetThemeScanState)

	// An automatic scan is already running (as it is for the first frames after
	// Settings opens), so the user's request is single-flighted away.
	settings.themeBusy = true
	a.scanThemes(true)
	if !settings.themeScanUserWant {
		t.Fatal("a user-initiated scan dropped by the single-flight left no trace of the intent")
	}

	// The automatic scan lands, carrying fromUser=false. The latch must still make
	// it count as the user's import.
	settings.themeBusy = false
	settings.themeRes <- themeScan{names: []string{"fixture"}, root: `C:\themes-root`, fromUser: false}
	a.pollThemeScan()
	if a.themeResizeArmGen == 0 {
		t.Error("the latched user intent was not honoured — the auto-snap is lost for that click")
	}
	if settings.themeScanUserWant {
		t.Error("the latch was not cleared: the NEXT automatic scan would arm a resize too")
	}

	// And it really is one-shot: a following automatic scan must not re-arm.
	a.themeResizeArmGen = 0
	settings.themeRes <- themeScan{names: []string{"fixture"}, root: `C:\themes-root`, fromUser: false}
	a.pollThemeScan()
	if a.themeResizeArmGen != 0 {
		t.Error("a later automatic scan armed the resize — the latch is sticky")
	}
}

// themeScanWait bounds the wait for a re-issued scan's goroutine to land. Scans
// stat a couple of directories, so this is orders of magnitude more than needed;
// it exists only so a broken re-issue fails the test instead of hanging it.
const themeScanWait = 10 * time.Second

// awaitThemeScan receives the next scan result, failing rather than blocking
// forever. It DRAINS the channel, so a test that starts a real scan goroutine
// cannot leave a stray result behind for the next test's pollThemeScan.
func awaitThemeScan(t *testing.T) themeScan {
	t.Helper()
	select {
	case res := <-settings.themeRes:
		return res
	case <-time.After(themeScanWait):
		t.Fatal("no scan result landed — the dropped request was never re-issued")
		return themeScan{}
	}
}

// TestThemeScanReIssuesForAStaleFolder is the other half of the single-flight
// trap. Latching the intent keeps a dropped request alive, but the result that
// eventually lands answers the folder the in-flight scan was ISSUED for — and the
// request that got dropped is usually a folder pick, i.e. exactly the case where
// that folder has just changed. Re-labelling that stale result would write the
// OLD root back over the folder the user picked and arm the window snap for a
// theme root they never chose, while the folder they did choose was never scanned
// at all. The request has to be re-issued instead.
func TestThemeScanReIssuesForAStaleFolder(t *testing.T) {
	a := testTabApp(t)
	t.Cleanup(resetThemeScanState)

	const staleRoot = `C:\old-theme-root`
	pickedRoot := t.TempDir() // a real directory: the re-issued scan really stats it

	// A scan issued for staleRoot is in flight when the user picks a new folder, so
	// the pick is single-flighted away and only the latch records it.
	settings.themeBusy = true
	settings.themeDir = pickedRoot
	a.scanThemes(true)

	// The stale scan lands. It must change nothing.
	settings.themeBusy = false
	settings.themeRes <- themeScan{names: []string{"stale"}, root: staleRoot, in: staleRoot}
	a.pollThemeScan()
	if settings.themeDir != pickedRoot {
		t.Errorf("theme folder = %q, want the folder the user picked (%q) — a stale scan overwrote it", settings.themeDir, pickedRoot)
	}
	if a.themeResizeArmGen != 0 {
		t.Error("a stale scan armed the window snap — it would snap to a theme root the user never picked")
	}
	if !settings.themeScanUserWant {
		t.Fatal("the intent was spent on the stale result")
	}
	if !settings.themeBusy {
		t.Fatal("the dropped request was not re-issued — the picked folder is never scanned")
	}

	// The re-issue really carries the picked folder, and ITS landing is the one
	// that acts.
	res := awaitThemeScan(t)
	if res.in != pickedRoot {
		t.Fatalf("the re-issued scan was made for %q, want the picked folder %q", res.in, pickedRoot)
	}
	settings.themeRes <- res
	a.pollThemeScan()
	if a.themeResizeArmGen == 0 {
		t.Error("the re-issued scan's landing did not arm the snap — the user's import produced nothing")
	}
	if settings.themeScanUserWant {
		t.Error("the latch survived the landing that consumed it")
	}
}

// TestThemeScanIntentSurvivesALandingThatResolvedNothing pins that the latch is
// cleared only where it is CONSUMED. A scan that resolves neither a root nor a
// pick takes no action at all — and that is the DEFAULT state, since anyone using
// the bundled themes has an empty theme-folder field — so clearing the intent
// there would swallow an "Apply & rescan" click and arm nothing.
func TestThemeScanIntentSurvivesALandingThatResolvedNothing(t *testing.T) {
	a := testTabApp(t)
	t.Cleanup(resetThemeScanState)

	settings.themeBusy = true
	a.scanThemes(true) // single-flighted away; only the latch records it
	settings.themeBusy = false

	// It lands having resolved nothing: no custom root, no auto-pick.
	settings.themeRes <- themeScan{names: []string{"default"}}
	a.pollThemeScan()
	if a.themeResizeArmGen != 0 {
		t.Fatal("a landing that resolved no theme root armed a snap")
	}
	if !settings.themeScanUserWant {
		t.Fatal("the intent was cleared by a landing that took no action — the click is lost with no trace")
	}

	// The next landing that DOES resolve one honours it.
	settings.themeRes <- themeScan{names: []string{"fixture"}, root: `C:\themes-root`}
	a.pollThemeScan()
	if a.themeResizeArmGen == 0 {
		t.Error("the surviving intent was not honoured by the landing that acted")
	}
	if settings.themeScanUserWant {
		t.Error("the latch survived the landing that consumed it — the next automatic scan would snap too")
	}
}

// TestThemeResizeArmingDisarmsWhenOutraced pins the disarm half of the
// generation check. themeRes is a single slot and both the publish loop and
// pollThemeApply drop a result that a higher generation has overtaken, so once a
// NEWER apply lands the armed one can never arrive. Leaving the arming set
// stranded it for the life of the process. It still must not RESIZE there: a
// newer apply can be a server binding its own theme.
func TestThemeResizeArmingDisarmsWhenOutraced(t *testing.T) {
	a := testTabApp(t)
	picked := startThemeApply(a)
	a.themeResizeArmGen = picked
	newer := startThemeApply(a) // a heal, a purge recovery, or a server bind

	landThemeApplyGen(a, newer, designCanvas(1280, 720))
	if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
		t.Errorf("a newer (unarmed) apply resized to %dx%d", w, h)
	}
	if a.themeResizeArmGen != 0 {
		t.Errorf("the arming survived being outraced (%d) — it can never match again, so it is permanent litter", a.themeResizeArmGen)
	}

	// The outraced apply publishing late changes nothing: pollThemeApply's
	// consume-side guard drops it before it can reach the resize at all.
	landThemeApplyGen(a, picked, designCanvas(714, 579))
	if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
		t.Errorf("the outraced apply still resized to %dx%d", w, h)
	}
}

// TestThemeDesignSizeNeedsAViewportToo pins that the offer and the snap read the
// SAME precondition themeLayout() does. themeLayout requires courtroom AND
// viewport; a theme with only a courtroom rect therefore draws through the
// CLASSIC layout, so its canvas is not on screen — offering "the window becomes
// the canvas" or snapping to it would be a lie about what is being drawn.
func TestThemeDesignSizeNeedsAViewportToo(t *testing.T) {
	a := testTabApp(t)
	courtOnly := map[string]theme.Rect{"courtroom": {X: 0, Y: 0, W: 714, H: 579}}
	a.themeRectsOrig, a.themeRects = courtOnly, courtOnly
	a.themeLay.valid = false
	if lay := a.themeLayout(714, 579); lay.valid {
		t.Fatal("fixture is wrong: themeLayout validated a theme with no viewport")
	}
	if _, _, ok := a.themeDesignWindowSize(); ok {
		t.Error("reported a design size for a canvas the layout engine refuses to draw")
	}
	if _, _, ok := a.themeDesignSizeOffer(); ok {
		t.Error("offered the design-size row while the classic layout is what is drawn")
	}
	// The automatic half must refuse for the same reason, and still spend the
	// one-shot rather than leave it armed for some later apply.
	gen := startThemeApply(a)
	a.themeResizeArmGen = gen
	landThemeApplyGen(a, gen, courtOnly)
	if a.themeResizeArmGen != 0 {
		t.Error("the one-shot survived a theme with no viewport")
	}
	if w, h := a.d.Prefs.WindowSize(); w != 0 || h != 0 {
		t.Errorf("resized to %dx%d for a theme the classic layout draws", w, h)
	}
}

// TestThemeFitUpgradeSnapIsArmedOnceAtBoot pins MAJOR 2: the #29 default move
// (Stretch→Native) changes what is on screen, so the geometry move has to ride
// the SAME one-shot. Without it an existing user's first launch after updating
// swaps an edge-to-edge courtroom for a small centred canvas ringed by bars, and
// the only way out is a settings hunt — the outcome the product rule forbids.
//
// The trigger is the STAMP being newly written, never "is this boot", so it can
// fire exactly once ever.
func TestThemeFitUpgradeSnapIsArmedOnceAtBoot(t *testing.T) {
	// A file that predates the move: no stamp, still on the old Stretch default.
	dir := t.TempDir()
	path := filepath.Join(dir, config.PrefsFileName)
	if err := os.WriteFile(path, []byte(`{"themeFit": 0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	upgraded, err := config.New(path)
	if err != nil {
		t.Fatalf("load upgraded prefs: %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	if !upgraded.ThemeFitDefaultJustMoved() {
		t.Fatal("the pre-stamp file did not report the one-shot move — nothing would arm")
	}

	a := testTabApp(t)
	a.d.Prefs = upgraded
	gen := startThemeApply(a)
	a.armBootThemeResize(gen)
	if a.themeResizeArmGen != gen {
		t.Fatalf("boot arming = %d, want the boot apply's gen %d", a.themeResizeArmGen, gen)
	}
	landThemeApplyGen(a, gen, designCanvas(714, 579))
	// Canvas + the chrome band above it (themeDesignWindowInset, #14).
	wantH := 579 + int(themeDesignWindowInset)
	if w, h := a.d.Prefs.WindowSize(); w != 714 || h != wantH {
		t.Errorf("the upgrade snap gave window %dx%d, want the theme's 714x%d", w, h, wantH)
	}

	// SECOND LAUNCH: the stamp is on disk now, so the signal is gone forever and
	// the boot apply must leave the window exactly where the user put it.
	if err := upgraded.SaveNow(); err != nil {
		t.Fatalf("flush the stamp: %v", err)
	}
	relaunched, err := config.New(path)
	if err != nil {
		t.Fatalf("reload prefs: %v", err)
	}
	t.Cleanup(func() { _ = relaunched.Close() })
	if relaunched.ThemeFitDefaultJustMoved() {
		t.Fatal("the move re-reported on the next launch — the window would snap every boot")
	}
	b := testTabApp(t)
	b.d.Prefs = relaunched
	if w, h := b.d.Prefs.WindowSize(); w != 714 || h != wantH {
		t.Fatalf("the snapped window did not persist: reloaded %dx%d", w, h)
	}
	b.armBootThemeResize(startThemeApply(b))
	if b.themeResizeArmGen != 0 {
		t.Fatal("a second launch armed the upgrade snap again")
	}
	// The user has since switched to a theme with a different canvas. Nothing may
	// follow it: the upgrade snap is spent, and this is an ordinary boot apply.
	landThemeApply(b, designCanvas(1280, 720))
	if w, h := b.d.Prefs.WindowSize(); w != 714 || h != wantH {
		t.Errorf("a second launch resized to %dx%d — it must fire once and only once", w, h)
	}
}

// TestThemeFitUpgradeSnapSkipsPreservedPicks is the MINOR A consequence, stated
// as a test: a file that carried a deliberate Letterbox/Crop/Custom mode keeps
// it, so nothing about that user's look changed and nothing may move their
// window either. The stamp is still written, so neither half runs twice.
func TestThemeFitUpgradeSnapSkipsPreservedPicks(t *testing.T) {
	path := filepath.Join(t.TempDir(), config.PrefsFileName)
	if err := os.WriteFile(path, []byte(`{"themeFit": 3, "themeFitZoom": 150}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prefs, err := config.New(path)
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	if got := prefs.ThemeFitMode(); got != config.ThemeFitCustom {
		t.Fatalf("ThemeFitMode = %d, want the preserved Custom(%d)", got, config.ThemeFitCustom)
	}
	if prefs.ThemeFitDefaultJustMoved() {
		t.Fatal("a preserved pick reported the move — their window would snap for no reason")
	}

	a := testTabApp(t)
	a.d.Prefs = prefs
	a.armBootThemeResize(startThemeApply(a))
	if a.themeResizeArmGen != 0 {
		t.Error("a preserved pick armed the upgrade snap")
	}
}

// TestThemeDesignSizeRowFollowsTheThemedLayout pins MINOR D: the manual "Theme's
// design size" entry is offered only when the themed courtroom layout is the one
// being drawn, exactly like the automatic snap. With it off the theme's canvas is
// not on screen at all, so the button would merely cramp the classic layout while
// its hint promised the window would become the canvas.
func TestThemeDesignSizeRowFollowsTheThemedLayout(t *testing.T) {
	a := testTabApp(t)
	// No theme at all: nothing to offer either way.
	if _, _, ok := a.themeDesignSizeOffer(); ok {
		t.Error("offered a design size with no theme loaded")
	}
	stageThemeCanvas(a, 714, 579)
	if !a.d.Prefs.ThemeLayoutEnabled() {
		t.Fatal("the themed layout is the shipped default; this test needs it on to start")
	}
	w, h, ok := a.themeDesignSizeOffer()
	if !ok || w != 714 || h != 579 {
		t.Fatalf("offer = (%d,%d,%v), want the theme's 714x579 while the themed layout is on", w, h, ok)
	}

	a.d.Prefs.SetThemeLayout(false)
	if _, _, ok := a.themeDesignSizeOffer(); ok {
		t.Error("the row is still offered with the themed courtroom layout off — its hint would be false")
	}
	// The automatic half already refused there; both must read the same pref.
	gen := startThemeApply(a)
	a.themeResizeArmGen = gen
	landThemeApplyGen(a, gen, designCanvas(714, 579))
	if pw, ph := a.d.Prefs.WindowSize(); pw != 0 || ph != 0 {
		t.Errorf("the automatic half resized to %dx%d with the themed layout off", pw, ph)
	}
}
