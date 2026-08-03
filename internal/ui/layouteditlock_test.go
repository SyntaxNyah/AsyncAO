package ui

// The layout editors' UNRECOVERABLE states, and the one frame-level decision that
// keeps the server-tab strip painted exactly once.
//
// Everything here drives the REAL courtroom pass. The lock these pin was not visible
// in any arithmetic: every predicate involved was individually correct, and the app
// died from the COMBINATION — an editor armed, a modal holding the pass, and four
// separate surfaces each correctly standing down for a fifth that never drew.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// courtroomModalOpeners is one "open it" function per row of courtroomModals, in the
// same order. It exists so the exhaustiveness test below FAILS TO COMPILE ITS
// EXPECTATION — the length check — the moment a row is added to the table without
// anyone thinking about the editors that have to close it.
var courtroomModalOpeners = []struct {
	name string
	open func(*App)
}{
	{"iniswap", func(a *App) { a.showIni = true }},
	{"background picker", func(a *App) { a.bgPick.show = true }},
	// The timer, the login dialog and the pair popup left this table in #31 — they are
	// non-blocking floatWin panels now, so they cannot end the pass and cannot strand
	// an editor. What remains is the set that genuinely takes the screen.
	{"SFX browser", func(a *App) { a.showSfxBrowser = true }},
}

// TestEveryCourtroomModalCloseUndoesItsOpen pins the modal table's internal
// consistency: each row's close really dismisses the flag its open reads. The table
// is what the layout editors now clear (closeCourtroomModals), so a row whose close
// does not match its open would put the editors right back where they were — closing
// a hand-picked subset.
func TestEveryCourtroomModalCloseUndoesItsOpen(t *testing.T) {
	if len(courtroomModalOpeners) != len(courtroomModals) {
		t.Fatalf("courtroomModals has %d rows but this test knows how to open %d — a new return-to-top modal was added without deciding what the layout editors do with it (it can strand them: see modalReturnSkippedWhileEditing)",
			len(courtroomModals), len(courtroomModalOpeners))
	}
	for i, o := range courtroomModalOpeners {
		a := testTabApp(t)
		o.open(a)
		if !courtroomModals[i].open(a) {
			t.Fatalf("row %d (%s): the test's opener does not set the flag row %d reads", i, o.name, i)
		}
		a.closeCourtroomModals()
		if courtroomModals[i].open(a) {
			t.Errorf("row %d (%s): closeCourtroomModals left it open", i, o.name)
		}
	}
}

// TestArmingALayoutEditorClosesEveryCourtroomModal is the second of the three
// belt-and-braces guarantees against the hard lock: with every return-to-top modal
// open at once, arming either editor must leave none of them up.
//
// The arm paths used to close a HAND-WRITTEN list of nine popups that had drifted
// three entries behind this table — showTimer, pairPopupOpen and showSfxBrowser were
// all missing — and any one of those three open when the editor armed took the
// courtroom pass before the editor's overlay drew.
func TestArmingALayoutEditorClosesEveryCourtroomModal(t *testing.T) {
	for _, tc := range []struct {
		name string
		arm  func(*App)
	}{
		{"themed editor", (*App).startLayoutEdit},
		{"classic editor", (*App).startClassicEdit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testTabApp(t)
			for _, o := range courtroomModalOpeners {
				o.open(a)
			}
			if !a.courtroomModalUp() {
				t.Fatal("fixture: nothing is open, so nothing is being tested")
			}
			tc.arm(a)
			if a.courtroomModalUp() {
				for i, o := range courtroomModalOpeners {
					if courtroomModals[i].open(a) {
						t.Errorf("%s survived the editor arming — it ends the courtroom pass before the editor's overlay draws", o.name)
					}
				}
			}
		})
	}
}

// stageThemedEditorFixture is a themed courtroom with a real Ctx, a couple of server
// chips and the THEMED editor armed by hand — deliberately NOT through
// startLayoutEdit, because the states below are exactly the ones that path now
// prevents, and they must stay recoverable if anything ever reaches them again.
func stageThemedEditorFixture(t *testing.T) (*App, int32, int32, func()) {
	t.Helper()
	a, cleanup := stageThemedCourtroom(t)
	w, h, err := a.ctx.Ren.GetOutputSize()
	if err != nil {
		cleanup()
		t.Skipf("output size unavailable: %v", err)
	}
	a.d.Prefs.SetTheme(stripEditTheme, "") // drawLayoutEditor disarms itself with no theme name
	a.serverName = "first session"
	a.tabs = []*courtTab{{}, {state: sessionState{serverName: "second session"}}}
	a.activeTab = 0
	a.layoutEdit, a.layoutSnap = true, false
	return a, w, h, cleanup
}

