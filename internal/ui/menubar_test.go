package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// The app-wide menu bar (#14, menubar.go). These pin the four things that make it a
// menu bar rather than a painted strip: the fence-ordering precondition, the modal
// fence's set/RELEASE discipline, the standard menu-bar input grammar, and the
// per-frame allocation budget it has to share with every screen it floats over.

// menuBarUnderRect stands in for a widget a SCREEN draws in the top band — an AO2
// theme's control button, the tab strip, the lobby's utility row — i.e. something
// painted under the strip by a pass that runs after menuBarFrame.
var menuBarUnderRect = sdl.Rect{X: 40, Y: 4, W: 120, H: 14}

// stageMenuBarApp builds the minimal App the bar needs: real prefs (the placeholder
// items read them) and a fontless Ctx, which is enough for every geometry and input
// path — TextWidth returns 0 without a font, so titles are pad-only but present.
func stageMenuBarApp(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	a.ctx.mouseX, a.ctx.mouseY = -1, -1 // start with the pointer off every rect
	return a
}

// menuBarFrameFor runs one phase-1 the way App.Frame does — with the per-frame
// overlay-fence registry cleared first. That clear is BeginFrame's job (ui.go) and
// these unit fixtures don't run it; without it the BOUNDED registry (overlayFenceCap)
// fills across successive synthetic frames and later publications are dropped.
func menuBarFrameFor(a *App, w, h int32) {
	a.ctx.overlayFenceN, a.ctx.overlayFenceDrops = 0, 0
	a.ctx.overlayOwner, a.ctx.overlayOwnerMark = false, 0
	a.menuBarFrame(w, h)
}

// TestMenuBarStandsDownUnderALayoutEditor is the top-band ownership rule. Both layout
// editors paint a 26 px banner at Y=0 and put their ENTIRE control row at Y=2..24 —
// Done, Reset all, Snap, Grid/Aspect, Magnet, Profile. drawMenuBar runs AFTER the
// screen dispatch, so an opaque 22 px strip painted there buries almost all of it; the
// chips still answer the mouse (both editors hit-test with raw pointIn, which no fence
// covers) but they are invisible, and Done is the editor's primary exit — reachable in
// one click from the Extras menu's own "Edit layout…" row.
//
// The RESERVED BAND must not move with the paint: it is what every screen and the
// themed layout cache offset content by, so dropping it on entry would shift the very
// widgets the editor exists to place.
func TestMenuBarStandsDownUnderALayoutEditor(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*App)
	}{
		{"classic slot editor", func(a *App) { a.classicEdit = true }},
		{"themed rect editor", func(a *App) { a.layoutEdit = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := stageMenuBarApp(t)
			c := a.ctx
			const w, h = int32(1280), int32(720)
			band := a.topChromeH()

			a.menuOpen = menuBarMenus[0].title
			tc.set(a)
			menuBarFrameFor(a, w, h)

			if a.menuBar.draws {
				t.Error("the editor owns the top band: the strip must not paint over its banner (Done lives at Y=2..24)")
			}
			if c.overlayFenceN != 0 {
				t.Errorf("a strip that paints nothing must fence nothing, got registry depth %d", c.overlayFenceN)
			}
			if a.menuBarOpen() || c.modalOn || a.menuFenceOn {
				t.Error("an editor frame must close the pane and release the modal fence")
			}
			if got := a.topChromeH(); got != band {
				t.Errorf("the reserved band became %d (was %d) — the editor would shift the widgets it is placing", got, band)
			}
			if a.menuBarHeight() != menuBarH {
				t.Errorf("menuBarHeight = %d while editing, want the band kept at %d", a.menuBarHeight(), menuBarH)
			}

			// Leaving the editor brings the strip straight back.
			a.classicEdit, a.layoutEdit = false, false
			menuBarFrameFor(a, w, h)
			if !a.menuBar.draws {
				t.Error("leaving the editor must bring the strip back")
			}
		})
	}
}

// TestMenuBarPaintDecisionSurvivesAMidFrameEditor is the rule above at the FRAME's
// resolution rather than the predicate's. The bar's two halves straddle the screen
// dispatch, so an editor can be armed or dismissed between them and phase 1's answer
// goes stale in BOTH directions — the reason the decision is re-taken after
// menuBarInput and settled again at the paint site (menuBarPaintsNow).
//
// Measured at 1280x720 before the fix: the Extras → "Edit layout…" row fires INSIDE
// menuBarInput, so the frame it arms an editor ended with menuBar.draws=true,
// menuBarPaints=false and one published fence — the strip painted straight over the
// banner the courtroom pass then drew. The mirror: the classic editor lays out its
// chips, handles their clicks and paints its banner LAST, so the frame Done is pressed
// returned before painting anything, and phase 1's stand-down left the 22 px band EMPTY
// for that frame.
func TestMenuBarPaintDecisionSurvivesAMidFrameEditor(t *testing.T) {
	const w, h = int32(1280), int32(720)

	t.Run("our own Edit layout row arms one", func(t *testing.T) {
		a := stageMenuBarApp(t)
		c := a.ctx
		a.screen = ScreenCourtroom // menuInCourtroom gates the row
		a.menuOpen = menuExtrasTitle
		ei := menuBarIndexOf(menuExtrasTitle)
		ri := menuItemIndexOf(menuExtrasItems, "Edit layout…")
		if ei < 0 || ri < 0 {
			t.Fatal("fixture: the Extras menu no longer carries an Edit layout row")
		}
		row := menuPaneRowRect(a.menuPaneRect(ei, w, h), menuExtrasItems, ri)
		c.mouseX, c.mouseY = row.X+row.W/2, row.Y+row.H/2
		c.clicked = true
		menuBarFrameFor(a, w, h)

		if !a.layoutEditorArmed() {
			t.Fatal("fixture: the row did not arm an editor, so nothing is being tested")
		}
		if a.menuBar.draws {
			t.Error("the frame our own row armed the editor still latched a paint — the strip lands on the banner")
		}
		if c.overlayFenceN != 0 {
			t.Errorf("the strip published %d fence(s) for pixels it no longer paints", c.overlayFenceN)
		}
		if a.menuBarOpen() || a.menuFenceOn {
			t.Error("the pane must be closed and the modal fence released on the arm frame")
		}
		// And the editor's banner, painted by the pass below, keeps it down.
		a.editorBannerPainted = true
		if a.menuBarPaintsNow() {
			t.Error("the strip paints over a banner that reached the screen this frame")
		}
	})

	t.Run("something later in the frame arms one", func(t *testing.T) {
		// A hotkey or the compact toolbox's Edit chip arms an editor from INSIDE the
		// courtroom pass, long after phase 1 — and early enough that the editor's own
		// draw site still runs, so the banner is up.
		a := stageMenuBarApp(t)
		a.screen = ScreenCourtroom
		menuBarFrameFor(a, w, h)
		if !a.menuBar.draws {
			t.Fatal("fixture: the bar must be painting before the editor arms")
		}
		a.classicEdit = true
		a.editorBannerPainted = true // what drawClassicEditor publishes when it paints
		if a.menuBarPaintsNow() {
			t.Error("an editor armed after phase 1 still had the strip painted over its banner")
		}
	})

	t.Run("armed after every draw site has run", func(t *testing.T) {
		// The command palette draws AFTER the courtroom pass, so the frame it arms an
		// editor paints no banner at all. Standing the bar down on the armed FLAG would
		// blank the band for that frame — a fresh gap where the other two close one.
		a := stageMenuBarApp(t)
		a.screen = ScreenCourtroom
		menuBarFrameFor(a, w, h)
		a.layoutEdit = true // armed, but nothing drew a banner this frame
		if !a.menuBarPaintsNow() {
			t.Error("the strip vanished on a frame where no editor banner was ever painted")
		}
	})

	t.Run("dismissed before its banner painted", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			stop func(*App)
		}{
			{"classic Done", func(a *App) { a.stopClassicEdit() }},
			{"themed force-stop", func(a *App) { a.stopLayoutEdit() }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				a := stageMenuBarApp(t)
				a.screen = ScreenCourtroom
				a.classicEdit, a.layoutEdit = true, true
				menuBarFrameFor(a, w, h)
				if a.menuBar.draws {
					t.Fatal("fixture: phase 1 must have stood the bar down")
				}
				a.classicEdit, a.layoutEdit = false, false
				tc.stop(a)
				if !a.menuBarPaintsNow() {
					t.Error("the editor stopped without painting anything, and the top band was left empty for the frame")
				}
				if a.menuBar.strip.W != w || a.menuBar.strip.H != menuBarH {
					t.Errorf("the recovered strip is %+v, want the full-width band %dx%d", a.menuBar.strip, w, menuBarH)
				}
				// Recovered INERT: no fence was published and no input was taken for it,
				// so it must not offer hover feedback either.
				a.ctx.mouseX, a.ctx.mouseY = a.menuBarTitleRect(0).X+1, 1
				if a.menuBarHot(a.menuBarTitleRect(0)) {
					t.Error("the recovered strip highlights a title whose click it already refused")
				}
			})
		}
	})

	t.Run("no band at all", func(t *testing.T) {
		// A full-window mode takes the band away outright, so there is nothing to
		// recover — editorHeld must stay clear or the bar would paint over an export.
		for name, set := range map[string]func(*App){
			"gif export":  func(a *App) { a.gifExporting = true },
			"theater":     func(a *App) { a.theaterOn = true },
			"scene maker": func(a *App) { a.makerOpen = true },
		} {
			a := stageMenuBarApp(t)
			a.classicEdit = true
			set(a)
			menuBarFrameFor(a, w, h)
			if a.menuBar.editorHeld || a.menuBarPaintsNow() {
				t.Errorf("%s: the strip has no band there, but it recorded one to recover", name)
			}
		}
	})
}