// layoutEditorBannerText mirrors drawLayoutEditor's own banner string. Rasterized
// nowhere else in the client, so finding it in a freshly purged text cache is proof
// the editor's overlay drew this frame.
const layoutEditorBannerText = "LAYOUT EDIT — drag = move, arrows = nudge 1 px (Ctrl = grid), corner grip = resize, Tab = cycle, R = rotate, right-click = reset, Ctrl+Z/Y = undo, Esc = exit"

// TestThemedEditorSurvivesAnOpenCourtroomModal is the BLOCKING regression: the themed
// courtroom could hard-lock.
//
// Three return-to-top modals — the timer, the pair popup and the SFX browser — were in
// the courtroom modal table but NOT in the list the editors closed as they armed. With
// one of them up, drawCourtroomThemed returned at its modal check, which (unlike the
// classic twin) carried no editor guard. The consequences compounded:
//
//   - drawLayoutEditor never ran: no banner, no Done chip, and its own Esc handler
//     never fired;
//   - layoutEditFence had already set c.modalOn, so the modal's own kit buttons were
//     hovering()-dead;
//   - the server-tab strip stood down for an editor that was not drawing, and so did
//     the menu bar;
//   - and the app-level Esc guard had just been widened to swallow Esc whenever an
//     editor was armed, closing the last exit.
//
// Nothing on screen answered. This test arms the editor with each of the three open
// and asserts the editor draws, Done is present, and Esc exits — by both routes.
func TestThemedEditorSurvivesAnOpenCourtroomModal(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*App)
	}{
		{"iniswap", func(a *App) { a.showIni = true }},
		{"background picker", func(a *App) { a.bgPick.show = true }},
		{"SFX browser", func(a *App) { a.showSfxBrowser = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, w, h, cleanup := stageThemedEditorFixture(t)
			defer cleanup()
			tc.open(a)
			if !a.courtroomModalUp() {
				t.Fatal("fixture: the modal is not open, so nothing is being tested")
			}

			a.ctx.purgeTextCache()
			a.drawCourtroom(w, h)
			if !a.themeLay.valid {
				t.Fatal("fixture: the courtroom did not take the THEMED branch")
			}
			if !textCacheHasLabel(a.ctx, layoutEditorBannerText) {
				t.Error("the editor's banner never drew: the modal took the pass and the editor is stranded behind its own input fence")
			}
			if !textCacheHasLabel(a.ctx, "Done") {
				t.Error("the editor's Done chip never drew — its primary exit is invisible")
			}
			// The strip stands down for the editor, so the editor's own pass has to be
			// the thing that paints it. Both were false in the locked state.
			if !a.tabStripPaintsUnderEditor() {
				t.Error("the strip's in-pass site is closed while the editor owns the screen — App.Frame would paint it over the banner")
			}
			if !textCacheHasLabel(a.ctx, a.tabChipLabel(1)) {
				t.Error("the courtroom pass never painted the server-tab strip, and App.Frame's site has stood down for the editor: the strip is simply gone")
			}

			// Esc, route 1: closeTopOverlay. The app-level handler runs this BEFORE any
			// draw, so it is the route that has to answer even on a frame where the
			// editor's own draw does not run at all.
			if !a.closeTopOverlay() {
				t.Fatal("Esc had nowhere to go: closeTopOverlay does not answer for an armed editor, so the key falls through to the courtroom's leave-the-server confirm")
			}
			if a.layoutEdit {
				t.Error("closeTopOverlay did not exit the editor")
			}
			if a.ctx.modalOn {
				t.Error("the editor's input fence outlived it — an un-released modalOn freezes the whole UI")
			}

			// Esc, route 2: the editor's own draw-time handler, which the modal return
			// used to delete from the frame. Belt and braces — either one alone gets the
			// user out.
			a.layoutEdit = true
			tc.open(a)
			a.ctx.escPressed = true
			a.drawCourtroom(w, h)
			a.ctx.escPressed = false
			if a.layoutEdit {
				t.Error("the editor's own Esc handler never fired: its draw is still being skipped")
			}
		})
	}
}

// --- the strip's paint site is decided ONCE per frame --------------------------------

// exactlyOneStripPaint drives one whole frame the way App.Frame does — reset the
// latch, run the courtroom pass, then consult the over-everything gate — and reports
// whether the in-pass site painted and whether App.Frame's site still wants to.
func exactlyOneStripPaint(t *testing.T, a *App, w, h int32, beforePass, afterPass func(*App)) (inPass, overEverything bool) {
	t.Helper()
	a.tabStripPaint = tabStripPaintLatch{} // App.Frame's per-frame reset
	if beforePass != nil {
		beforePass(a)
	}
	a.ctx.purgeTextCache()
	a.drawCourtroom(w, h)
	if afterPass != nil {
		afterPass(a) // e.g. the command palette, which draws AFTER the in-pass site
	}
	return textCacheHasLabel(a.ctx, a.tabChipLabel(1)), a.tabStripPaintsOverEverything()
}