// TestMenuBarReleaseDoesNotStealASiblingFence pins the SCOPE of the modal-fence
// release. c.modalOn is one shared flag with five drivers, and the other four — the
// emoji picker, the reaction picker, the roster menu and the What's New modal — all
// run EARLIER in App.Frame. The bar hit-tests itself with raw pointIn, so a pane can
// be opened while a sibling already holds the fence; a blind `modalOn = false` on the
// frame our pane closes then leaves the screen under the sibling live for a frame.
func TestMenuBarReleaseDoesNotStealASiblingFence(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	// The roster menu is open and holding the shared flag.
	a.rosterMenuOpen, a.rosterMenuFenceOn = true, true
	c.modalOn = true

	a.menuOpen = menuBarMenus[0].title
	menuBarFrameFor(a, w, h)
	if !c.modalOn || !a.menuFenceOn {
		t.Fatal("the open pane must hold the fence too")
	}

	a.closeMenuBar()
	menuBarFrameFor(a, w, h)
	if !c.modalOn {
		t.Error("closing our pane released the ROSTER MENU's fence: everything under it is live for a frame")
	}
	if a.menuFenceOn {
		t.Error("our own bookkeeping must still clear, or the next close would re-check nothing")
	}

	// With no sibling holding it, the ordinary release still happens.
	a.rosterMenuOpen, a.rosterMenuFenceOn = false, false
	c.modalOn = false
	a.menuOpen = menuBarMenus[0].title
	menuBarFrameFor(a, w, h)
	a.closeMenuBar()
	menuBarFrameFor(a, w, h)
	if c.modalOn || a.menuFenceOn {
		t.Error("with nobody else holding it, the frame the pane closes must release the fence")
	}
}

// TestMenuPaneCullAndHitTestAgree pins that a row past a CLAMPED pane's bottom edge is
// neither painted nor clickable. The draw stopped at the first row whose bottom passed
// the clamp while the hit test accepted any point inside both the pane and the row — so
// the last, undrawn row stayed clickable through its few visible pixels.
func TestMenuPaneCullAndHitTestAgree(t *testing.T) {
	a := stageMenuBarApp(t)
	const w = int32(1280)
	mi := menuBarIndexOf(menuServersTitle)
	items := menuBarMenus[mi].items

	// A window with room for two and a half rows: the third is cut mid-row, which is
	// exactly the case that produced a clickable sliver.
	h := menuBarH + menuPanePadY + 2*menuPaneRowH + menuPaneRowH/2 + menuPaneEdgePad
	pane := a.menuPaneRect(mi, w, h)
	culled, sliverY := -1, int32(0)
	for i := range items {
		r := menuPaneRowRect(pane, items, i)
		if menuPaneRowFits(pane, r) || items[i].kind == menuItemSeparator {
			continue
		}
		if r.Y < pane.Y+pane.H { // a visible sliver of an unpainted row
			culled, sliverY = i, pane.Y+pane.H-1
			break
		}
	}
	if culled < 0 {
		t.Fatalf("fixture: the %d-tall window must cut a row mid-row (pane %+v)", h, pane)
	}
	row := menuPaneRowRect(pane, items, culled)
	if got := menuPaneRowAt(pane, items, row.X+row.W/2, sliverY); got >= 0 {
		t.Errorf("row %d is culled by the draw but hit-tested as row %d — a menu must never fire a command it did not paint", culled, got)
	}
	// The rows that DO fit are still reachable, i.e. the fix culls and nothing more.
	first := menuPaneRowRect(pane, items, 0)
	if got := menuPaneRowAt(pane, items, first.X+first.W/2, first.Y+first.H/2); got != 0 {
		t.Errorf("the first row must still hit, got %d", got)
	}
}

// TestMenuBarShowsNoHoverFeedbackWhileInert: under a blocking window modal the strip
// paints but refuses input, so it must not light up under the cursor either.
func TestMenuBarShowsNoHoverFeedbackWhileInert(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	title := a.menuBarTitleRect(0)
	c.mouseX, c.mouseY = title.X+title.W/2, title.Y+title.H/2
	menuBarFrameFor(a, w, h)
	if !a.menuBarHot(title) {
		t.Fatal("fixture: an ordinary frame must highlight the title under the cursor")
	}

	a.showQuitConfirm = true
	menuBarFrameFor(a, w, h)
	if !a.menuBar.draws {
		t.Fatal("the strip still paints under a modal — it just goes inert")
	}
	if !a.menuBar.inert {
		t.Fatal("the latch must record the inert frame, so phase 2 replays phase 1's answer")
	}
	if a.menuBarHot(title) {
		t.Error("a title that cannot be opened must not light up under the cursor")
	}
}