// TestTabStripPaintSiteIsLatchedOncePerFrame pins the frame-level decision.
//
// The strip has two draw sites — the courtroom pass (under a layout editor's overlay)
// and App.Frame's over-everything paint — and exactly one may run per frame. They used
// to answer INDEPENDENTLY, the in-pass site off the editor flag and App.Frame's off a
// predicate re-evaluated after the whole screen dispatch, so any mid-frame change made
// them disagree:
//
//   - DISMISS frame (Done, Esc, or a theme lost mid-pass): the in-pass site painted,
//     the editor then disarmed itself, and App.Frame's gate re-opened and painted the
//     same strip a SECOND time — over the banner that was still on screen.
//   - ARM frame from the command palette (which draws inside the courtroom case, AFTER
//     the in-pass site): the pass had already gone by, and App.Frame's re-test now said
//     the editor owned it. Nobody painted the strip at all.
//
// One latch, taken at the top of the pass and replayed by both sites, makes the two
// exact complements.
func TestTabStripPaintSiteIsLatchedOncePerFrame(t *testing.T) {
	for _, tc := range []struct {
		name       string
		beforePass func(*App)
		afterPass  func(*App)
		wantInPass bool
	}{
		{
			name:       "arm frame (menu bar: armed before the pass)",
			beforePass: func(a *App) { a.layoutEdit = true },
			wantInPass: true,
		},
		{
			name:       "arm frame (command palette: armed after the in-pass site)",
			afterPass:  func(a *App) { a.layoutEdit = true },
			wantInPass: false,
		},
		{
			name:       "steady frame",
			beforePass: func(a *App) { a.layoutEdit = true },
			wantInPass: true,
		},
		{
			name: "dismiss frame (Esc inside the pass)",
			beforePass: func(a *App) {
				a.layoutEdit = true
				a.ctx.escPressed = true
			},
			afterPass:  func(a *App) { a.ctx.escPressed = false },
			wantInPass: true,
		},
		{
			name: "dismiss frame (the theme went away mid-pass)",
			beforePass: func(a *App) {
				a.layoutEdit = true
				a.d.Prefs.SetThemeLayout(false) // drawCourtroom force-stops the themed editor
			},
			wantInPass: true,
		},
		{
			name:       "no editor armed",
			wantInPass: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, w, h, cleanup := stageThemedEditorFixture(t)
			defer cleanup()
			a.layoutEdit = false // each case arms (or does not) for itself

			inPass, overEverything := exactlyOneStripPaint(t, a, w, h, tc.beforePass, tc.afterPass)
			if inPass != tc.wantInPass {
				t.Errorf("the courtroom pass painted the strip = %v, want %v", inPass, tc.wantInPass)
			}
			if inPass == overEverything {
				t.Errorf("both sites agree (in-pass=%v, over-everything=%v): the strip is painted %s",
					inPass, overEverything, map[bool]string{true: "TWICE", false: "not at all"}[inPass])
			}
		})
	}
}

// TestTabStripPaintLatchIsClearOffTheCourtroom pins the reset half. Only a courtroom
// pass takes the decision; a frame with no pass at all (the lobby, char select, and the
// three full-window modes) must fall back to App.Frame's site, or a stale latch from
// the last courtroom frame would delete the strip from every screen after it.
func TestTabStripPaintLatchIsClearOffTheCourtroom(t *testing.T) {
	a, w, h, cleanup := stageThemedEditorFixture(t)
	defer cleanup()
	a.tabStripPaint = tabStripPaintLatch{}
	a.drawCourtroom(w, h)
	if !a.tabStripPaint.inPass {
		t.Fatal("fixture: the editor's pass did not claim the strip, so the stale-latch case is not being tested")
	}
	a.tabStripPaint = tabStripPaintLatch{} // App.Frame's reset, on a lobby frame
	if !a.tabStripPaintsOverEverything() {
		t.Error("a frame with no courtroom pass must fall back to App.Frame's site, or the strip vanishes off the courtroom")
	}
}

// --- the drag's grab point, at every scale -------------------------------------------

// TestTabStripDragIsExactAtEveryScale is MAJOR 1's gate. The strip's placement is
// stored in canvas-relative CLIENT px and its forward map (tabStripCacheRect) is one
// addition, so editBaseFor's subtraction is an EXACT inverse — and a one-pixel drag is
// a one-pixel move at every scale, not only where the two rounding rules happened to
// agree.
//
// While the map was scaled, the preimage rounded and the forward transform truncated,
// so the base was not a fixed point: measured at Letterbox 1000x900 (scale 1.4006) a
// +1,+1 cursor move took the box {460,22,79,22} → {459,23,79,22} — BACKWARDS on X —
// and +40,+40 landed at +38,+40, a constant −2 px error riding the whole gesture. Crop
// 900x700 (scale 1.2605) went {410,22} → {409,21}. Native and Stretch round-tripped
// only at scale 1 and 2, which is why the first fixture could not see any of it.
func TestTabStripDragIsExactAtEveryScale(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	for _, tc := range themeFitDragCases {
		for _, d := range []int32{1, 2, 3, 40} {
			t.Run(tc.name, func(t *testing.T) {
				a := stageTabbarlessCourtroom(t, stripEditTheme)
				a.ctx = ctx
				a.uiScalePct = 100
				a.layoutEdit, a.layoutSnap = true, false
				tc.apply(a)
				a.themeLay.valid = false

				lay := a.themeWindowLayout(tc.w, tc.h)
				before, ok := lay.rect(themeTabBarKey)
				if !ok {
					t.Fatal("the themed editor must be handed a box for the strip")
				}
				ctx.mouseX, ctx.mouseY = before.X+before.W/2, before.Y+before.H/2
				ctx.mouseDown, a.editPrev = true, false
				a.drawLayoutEditor(tc.w, tc.h, lay)
				if a.editKey != themeTabBarKey || a.editDrag != 1 {
					t.Fatalf("a press on the strip's box must grab it for a MOVE, got key=%q drag=%d", a.editKey, a.editDrag)
				}
				ctx.mouseX, ctx.mouseY = ctx.mouseX+d, ctx.mouseY+d
				a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))
				ctx.mouseDown = false
				a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))

				after, ok := a.themeWindowLayout(tc.w, tc.h).rect(themeTabBarKey)
				if !ok {
					t.Fatal("the strip lost its box during the drag")
				}
				want := sdl.Rect{X: before.X + d, Y: before.Y + d, W: before.W, H: before.H}
				if after != want {
					t.Fatalf("scale %.4f: a (%+d,%+d) drag moved the box %+v → %+v, want %+v",
						lay.scaleX, d, d, before, after, want)
				}
				// MAJOR 2: the box must be ON the widget after the release, and stay
				// there. A release that flips parked-ness used to leave the parked box
				// in the cache for good — idle editor frames never healed it.
				assertStripBoxIsOnTheStrip(t, a, tc.w, tc.h, "after release")
				for i := 0; i < 3; i++ {
					a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))
				}
				assertStripBoxIsOnTheStrip(t, a, tc.w, tc.h, "after three idle editor frames")
			})
		}
	}
}

// TestTabStripBoxSurvivesADiscardedDrag is MAJOR 2 in isolation: the gesture whose
// RELEASE flips parked-ness back off.
//
// Parked-ness has an in-flight arm — a drag that has grabbed the strip is a placement
// in progress — so the layout cache is rebuilt with the parked (canvas) placement the
// moment the press lands. A gesture that is then DISCARDED at release (a press with no
// movement, or a move that comes back to where it started) writes no override, so
// parked-ness goes back to false — and the cache kept the parked box. Measured at
// Letterbox 1000x900: the editor's box sat at a canvas position while the strip
// painted in the chrome band, and three further idle editor frames never healed it;
// only a window, fit, theme or chip-width change did.
//
// Both halves of the fix are exercised here: parked-ness is part of the cache key (so
// it is CORRECT) and the release invalidates (so it is IMMEDIATE).
func TestTabStripBoxSurvivesADiscardedDrag(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	for _, tc := range themeFitDragCases {
		for _, gesture := range []struct {
			name string
			path []int32 // cursor deltas from the grab point, in order; the last is the release position
		}{
			{"press and release without moving", []int32{0}},
			{"move away and come back", []int32{40, 0}},
		} {
			t.Run(tc.name+"/"+gesture.name, func(t *testing.T) {
				a := stageTabbarlessCourtroom(t, stripEditTheme)
				a.ctx = ctx
				a.uiScalePct = 100
				a.layoutEdit, a.layoutSnap = true, false
				tc.apply(a)
				a.themeLay.valid = false

				lay := a.themeWindowLayout(tc.w, tc.h)
				before, ok := lay.rect(themeTabBarKey)
				if !ok {
					t.Fatal("the themed editor must be handed a box for the strip")
				}
				grabX, grabY := before.X+before.W/2, before.Y+before.H/2
				ctx.mouseX, ctx.mouseY = grabX, grabY
				ctx.mouseDown, a.editPrev = true, false
				a.drawLayoutEditor(tc.w, tc.h, lay)
				for _, d := range gesture.path {
					ctx.mouseX, ctx.mouseY = grabX+d, grabY+d
					a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))
				}
				ctx.mouseDown = false
				a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))

				if a.tabStripThemeParked() {
					t.Fatal("a discarded gesture must leave the strip un-parked — nothing was persisted")
				}
				assertStripBoxIsOnTheStrip(t, a, tc.w, tc.h, "immediately after the discarded release")
				after, _ := a.themeWindowLayout(tc.w, tc.h).rect(themeTabBarKey)
				if after != before {
					t.Errorf("a discarded gesture moved the box %+v → %+v", before, after)
				}
				for i := 0; i < 3; i++ {
					a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))
				}
				assertStripBoxIsOnTheStrip(t, a, tc.w, tc.h, "after three idle editor frames")
			})
		}
	}
}