// TestMenuBarFrameFitsInTheFenceRegistry is the loud half of the bounded-registry
// rule: a real frame with a pane AND a submenu open, plus the compact toolbox strip
// published inside the courtroom pass, must not have a single publication DROPPED.
// A drop is silent — the occluder still paints, only its input protection is gone.
func TestMenuBarFrameFitsInTheFenceRegistry(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	mi := menuBarIndexOf("View")
	a.menuOpen = menuBarMenus[mi].title
	for _, it := range menuBarMenus[mi].items {
		if it.kind == menuItemSubmenu {
			a.menuSub = it.label
			break
		}
	}
	menuBarFrameFor(a, w, h)
	if a.menuBar.subRow < 0 {
		t.Fatal("fixture: the submenu must be open")
	}
	// The last publisher of a real frame: the compact toolbox, from inside the pass.
	c.fenceOverlay(sdl.Rect{X: 900, Y: 600, W: 160, H: 22})

	if c.overlayFenceDrops != 0 {
		t.Errorf("%d fence publication(s) dropped on an ordinary frame — the LAST publisher (the toolbox) is the one that loses, silently restoring #26's click-through", c.overlayFenceDrops)
	}
	if c.overlayFenceN >= overlayFenceCap {
		t.Errorf("the registry is full at %d/%d on an ordinary frame: no headroom left", c.overlayFenceN, overlayFenceCap)
	}
}

// TestMenuBarFenceOutlivesTheScreenPass is THE ordering precondition (menubar.go's
// header). App-level publication has to sit BELOW every in-pass publisher in the
// fence registry, because registry order is paint order:
//
//   - a widget a screen draws under the strip must be inert, and
//   - drawCourtroom's `defer c.overlayFenceRelease(mark)` must not take the bar's
//     fence down with the pass, or the post-courtroom overlays would draw through it.
func TestMenuBarFenceOutlivesTheScreenPass(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	// Cursor over the buried widget, which is under the strip band. Baseline: this
	// is the bug — with no fence the buried widget hovers and would fire.
	c.mouseX, c.mouseY = menuBarUnderRect.X+10, menuBarUnderRect.Y+2
	c.downX, c.downY = c.mouseX, c.mouseY
	c.clicked = true
	if !c.hovering(menuBarUnderRect) || !c.ClickedIn(menuBarUnderRect) {
		t.Fatal("test setup: the cursor must start over the buried widget")
	}
	c.clicked = false

	menuBarFrameFor(a, w, h)
	if !a.menuBar.draws || !a.menuBar.live {
		t.Fatalf("the bar must draw and be live on a plain lobby frame (draws=%v live=%v)", a.menuBar.draws, a.menuBar.live)
	}
	if c.overlayFenceN == 0 {
		t.Fatal("menuBarFrame must publish the strip's overlay fence")
	}
	if c.hovering(menuBarUnderRect) {
		t.Error("a widget a screen draws under the menu bar must be inert (#14/#26): the strip paints over it")
	}

	// Now replay what drawCourtroom does around its own pass.
	passMark := c.overlayFenceMark()
	c.fenceOverlay(sdl.Rect{X: 900, Y: 600, W: 120, H: 22}) // the compact toolbox strip
	c.overlayFenceRelease(passMark)                         // the pass returns
	if c.hovering(menuBarUnderRect) {
		t.Error("the pass's release must drop only ITS OWN fences — the App-level menu bar fence has to survive the screen pass")
	}

	// Outside the strip the frame is untouched: this is a rect fence, not modalOn.
	below := sdl.Rect{X: 40, Y: menuBarH + 40, W: 120, H: 20}
	c.mouseX, c.mouseY = below.X+5, below.Y+5
	if !c.hovering(below) {
		t.Error("a widget clear of the strip must stay live — the bar fences its rect, not the frame")
	}
}

// TestMenuBarPaneOccludesAnOccluder is the composability half of the precondition:
// the compact toolbox brackets its own draw with pushOverlayOwner, and that suspend
// is MARK-scoped. An open menu pane published App-level (i.e. EARLIER) must still
// occlude the toolbox's chips, or the click-through bug #26 fixed reappears one
// level up with the roles swapped.
func TestMenuBarPaneOccludesAnOccluder(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	a.menuOpen = menuBarMenus[0].title
	menuBarFrameFor(a, w, h)
	pane := a.menuBar.pane
	if pane.W <= 0 || pane.H <= 0 {
		t.Fatal("an open menu must resolve a pane rect")
	}

	// An occluder drawn later whose strip overlaps the open pane.
	strip := sdl.Rect{X: pane.X + 4, Y: pane.Y + 4, W: 80, H: 20}
	ownMark := c.overlayFenceMark()
	c.fenceOverlay(strip)
	c.mouseX, c.mouseY = strip.X+5, strip.Y+5

	prev := c.pushOverlayOwner(ownMark)
	if c.hovering(strip) {
		t.Error("an occluder that suspends its OWN fence must still yield to the menu bar's earlier pane fence")
	}
	if !c.overlayFenced(c.mouseX, c.mouseY) {
		t.Error("the raw query must agree with hovering(): the pane fence is published earlier, so it is still in force")
	}
	c.popOverlayOwner(prev)
}

// TestMenuBarModalFenceHeldAndReleased is the freeze guard: modalOn persists across
// frames, so an open pane must hold it and a closed one must RELEASE it (app.go says
// an un-released modalOn freezes the whole UI).
func TestMenuBarModalFenceHeldAndReleased(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	menuBarFrameFor(a, w, h)
	if c.modalOn || a.menuFenceOn {
		t.Fatal("a closed menu bar must not hold the modal fence")
	}

	a.menuOpen = menuBarMenus[1].title
	menuBarFrameFor(a, w, h)
	if !c.modalOn || !a.menuFenceOn {
		t.Fatal("an open pane must hold the kit's modal fence")
	}

	a.closeMenuBar()
	menuBarFrameFor(a, w, h)
	if c.modalOn || a.menuFenceOn {
		t.Fatal("the frame the pane closes must RELEASE the modal fence — an un-released modalOn freezes the UI")
	}

	// A frame on which the bar is not shown at all still has to release it.
	a.menuOpen = menuBarMenus[1].title
	menuBarFrameFor(a, w, h)
	a.theaterOn = true
	menuBarFrameFor(a, w, h)
	if c.modalOn || a.menuFenceOn || a.menuBarOpen() {
		t.Fatal("hiding the bar (theater mode) must close the pane and release the fence")
	}
}

// TestMenuBarEscapeClosesPane pins the Esc route. Without a case in closeTopOverlay,
// Esc falls through to app.go's per-screen arms, LEAVES the screen and strands the
// pane open behind its fence — the bug floatbox.go documents for the .demo browser.
func TestMenuBarEscapeClosesPane(t *testing.T) {
	a := stageMenuBarApp(t)
	a.menuOpen = menuBarMenus[0].title
	if !a.closeTopOverlay() {
		t.Fatal("closeTopOverlay must report that it closed the menu bar's pane")
	}
	if a.menuBarOpen() {
		t.Fatal("Esc must close the open pane")
	}
	if a.closeTopOverlay() {
		t.Error("with nothing open, closeTopOverlay must fall through so Esc still leaves the screen")
	}
}

// TestMenuBarClickOpensTogglesAndSwitches walks the standard menu-bar grammar:
// click to open, click the same title to close, and — once ANY pane is open —
// hover-to-switch without a second click.
func TestMenuBarClickOpensTogglesAndSwitches(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	first := a.menuBarTitleRect(0)
	second := a.menuBarTitleRect(1)

	c.mouseX, c.mouseY = first.X+first.W/2, first.Y+first.H/2
	c.clicked = true
	menuBarFrameFor(a, w, h)
	if a.menuOpen != menuBarMenus[0].title {
		t.Fatalf("a click on a title must open its menu, got %q", a.menuOpen)
	}
	if c.clicked {
		t.Error("the bar must consume the click it acted on")
	}

	// Hover-to-switch: no click, just the cursor on a neighbouring title.
	c.mouseX, c.mouseY = second.X+second.W/2, second.Y+second.H/2
	menuBarFrameFor(a, w, h)
	if a.menuOpen != menuBarMenus[1].title {
		t.Fatalf("hovering another title while a pane is open must switch to it, got %q", a.menuOpen)
	}

	// Clicking the OPEN title toggles it shut.
	c.clicked = true
	menuBarFrameFor(a, w, h)
	if a.menuBarOpen() {
		t.Fatal("clicking the open menu's own title must close it")
	}
}

// TestMenuBarDoesNotStealClicksItDidNotConsume is the other half of the input
// contract: while nothing is open, a click anywhere off the strip belongs to the
// screen underneath and must reach it untouched.
func TestMenuBarDoesNotStealClicksItDidNotConsume(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	c.mouseX, c.mouseY = 400, 400
	c.clicked = true
	menuBarFrameFor(a, w, h)
	if !c.clicked {
		t.Fatal("a closed menu bar must not swallow a click that landed on the screen")
	}

	// With a pane open, the dismissing click IS ours: it closes the menu and is
	// swallowed, so the raw-pointIn sites underneath can't also act on it.
	a.menuOpen = menuBarMenus[0].title
	c.clicked = true
	menuBarFrameFor(a, w, h)
	if a.menuBarOpen() {
		t.Fatal("a click outside the strip and pane must close the open menu")
	}
	if c.clicked {
		t.Error("the dismissing click must be swallowed — the user meant 'put the menu away', not 'press what's under it'")
	}
}