// TestTabStripParkedNessIsPartOfTheLayoutCacheKey pins the CORRECTNESS half of MAJOR
// 2 directly, without leaning on a gesture.
//
// Parked-ness selects between two completely different placements — the docked chrome
// band and a spot on the theme canvas — and its only source is a persisted per-key
// override, which nothing about the window, the band, the fit, the theme or the chip
// width reflects. It was NOT in the cache key, so every writer had to remember to
// invalidate by hand; the two reset paths and restoreLayout did, and the editor's drag
// release did not. In the key it cannot be forgotten again.
func TestTabStripParkedNessIsPartOfTheLayoutCacheKey(t *testing.T) {
	a := stageTabbarlessCourtroom(t, stripEditTheme)
	const w, h = int32(1000), int32(900)
	docked, ok := a.themeWindowLayout(w, h).rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip has no box while docked")
	}

	// Park it by writing ONLY what parked-ness is made of, and deliberately NOT
	// touching themeLay.valid — the whole point is that the cache notices by itself.
	placed := stripDesignRect(int(docked.X)-100, int(docked.Y)+220)
	a.themeRects[themeTabBarKey] = placed
	a.d.Prefs.SetThemeRectOverride(stripEditTheme, themeTabBarKey,
		[4]int{placed.X, placed.Y, placed.W, placed.H})
	parked, ok := a.themeWindowLayout(w, h).rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip lost its box when it was parked")
	}
	if parked == docked {
		t.Fatalf("the cache still hands out the DOCKED box %+v after the strip was parked — parked-ness is not in the key", docked)
	}
	assertStripBoxIsOnTheStrip(t, a, w, h, "parked, cache never invalidated by hand")

	// And back the other way: un-parking is the direction the drag release takes.
	a.d.Prefs.ClearThemeRectOverride(stripEditTheme, themeTabBarKey)
	back, ok := a.themeWindowLayout(w, h).rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip lost its box when it was un-parked")
	}
	if back != docked {
		t.Errorf("after un-parking the box is %+v, want the docked %+v — the cache kept the parked placement", back, docked)
	}
	assertStripBoxIsOnTheStrip(t, a, w, h, "un-parked, cache never invalidated by hand")
}

// TestThemeLayoutCacheHitIsAllocFreeWithASeededTabStrip guards the price of putting
// parked-ness in the key. The flag is resolved BEFORE the validity test — it has to
// be, it is part of the test — so it now runs on every themeLayoutIn call, i.e. every
// courtroom frame, on the cache-HIT path that the whole design exists to keep cheap.
//
// The existing themed whole-screen gate cannot cover this: its fixture's theme has no
// asyncao_tabbar entry at all, so tabStripThemeParked returns on its first map probe
// and never reaches the prefs read. This one seeds the key and parks the strip, which
// is the branch that actually asks the preference layer.
func TestThemeLayoutCacheHitIsAllocFreeWithASeededTabStrip(t *testing.T) {
	a := stageTabbarlessCourtroom(t, stripEditTheme)
	const w, h = int32(1000), int32(900)
	box, ok := a.themeWindowLayout(w, h).rect(themeTabBarKey)
	if !ok {
		t.Fatal("fixture: the strip has no box, so the parked branch is not reachable")
	}
	parkTabStripForTest(a, stripEditTheme, stripDesignRect(int(box.X)-40, int(box.Y)+120))
	a.themeWindowLayout(w, h) // build once; everything below must be a cache hit
	if !a.tabStripThemeParked() {
		t.Fatal("fixture: the strip is not parked, so the prefs probe is not being measured")
	}
	if n := testing.AllocsPerRun(200, func() { a.themeWindowLayout(w, h) }); n != 0 {
		t.Fatalf("a themed layout cache HIT allocates %.1f/op, want 0 — the parked-ness probe is on every courtroom frame (fix the alloc, don't loosen the gate)", n)
	}
}