// TestMenuBarSubmenuOpensAndFences pins the submenu: hovering its parent row opens
// the child pane, the child publishes its own fence, and moving to an ordinary row
// closes it again. The content stage needs exactly this for "Theme fit ▸".
func TestMenuBarSubmenuOpensAndFences(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	mi := menuBarIndexOf("View")
	if mi < 0 {
		t.Fatal("the View menu is the fixture: it is the one carrying a submenu")
	}
	items := menuBarMenus[mi].items
	si := -1
	for i := range items {
		if items[i].kind == menuItemSubmenu {
			si = i
			break
		}
	}
	if si < 0 {
		t.Fatal("the View menu must carry a submenu row")
	}

	a.menuOpen = menuBarMenus[mi].title
	menuBarFrameFor(a, w, h)
	pane := a.menuBar.pane
	row := menuPaneRowRect(pane, items, si)

	c.mouseX, c.mouseY = row.X+row.W/2, row.Y+row.H/2
	fencesBefore := c.overlayFenceN
	menuBarFrameFor(a, w, h)
	if a.menuSub != items[si].label {
		t.Fatalf("hovering a submenu row must open it, got %q", a.menuSub)
	}
	if a.menuBar.sub.W <= 0 || a.menuBar.sub.H <= 0 {
		t.Fatal("an open submenu must resolve a pane rect")
	}
	if c.overlayFenceN <= fencesBefore {
		t.Error("the open submenu must publish its own overlay fence — it paints over the screen too")
	}
	// The submenu must not sit under the strip or off the window.
	if a.menuBar.sub.Y < menuBarH || a.menuBar.sub.X+a.menuBar.sub.W > w {
		t.Errorf("the submenu must stay on-window and below the strip: %+v", a.menuBar.sub)
	}

	// Hovering an ordinary row of the parent pane closes the child.
	for i := range items {
		if items[i].kind == menuItemAction || items[i].kind == menuItemCheck {
			r := menuPaneRowRect(pane, items, i)
			c.mouseX, c.mouseY = r.X+r.W/2, r.Y+r.H/2
			break
		}
	}
	menuBarFrameFor(a, w, h)
	if a.menuSub != "" {
		t.Errorf("hovering a non-submenu row must close the child pane, got %q", a.menuSub)
	}
}

// menuRow resolves a row BY LABEL, failing the test when the model no longer carries
// it. The fixtures below used to index the tables positionally, which was fine while
// they were placeholders but pinned row ORDER — a thing no assertion here is about —
// the moment the tables carried real content.
func menuRow(t *testing.T, menuTitle, label string) *menuItem {
	t.Helper()
	mi := menuBarIndexOf(menuTitle)
	if mi < 0 {
		t.Fatalf("no %q menu", menuTitle)
	}
	items := menuBarMenus[mi].items
	ri := menuItemIndexOf(items, label)
	if ri < 0 {
		t.Fatalf("the %q menu has no %q row", menuTitle, label)
	}
	return &items[ri]
}

// TestMenuBarItemActionFiresAndCloses pins the item contract: an enabled row runs
// its action and closes the menu, and a disabled row does neither.
func TestMenuBarItemActionFiresAndCloses(t *testing.T) {
	a := stageMenuBarApp(t)

	// Enabled + checkable: the View menu's performance-overlay toggle.
	viewTitle := menuBarMenus[menuBarIndexOf("View")].title
	perf := menuRow(t, "View", "Performance overlay")
	a.menuOpen = viewTitle
	before := a.perfHUD
	a.fireMenuItem(perf)
	if a.perfHUD == before {
		t.Error("an enabled item's action must fire")
	}
	if a.menuBarOpen() {
		t.Error("firing an item must close the menu (the roster-menu discipline)")
	}

	// A separator is never a target.
	viewItems := menuBarMenus[menuBarIndexOf("View")].items
	sep := -1
	for i := range viewItems {
		if viewItems[i].kind == menuItemSeparator {
			sep = i
			break
		}
	}
	if sep < 0 {
		t.Fatal("the View menu must carry a separator (the decoration fixture)")
	}
	a.menuOpen = viewTitle
	a.fireMenuItem(&viewItems[sep])
	if !a.menuBarOpen() {
		t.Error("a separator must not fire and must not close the menu")
	}

	// Disabled: Servers → Disconnect with no session.
	disc := menuRow(t, menuServersTitle, "Disconnect")
	a.sess = nil
	if a.menuItemEnabled(disc) {
		t.Fatal("Disconnect must be disabled with no session (the enabled-predicate fixture)")
	}
	a.menuOpen = menuServersTitle
	a.fireMenuItem(disc)
	if !a.menuBarOpen() {
		t.Error("a disabled item must neither fire nor close the menu")
	}
}