// TestTabStripDragReleaseInvalidatesTheLayoutCache pins the IMMEDIACY half. The key
// makes the answer correct whenever it is next asked; invalidating at the release
// makes the heal happen on the very next frame instead of whenever some other part of
// the key happens to move. Every other mutation of the layout already did this — the
// release was the one that did not.
func TestTabStripDragReleaseInvalidatesTheLayoutCache(t *testing.T) {
	a, ctx, cleanup := stripEditFixture(t)
	defer cleanup()

	lay := a.themeWindowLayout(stripEditW, stripEditH)
	box, ok := lay.rect(themeTabBarKey)
	if !ok {
		t.Fatal("the themed editor must be handed a box for the strip")
	}
	ctx.mouseX, ctx.mouseY = box.X+box.W/2, box.Y+box.H/2
	ctx.mouseDown, a.editPrev = true, false
	a.drawLayoutEditor(stripEditW, stripEditH, lay)
	ctx.mouseX, ctx.mouseY = ctx.mouseX+12, ctx.mouseY+80
	a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))

	ctx.mouseDown = false
	a.themeWindowLayout(stripEditW, stripEditH) // the frame's rebuild, before the release is seen
	if !a.themeLay.valid {
		t.Fatal("fixture: the cache must be valid going into the release, or nothing is being tested")
	}
	a.drawLayoutEditor(stripEditW, stripEditH, &a.themeLay)
	if a.themeLay.valid {
		t.Error("the drag release left the layout cache valid: the strip's box heals only when something else in the key moves")
	}
}

// themeFitDragCases are the fit modes and window sizes the strip's drag is proven at.
// Native alone is not a proof: it pins the layout scale at 1, which is exactly where
// the broken map round-tripped.
var themeFitDragCases = []struct {
	name  string
	w, h  int32
	apply func(*App)
}{
	{"Native 714x760", 714, 760, func(a *App) { a.d.Prefs.SetThemeFit(config.ThemeFitNative) }},
	{"Native 1152x864", 1152, 864, func(a *App) { a.d.Prefs.SetThemeFit(config.ThemeFitNative) }},
	{"Letterbox 1000x900", 1000, 900, func(a *App) { a.d.Prefs.SetThemeFit(config.ThemeFitLetterbox) }},
	{"Crop 900x700", 900, 700, func(a *App) { a.d.Prefs.SetThemeFit(config.ThemeFitCrop) }},
	{"Stretch 1000x900", 1000, 900, func(a *App) { a.d.Prefs.SetThemeFit(config.ThemeFitStretch) }},
	{"Custom zoom+pan 1000x800", 1000, 800, func(a *App) {
		a.d.Prefs.SetThemeFit(config.ThemeFitCustom)
		a.d.Prefs.SetThemeFitZoom(120)
		a.d.Prefs.SetThemeFitPan(7, -5)
	}},
}

// stripDesignRect is a stored strip placement: canvas-relative CLIENT px in X/Y, with
// the two size slots at their inert values (applyRectOverrides — every consumer
// re-derives the strip's size from the chips).
func stripDesignRect(x, y int) theme.Rect {
	return theme.Rect{X: x, Y: y, W: tabBarDesignSeedW, H: int(tabBarH)}
}

// assertStripBoxIsOnTheStrip is the "box == widget" invariant, checked against the
// PAINTED chips rather than against any arithmetic.
func assertStripBoxIsOnTheStrip(t *testing.T, a *App, w, h int32, stage string) {
	t.Helper()
	box, ok := a.themeWindowLayout(w, h).rect(themeTabBarKey)
	if !ok {
		t.Fatalf("%s: the strip has no editor box", stage)
	}
	rects, _ := a.tabBarRects(w, h)
	if len(rects) == 0 {
		t.Fatalf("%s: fixture has no chips", stage)
	}
	painted := sdl.Rect{X: rects[0].X, Y: rects[0].Y, W: a.tabStripTotalW(), H: tabBarH}
	if box != painted {
		t.Errorf("%s: the editor's box is %+v but the strip paints %+v", stage, box, painted)
	}
}