// TestMenuBarHeightIsZeroWhenHidden pins the accessor the layout stage offsets every
// screen by: it must report ZERO whenever the bar is not shown, so the offset can be
// applied unconditionally without the caller re-deriving the visibility rule.
func TestMenuBarHeightIsZeroWhenHidden(t *testing.T) {
	a := stageMenuBarApp(t)
	if got := a.menuBarHeight(); got != menuBarH {
		t.Fatalf("a plain frame reserves menuBarH (%d), got %d", menuBarH, got)
	}
	for _, tc := range []struct {
		name string
		set  func()
		undo func()
	}{
		{"theater", func() { a.theaterOn = true }, func() { a.theaterOn = false }},
		{"gif export", func() { a.gifExporting = true }, func() { a.gifExporting = false }},
		{"scene maker", func() { a.makerOpen = true }, func() { a.makerOpen = false }},
	} {
		tc.set()
		if got := a.menuBarHeight(); got != 0 {
			t.Errorf("%s takes the whole window, so the bar reserves nothing — got %d", tc.name, got)
		}
		tc.undo()
	}
	if got := a.menuBarHeight(); got != menuBarH {
		t.Fatalf("the height must come back once the overlay closes, got %d", got)
	}
}

// TestMenuBarSuppressedUnderBlockingModal pins that the bar stands down when a
// blocking window modal owns the frame: an open pane would otherwise hold modalOn
// and blank the modal's own buttons.
func TestMenuBarSuppressedUnderBlockingModal(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	a.menuOpen = menuBarMenus[0].title
	a.showQuitConfirm = true
	menuBarFrameFor(a, w, h)
	if a.menuBarOpen() {
		t.Error("a blocking modal must force the pane closed")
	}
	if c.modalOn || a.menuFenceOn {
		t.Error("and the bar's modal fence must be released, so the modal's buttons stay live")
	}
	if !a.menuBar.draws {
		t.Error("the strip still PAINTS under a modal — it just goes inert")
	}
}

// TestMenuBarPublishesNothingWhilePointerIsFenced pins the draw-vs-fence rule for the
// overlays that paint ABOVE the bar and unfence the pointer for their own pass (the
// blocking modals, the hotkey sheet): a fence we left in the registry would blank
// THEIR widgets over pixels we never painted.
func TestMenuBarPublishesNothingWhilePointerIsFenced(t *testing.T) {
	a := stageMenuBarApp(t)
	c := a.ctx
	const w, h = int32(1280), int32(720)

	c.fencePointer()
	menuBarFrameFor(a, w, h)
	if c.overlayFenceN != 0 {
		t.Errorf("a later overlay already owns the pointer: publish nothing (depth %d)", c.overlayFenceN)
	}
	if a.menuBar.live {
		t.Error("the latch must record that the bar is inert this frame")
	}
	c.unfencePointer()
}

// TestMenuBarModelWellFormed pins the invariants the content stage must keep when it
// replaces the tables: a submenu carries rows, a separator carries no behaviour, and
// a checkable carries a predicate (else it is an action wearing a check gutter).
func TestMenuBarModelWellFormed(t *testing.T) {
	var walk func(t *testing.T, path string, items []menuItem)
	walk = func(t *testing.T, path string, items []menuItem) {
		for i := range items {
			it := &items[i]
			switch it.kind {
			case menuItemSeparator:
				if it.act != nil || it.checked != nil || it.sub != nil || it.label != "" {
					t.Errorf("%s row %d: a separator is decoration — no label, no action, no children", path, i)
				}
			case menuItemSubmenu:
				if len(it.sub) == 0 {
					t.Errorf("%s row %d (%q): a submenu with no rows opens an empty box", path, i, it.label)
				}
				walk(t, path+" > "+it.label, it.sub)
			case menuItemCheck:
				if it.checked == nil {
					t.Errorf("%s row %d (%q): a checkable needs a checked predicate", path, i, it.label)
				}
			}
			if it.kind != menuItemSeparator && it.label == "" {
				t.Errorf("%s row %d: every drawable row needs a label", path, i)
			}
		}
	}
	seen := map[string]bool{}
	for i := range menuBarMenus {
		m := &menuBarMenus[i]
		if m.title == "" {
			t.Fatalf("menu %d has no title — menuBarIndexOf keys on it", i)
		}
		if seen[m.title] {
			t.Fatalf("duplicate menu title %q: the open pane is remembered BY TITLE", m.title)
		}
		seen[m.title] = true
		if len(m.items) == 0 {
			t.Errorf("menu %q has no rows", m.title)
		}
		walk(t, m.title, m.items)
	}
}