// TestMagnetSeesTheLiveTabStripNotTheInertSeed pins the sibling-list fix.
//
// While the strip is unparked its stored entry is the SYNTHESIZED SEED — a
// tabBarDesignSeedW-wide box centred on the canvas that nothing paints and nothing
// hit-tests — so every OTHER widget's snapped drag was magnetting to a phantom while
// the strip sat in the chrome band. The magnet must read the same live rect the
// editor's drag box, stacking order, grip probe and reset all read.
func TestMagnetSeesTheLiveTabStripNotTheInertSeed(t *testing.T) {
	a := stageTabbarlessCourtroom(t, stripEditTheme)
	const w, h = int32(1000), int32(900)
	lay := a.themeWindowLayout(w, h)
	painted, ok := lay.rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip has no live rect")
	}
	seed := a.themeRects[themeTabBarKey]
	seedRect := sdl.Rect{X: int32(seed.X), Y: int32(seed.Y), W: int32(seed.W), H: int32(seed.H)}

	got := a.magnetSiblingRect(themeTabBarKey, seed, lay)
	if got == seedRect {
		t.Errorf("the magnet is still fed the inert seed %+v — the strip is painted at %+v", seedRect, painted)
	}
	// It is the live rect, expressed in the design units the sibling list uses.
	want := sdl.Rect{
		X: int32(float64(painted.X-lay.offX) / lay.scaleX),
		Y: int32(float64(painted.Y-lay.offY) / lay.scaleY),
		W: int32(float64(painted.W) / lay.scaleX),
		H: int32(float64(painted.H) / lay.scaleY),
	}
	if got != want {
		t.Errorf("the magnet sees %+v, want the painted strip in design units %+v", got, want)
	}
	// Every other key still reports its stored design rect, unchanged.
	vp := a.themeRects["viewport"]
	if got := a.magnetSiblingRect("viewport", vp, lay); got != (sdl.Rect{X: int32(vp.X), Y: int32(vp.Y), W: int32(vp.W), H: int32(vp.H)}) {
		t.Errorf("a theme's own widget must still magnet to its stored design rect, got %+v", got)
	}
}

// TestSnappedTabStripDragStaysOnTheStrip exercises the strip's SCREEN-space snap and
// magnet arm — the shipped default (snap on) at a scale other than 1, where the grid
// and the piece-to-piece magnet both run on painted pixels. The gesture may legally
// land on a grid line or flush to a sibling, so the assertion is the invariant that
// must hold whatever it snaps to: the box is on the widget, and a MOVE never resizes.
func TestSnappedTabStripDragStaysOnTheStrip(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	for _, tc := range themeFitDragCases {
		t.Run(tc.name, func(t *testing.T) {
			a := stageTabbarlessCourtroom(t, stripEditTheme)
			a.ctx = ctx
			a.uiScalePct = 100
			a.layoutEdit, a.layoutSnap = true, true
			tc.apply(a)
			a.themeLay.valid = false

			lay := a.themeWindowLayout(tc.w, tc.h)
			before, ok := lay.rect(themeTabBarKey)
			if !ok {
				t.Fatal("the themed editor must be handed a box for the strip")
			}
			ctx.mouseX, ctx.mouseY = before.X+before.W/2, before.Y+before.H/2
			ctx.mouseDown, a.editPrev = true, false
			a.drawLayoutEditor(tc.w, tc.h, lay)
			ctx.mouseX, ctx.mouseY = ctx.mouseX+37, ctx.mouseY+143
			a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))
			ctx.mouseDown = false
			a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))

			after, ok := a.themeWindowLayout(tc.w, tc.h).rect(themeTabBarKey)
			if !ok {
				t.Fatal("the strip lost its box during the snapped drag")
			}
			if after.W != before.W || after.H != before.H {
				t.Errorf("a snapped MOVE resized the strip %+v → %+v", before, after)
			}
			// Within one grid step of where an unsnapped drag would have put it — a
			// snap, not a teleport.
			for _, d := range []struct {
				axis     string
				got, exp int32
			}{{"X", after.X, before.X + 37}, {"Y", after.Y, before.Y + 143}} {
				if diff := d.got - d.exp; diff < -layoutGridDesign || diff > layoutGridDesign {
					t.Errorf("snapped drag moved %s to %d, want within one grid step (%d) of %d",
						d.axis, d.got, layoutGridDesign, d.exp)
				}
			}
			assertStripBoxIsOnTheStrip(t, a, tc.w, tc.h, "after a snapped drag")
		})
	}
}

// stripGridDragDY is the vertical travel TestSnappedTabStripLandsOnTheWindowGrid drags
// with. It has to clear the docked band by enough that the strip's top, bottom and
// centre are all out of magnet range of the canvas's top edge — which sits anywhere
// from Y=44 to Y=164 across the fit cases — so the GRID, not the magnet, is what
// decides Y. At 143 it does so in five of the six cases; the sixth (Native 1152x864,
// whose canvas top lands exactly on the grid line the drag reaches) is detected and
// skipped by the guide check rather than being tuned around.
const stripGridDragDY = int32(143)

// TestSnappedTabStripLandsOnTheWindowGrid pins the strip's grid PHASE.
//
// Its stored value is canvas-relative, so rounding it rounds against the canvas origin
// and the grid the user sees moves with the fit mode and the window size. Measured, one
// pixel of travel from the docked box at screen Y=22: Native 714x760 → 24, Native
// 1152x864 → 20, Letterbox 1000x900 → 19. One gesture, one widget, three answers, none
// of them on a line the window has anywhere. Every other step of this key's gesture —
// base, delta, magnet, clamp — is already in window space, so the grid is now too
// (snapTabStripScreen), and the landing is a window-grid line in every mode.
//
// A magnet flush BEATS the grid, deliberately and unchanged: the two run in that order
// for every key, and the magnet was already a window-space rule for this one. A run the
// magnet decided emits a horizontal guide, which is exactly how this test tells the two
// apart instead of assuming.
func TestSnappedTabStripLandsOnTheWindowGrid(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	gridDecided := 0
	for _, tc := range themeFitDragCases {
		t.Run(tc.name, func(t *testing.T) {
			a := stageTabbarlessCourtroom(t, stripEditTheme)
			a.ctx = ctx
			a.uiScalePct = 100
			a.layoutEdit, a.layoutSnap = true, true
			tc.apply(a)
			a.themeLay.valid = false

			lay := a.themeWindowLayout(tc.w, tc.h)
			before, ok := lay.rect(themeTabBarKey)
			if !ok {
				t.Fatal("the themed editor must be handed a box for the strip")
			}
			ctx.mouseX, ctx.mouseY = before.X+before.W/2, before.Y+before.H/2
			ctx.mouseDown, a.editPrev = true, false
			a.drawLayoutEditor(tc.w, tc.h, lay)
			ctx.mouseY += stripGridDragDY
			a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))
			ctx.mouseDown = false
			a.drawLayoutEditor(tc.w, tc.h, a.themeWindowLayout(tc.w, tc.h))

			after, ok := a.themeWindowLayout(tc.w, tc.h).rect(themeTabBarKey)
			if !ok {
				t.Fatal("the strip lost its box during the snapped drag")
			}
			for _, g := range a.alignGuides {
				if !g.vertical {
					return // the magnet took this axis; grid-first/magnet-second is the contract
				}
			}
			gridDecided++
			if after.Y%layoutGridDesign != 0 {
				t.Errorf("the grid put the strip at screen Y=%d, which is not on the window's %d px grid — it was rounded against the canvas origin (offY=%d)",
					after.Y, layoutGridDesign, a.themeWindowLayout(tc.w, tc.h).offY)
			}
		})
	}
	if gridDecided == 0 {
		t.Fatal("fixture: the magnet decided every case, so the grid's phase was never tested")
	}
}

// TestTabStripRoundTripIsStableAcrossScales proves the forward map and its preimage
// are an exact pair directly, independent of any gesture: every painted position maps
// back to a stored value that maps forward to the same pixel. That is the property the
// scaled map could not have — its image has gaps once the scale exceeds 1, so whole
// screen positions were simply unreachable and a one-pixel drag had nowhere to land.
func TestTabStripRoundTripIsStableAcrossScales(t *testing.T) {
	for _, tc := range themeFitDragCases {
		t.Run(tc.name, func(t *testing.T) {
			a := stageTabbarlessCourtroom(t, stripEditTheme)
			tc.apply(a)
			a.themeLay.valid = false
			lay := a.themeWindowLayout(tc.w, tc.h)
			if !lay.valid {
				t.Fatal("themeWindowLayout did not validate")
			}
			// Walk a band of screen positions through preimage → forward and demand
			// the identity.
			for _, sx := range []int32{0, 1, 2, 3, 5, 17, 100, 331, 460, 461, 462} {
				for _, sy := range []int32{0, 1, 22, 23, 24, 67, 200} {
					if sx > tc.w-lay.tabStripW || sy > tc.h-tabBarH {
						continue // outside the window: the paint clamps, by design
					}
					stored := stripDesignRect(int(sx-lay.offX), int(sy-lay.offY))
					parkTabStripForTest(a, stripEditTheme, stored)
					got, ok := a.themeWindowLayout(tc.w, tc.h).rect(themeTabBarKey)
					if !ok {
						t.Fatalf("(%d,%d): the strip lost its box", sx, sy)
					}
					if got.X != sx || got.Y != sy {
						t.Fatalf("scale %.4f: screen (%d,%d) stored as %+v painted back at (%d,%d) — the map is not invertible",
							lay.scaleX, sx, sy, stored, got.X, got.Y)
					}
				}
			}
		})
	}
}