// TestMenuBarThemeFitSubmenuMirrorsSettings pins that the submenu is DERIVED from
// themeFitOptions rather than hand-copied: the mode constants are persisted on disk
// and append-only, so a second hand-maintained ordering would eventually reinterpret
// somebody's saved mode.
func TestMenuBarThemeFitSubmenuMirrorsSettings(t *testing.T) {
	if len(menuThemeFitItems) != len(themeFitOptions) {
		t.Fatalf("the submenu has %d rows, the settings table %d", len(menuThemeFitItems), len(themeFitOptions))
	}
	a := stageMenuBarApp(t)
	for i := range menuThemeFitItems {
		it := &menuThemeFitItems[i]
		if it.arg != themeFitOptions[i].mode || it.label != themeFitOptions[i].label {
			t.Fatalf("row %d diverged from themeFitOptions: %q/%d vs %q/%d",
				i, it.label, it.arg, themeFitOptions[i].label, themeFitOptions[i].mode)
		}
		a.themeLay.valid = true
		a.fireMenuItem(it)
		if got := a.d.Prefs.ThemeFitMode(); got != it.arg {
			t.Fatalf("row %d must apply mode %d through SetThemeFit, got %d", i, it.arg, got)
		}
		if a.themeLay.valid {
			t.Fatalf("row %d must invalidate the layout cache, like every other fit mutation", i)
		}
		if !it.checked(a, it.arg) {
			t.Fatalf("row %d must read as checked once it is the active mode", i)
		}
	}
}

// stageMenuBarDrawApp builds a real-Ctx App on a software renderer for the alloc
// gates below — the bar draws on EVERY screen, so it shares TestDrawLobbyZeroAlloc's
// budget and must cost nothing per frame.
func stageMenuBarDrawApp(t *testing.T) (*App, func()) {
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
	a.screen = ScreenLobby
	a.lobbyStatus = "Servers loaded."
	return a, cleanup
}

// TestDrawLobbyWithMenuBarZeroAlloc is the always-on gate: the closed bar rides every
// screen, so a settled lobby frame WITH it must still allocate nothing. It mirrors
// TestDrawLobbyZeroAlloc's fixture on purpose — that gate deliberately draws a lobby
// with no menu bar, so without this one the bar would ship unmeasured.
func TestDrawLobbyWithMenuBarZeroAlloc(t *testing.T) {
	a, cleanup := stageMenuBarDrawApp(t)
	defer cleanup()

	const w, h = int32(1280), int32(720)
	a.ctx.mouseX, a.ctx.mouseY = a.menuBarTitleRect(0).X+2, 6 // parked on a title: the hover highlight is measured too
	draw := func() {
		menuBarFrameFor(a, w, h)
		a.noteScreenTransition()
		a.drawLobby(w, h)
		a.drawMenuBar(w, h)
	}
	// Prove the fixture really painted the bar before measuring anything: a silently
	// hidden strip would make this a duplicate of TestDrawLobbyZeroAlloc and leave
	// the widget uncovered again.
	draw()
	if !a.menuBar.draws || !a.menuBar.live {
		t.Fatalf("the fixture must draw a live menu bar (draws=%v live=%v)", a.menuBar.draws, a.menuBar.live)
	}

	if n := allocsPerFrame(allocGateFrames, 0, draw); n != 0 {
		t.Fatalf("a settled lobby frame with the menu bar allocates %.1f/op, want 0 — a per-frame allocation shipped (fix the alloc, don't loosen the gate)", n)
	}
}

// TestDrawMenuBarOpenPaneZeroAlloc is the same gate with a pane AND its submenu open,
// so the pane measure/place/paint path and the per-row checked/enabled predicates are
// measured too — the part of the widget the closed-bar gate above never reaches.
func TestDrawMenuBarOpenPaneZeroAlloc(t *testing.T) {
	a, cleanup := stageMenuBarDrawApp(t)
	defer cleanup()

	const w, h = int32(1280), int32(720)
	mi := menuBarIndexOf("View")
	a.menuOpen = menuBarMenus[mi].title
	items := menuBarMenus[mi].items
	for i := range items {
		if items[i].kind == menuItemSubmenu {
			a.menuSub = items[i].label
			break
		}
	}
	// Park the cursor inside the OPEN SUBMENU: that keeps the state fixed (no
	// hover-switch on the strip, no parent-row hover closing the child) while still
	// exercising a hovered row's highlight.
	menuBarFrameFor(a, w, h)
	if a.menuBar.sub.W <= 0 {
		t.Fatal("the fixture must open a submenu")
	}
	a.ctx.mouseX = a.menuBar.sub.X + a.menuBar.sub.W/2
	a.ctx.mouseY = a.menuBar.sub.Y + menuPanePadY + menuPaneRowH/2

	draw := func() {
		menuBarFrameFor(a, w, h)
		a.noteScreenTransition()
		a.drawLobby(w, h)
		a.drawMenuBar(w, h)
	}
	draw()
	if !a.menuBarOpen() || a.menuBar.subRow < 0 {
		t.Fatal("the fixture must stay open across a drawn frame — a state flip would make the gate measure a different frame")
	}

	if n := allocsPerFrame(allocGateFrames, 0, draw); n != 0 {
		t.Fatalf("a settled frame with an open menu pane allocates %.1f/op, want 0 — a per-frame allocation shipped (fix the alloc, don't loosen the gate)", n)
	}
}
