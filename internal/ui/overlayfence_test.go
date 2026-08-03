package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// The overlay-fence primitive (overlayfence.go) exists because the kit is
// immediate-mode and single-pass: a widget drawn EARLY cannot know a widget drawn
// LATER covers it, so the later one wins the pixels while BOTH win the click. That
// is issue #26 (a compact-toolbox chip also pressing the AO2 theme's "Call Mod"
// button underneath it) and the general shape of open issue #37.

// fenceUnderRect is the stand-in for a widget drawn EARLY in a pass — an AO2
// theme's control button, say — that a later overlay lands on top of.
var fenceUnderRect = sdl.Rect{X: 100, Y: 100, W: 120, H: 30}

// fenceOverRect is the overlay: it covers the middle of fenceUnderRect but leaves
// its left edge exposed, so the tests can prove the fence is rect-scoped and not a
// modalOn-style blanket over the whole frame.
var fenceOverRect = sdl.Rect{X: 140, Y: 90, W: 200, H: 60}

// TestOverlayFenceBlocksWidgetsBeneath pins the three halves of the contract: a
// widget under a published fence neither hovers nor fires, the fence OWNER can still
// hit-test itself, and a point outside the fence rect is untouched.
func TestOverlayFenceBlocksWidgetsBeneath(t *testing.T) {
	c := &Ctx{}
	// Cursor over the overlap: inside both the early widget and the later overlay.
	c.mouseX, c.mouseY = 160, 110
	c.clicked = true
	c.downX, c.downY = c.mouseX, c.mouseY // ClickedIn also needs the press origin

	// Baseline: this is the bug. Without a fence the buried widget hovers and fires.
	if !c.hovering(fenceUnderRect) || !c.ClickedIn(fenceUnderRect) {
		t.Fatal("test setup: the cursor must start over the buried widget")
	}

	ownMark := c.overlayFenceMark() // the depth our own publication will land at
	c.fenceOverlay(fenceOverRect)

	if c.hovering(fenceUnderRect) {
		t.Error("a widget under a published overlay fence must not hover (#26/#37)")
	}
	if c.ClickedIn(fenceUnderRect) {
		t.Error("a widget under a published overlay fence must not fire — ClickedIn routes through hovering()")
	}
	// The occluder itself: fenced like everyone else until it claims ownership.
	if c.hovering(fenceOverRect) {
		t.Error("the fence rect blanks by position, so even the owner reads false until it pushes ownership")
	}
	prev := c.pushOverlayOwner(ownMark)
	if !c.hovering(fenceOverRect) {
		t.Error("inside pushOverlayOwner the occluder must hit-test its OWN widgets (the dropdown's raw-pointIn precedent)")
	}
	if !c.hovering(fenceUnderRect) {
		t.Error("with only the owner's OWN fence published, suspending it leaves the buried widget live to the owner's own hit tests")
	}
	c.popOverlayOwner(prev)
	if c.hovering(fenceOverRect) {
		t.Error("popOverlayOwner must restore the fence (it was off before the push)")
	}

	// Rect-scoped, not a blanket: the exposed left edge of the buried widget is
	// still live. This is the whole reason the primitive is not just modalOn.
	c.mouseX, c.mouseY = fenceUnderRect.X+5, 110
	c.downX, c.downY = c.mouseX, c.mouseY
	if !c.hovering(fenceUnderRect) {
		t.Error("a point outside every published fence must stay live — the fence covers its rect, not the frame")
	}
}

// TestOverlayFenceOwnerSuspendComposes is the composability contract: the owner
// suspend is MARK-scoped, not a switch that turns the whole registry off. An
// occluder must stop being fenced by ITS OWN rect without becoming immune to
// everyone else's — otherwise a piece of chrome that brackets its own draw (the
// compact toolbox) keeps hit-testing its chips normally UNDERNEATH an app-wide menu
// bar's open dropdown, which is the same click-through bug one level up.
//
// Registry order is paint order (a fence is published immediately before the
// widgets it covers are drawn), so "published earlier" means "painted under me".
func TestOverlayFenceOwnerSuspendComposes(t *testing.T) {
	c := &Ctx{}
	// An EARLIER publisher — the menu bar's open dropdown, say — overlapping the
	// LEFT half of where the later occluder will sit, so the two cases (covered by
	// someone else / covered only by myself) are both reachable in one fixture.
	earlier := sdl.Rect{X: 100, Y: 80, W: 120, H: 90}
	c.fenceOverlay(earlier)

	// The later occluder publishes its own, smaller strip and brackets its draw.
	ownMark := c.overlayFenceMark()
	c.fenceOverlay(fenceOverRect)
	if c.overlayFenceN != 2 {
		t.Fatalf("test setup: want two published fences, got %d", c.overlayFenceN)
	}

	// Cursor inside BOTH rects.
	c.mouseX, c.mouseY = 160, 110
	prev := c.pushOverlayOwner(ownMark)
	if c.hovering(fenceOverRect) {
		t.Error("an EARLIER fence must still occlude a later owner — that fence paints above it")
	}
	if !c.overlayFenced(c.mouseX, c.mouseY) {
		t.Error("the raw query must agree with hovering(): the earlier fence is still in force")
	}
	c.popOverlayOwner(prev)

	// Now the same owner where only ITS OWN fence covers the cursor: suspended.
	c.mouseX, c.mouseY = fenceOverRect.X+fenceOverRect.W-5, fenceOverRect.Y+fenceOverRect.H-5
	if pointIn(c.mouseX, c.mouseY, earlier) {
		t.Fatal("test setup: this corner must be outside the earlier fence")
	}
	if c.hovering(fenceOverRect) {
		t.Fatal("test setup: unowned, the occluder's own fence must blank it")
	}
	prev = c.pushOverlayOwner(ownMark)
	if !c.hovering(fenceOverRect) {
		t.Error("an owner must never be fenced by its OWN published rect")
	}
	c.popOverlayOwner(prev)

	// Nesting: an inner owner draws inside (above) the outer one, so it can only
	// ever ignore MORE than the outer already does — never fewer. Pushing with a
	// LATER mark must not resurrect the outer owner's own fence.
	c.mouseX, c.mouseY = 160, 110
	outer := c.pushOverlayOwner(0) // outer suspends everything
	inner := c.pushOverlayOwner(c.overlayFenceN)
	if !c.hovering(fenceOverRect) {
		t.Error("a nested owner must inherit the outer owner's suspend, not narrow it")
	}
	c.popOverlayOwner(inner)
	if !c.hovering(fenceOverRect) {
		t.Error("popping the inner owner must restore the OUTER owner's suspend, not clear it")
	}
	c.popOverlayOwner(outer)
	if c.hovering(fenceOverRect) {
		t.Error("popping the outer owner must restore the fences")
	}

	// An occluder that publishes NOTHING passes the live depth and suspends nothing:
	// it has no rect of its own, and must still yield to what paints above it.
	prev = c.pushOverlayOwner(c.overlayFenceMark())
	if c.hovering(fenceOverRect) {
		t.Error("an owner with no published rect must still be occluded by every live fence")
	}
	c.popOverlayOwner(prev)
}

// TestOverlayFenceResetsPerFrame pins the reset discipline that keeps this from
// becoming the stranded-modalOn failure mode (an un-released fence FREEZES the UI):
// BeginFrame zeroes the registry unconditionally, and within a frame a pass releases
// exactly its own publications back to a mark.
func TestOverlayFenceResetsPerFrame(t *testing.T) {
	_, cleanup := newCaptureHarness(t) // BeginFrame reads live SDL mouse/mod state
	defer cleanup()
	c := &Ctx{}

	// Pass scoping: mark → publish → release restores the previous depth exactly.
	c.fenceOverlay(sdl.Rect{X: 0, Y: 0, W: 10, H: 10}) // an outer publisher (a menu bar, say)
	mark := c.overlayFenceMark()
	c.fenceOverlay(fenceOverRect)
	if c.overlayFenceN != mark+1 {
		t.Fatalf("fenceOverlay must publish exactly one rect: depth %d, want %d", c.overlayFenceN, mark+1)
	}
	c.overlayFenceRelease(mark)
	if c.overlayFenceN != mark {
		t.Errorf("overlayFenceRelease(mark) must drop only this pass's fences: depth %d, want %d", c.overlayFenceN, mark)
	}
	c.overlayFenceRelease(mark) // stale/duplicate release: only ever shrinks, never panics
	if c.overlayFenceN != mark {
		t.Errorf("a repeated release must be inert: depth %d, want %d", c.overlayFenceN, mark)
	}

	// Cross-frame: a publisher that returns without releasing (the freeze scenario)
	// must not leak into the next frame.
	c.fenceOverlay(fenceOverRect)
	c.overlayOwner, c.overlayOwnerMark = true, 0
	c.BeginFrame(0)
	c.mouseX, c.mouseY = 160, 110 // BeginFrame reseeds the cursor from SDL; put it back
	if c.overlayFenceN != 0 {
		t.Errorf("BeginFrame must zero the fence registry, got depth %d — an unreleased fence would freeze the UI", c.overlayFenceN)
	}
	if c.overlayOwner || c.overlayOwnerMark != 0 {
		t.Errorf("BeginFrame must clear the owner suspend (on=%v mark=%d), or a panic mid-draw disables the fence forever",
			c.overlayOwner, c.overlayOwnerMark)
	}
	if !c.hovering(fenceUnderRect) {
		t.Error("last frame's fence must not survive into this frame")
	}
}

// TestOverlayFenceHasHeadroomForEveryLivePublisher pins that the cap is not exactly
// the live publisher count. A publication past the cap is dropped SILENTLY, and the
// one that loses is whoever publishes LAST — which is a courtroom-pass publisher,
// because those run inside the pass while the menu bar publishes App-level, above the
// screen dispatch. With zero headroom the next occluder anyone adds re-opens issue
// #26's click-through with no test failing.
func TestOverlayFenceHasHeadroomForEveryLivePublisher(t *testing.T) {
	// The menu bar strip, its open pane, that pane's open submenu, the toolbox strip,
	// and the sprite-preview box (#37).
	const livePublishers = 5
	if overlayFenceCap <= livePublishers {
		t.Fatalf("overlayFenceCap = %d with %d publishers that can all be up at once — no headroom, and the drop is silent",
			overlayFenceCap, livePublishers)
	}
}

// TestOverlayFenceBounded pins hard rule 4: the registry is a fixed array, so
// publishing past overlayFenceCap drops the extra rect instead of growing anything.
// The degraded behaviour is a click leaking through (today's behaviour), never a
// runaway allocation or a panic.
func TestOverlayFenceBounded(t *testing.T) {
	c := &Ctx{}
	const overshoot = 3
	for i := 0; i < overlayFenceCap+overshoot; i++ {
		c.fenceOverlay(sdl.Rect{X: int32(i) * 10, Y: 0, W: 10, H: 10})
	}
	if c.overlayFenceN != overlayFenceCap {
		t.Errorf("registry depth = %d, want the cap %d — publication must be bounded", c.overlayFenceN, overlayFenceCap)
	}
	// The drop must be COUNTED. It is invisible otherwise: the occluder still paints,
	// only the input protection under it silently disappears.
	if c.overlayFenceDrops != overshoot {
		t.Errorf("dropped-publication count = %d, want %d — an overflow that nothing records can never be noticed", c.overlayFenceDrops, overshoot)
	}
	c.overlayFenceDrops = 0
	// An empty rect paints nothing, so fencing it would only eat live clicks.
	c.overlayFenceN = 0
	c.fenceOverlay(sdl.Rect{X: 5, Y: 5, W: 0, H: 20})
	c.fenceOverlay(sdl.Rect{X: 5, Y: 5, W: 20, H: 0})
	if c.overlayFenceN != 0 {
		t.Errorf("an empty rect must not be published, got depth %d", c.overlayFenceN)
	}
}

// TestOverlayFenceZeroAlloc pins the primitive on the render hot path: publish,
// query and release run every courtroom frame, so a fixed array + count must cost
// nothing (hard rule 4 / the whole-screen gates).
func TestOverlayFenceZeroAlloc(t *testing.T) {
	c := &Ctx{}
	c.mouseX, c.mouseY = 160, 110
	cycle := func() {
		mark := c.overlayFenceMark()
		c.fenceOverlay(fenceOverRect)
		_ = c.hovering(fenceUnderRect)
		prev := c.pushOverlayOwner(mark)
		_ = c.hovering(fenceOverRect)
		c.popOverlayOwner(prev)
		c.overlayFenceRelease(mark)
	}
	cycle()
	if n := testing.AllocsPerRun(500, cycle); n != 0 {
		t.Fatalf("the overlay-fence cycle allocates %.1f/op, want 0 — it runs every frame", n)
	}
}

// TestCompactToolboxFencesWhatItCovers is the #26 wiring: with the toolbox strip
// expanded, a widget it sits on top of goes inert, while a collapsed toolbox
// publishes nothing (fencing a slim grip's whole strip would eat clicks over empty
// pixels — the mirror of the bug being fixed).
func TestCompactToolboxFencesWhatItCovers(t *testing.T) {
	a := testTabApp(t)
	a.hidden = map[string]bool{} // App init seeds this; a bare fixture must too
	const w, h = int32(1280), int32(720)
	c := a.ctx

	strip := a.compactToolboxStripRect(w, h)
	// A theme control button drawn EARLY in the pass, overlapping the strip but
	// sticking out to its left — exactly the "Call Mod under the toolbox" geometry.
	themeBtn := sdl.Rect{X: strip.X - 60, Y: strip.Y, W: strip.W + 60, H: strip.H}
	// Park the cursor on the GRIP (inside the strip, and inside the theme button):
	// one position that exercises the buried widget, the fence and the owner's own
	// hit test at once.
	grip := compactToolboxGripRect(strip)
	c.mouseX, c.mouseY = grip.X+grip.W/2, grip.Y+grip.H/2

	// Pin it open so the expanded case doesn't hinge on hover-halo geometry; the
	// genuinely-collapsed case is exercised further down.
	a.toolboxPinned = true
	if !a.compactToolboxExpanded(strip, grip) {
		t.Fatal("test setup: a pinned toolbox must read as expanded")
	}
	if !c.hovering(themeBtn) {
		t.Fatal("test setup: the cursor must start over the buried theme button")
	}

	a.fenceCompactToolbox(w, h)
	if c.overlayFenceN != 1 {
		t.Fatalf("an expanded toolbox must publish exactly one fence, got depth %d", c.overlayFenceN)
	}
	if c.hovering(themeBtn) {
		t.Error("a theme button under the expanded toolbox strip must be inert (#26)")
	}
	// The toolbox's own chips still work: drawCompactToolbox brackets itself with
	// the mark the latch recorded (the depth from just before its own publication).
	prev := c.pushOverlayOwner(a.toolboxFence.mark)
	if !c.hovering(grip) {
		t.Error("the toolbox must still hit-test its own grip inside pushOverlayOwner")
	}
	c.popOverlayOwner(prev)
	// The latch the draw replays must describe exactly what was published.
	if !a.toolboxFence.set || !a.toolboxFence.draws || !a.toolboxFence.expanded {
		t.Errorf("latch must say the expanded strip paints in-pass: %+v", a.toolboxFence)
	}
	if a.toolboxFence.strip != strip {
		t.Errorf("latch strip %+v must be the rect that was fenced and will be drawn (%+v)", a.toolboxFence.strip, strip)
	}
	// Left of the strip the same button is still live.
	c.mouseX = strip.X - 30
	if !c.hovering(themeBtn) {
		t.Error("the part of the theme button that is NOT covered must stay clickable")
	}

	// Collapsed: cursor parked far away, nothing pinned → publish nothing.
	c.overlayFenceN = 0
	a.toolboxPinned = false
	c.mouseX, c.mouseY = 10, 10
	a.fenceCompactToolbox(w, h)
	if c.overlayFenceN != 0 {
		t.Errorf("a collapsed toolbox must publish no fence, got depth %d — it would blank the bottom-right corner", c.overlayFenceN)
	}

	// Hidden via panelToolbox: drawCompactToolbox early-returns, so must the fence.
	a.toolboxPinned = true
	a.hidden[panelToolbox] = true
	a.fenceCompactToolbox(w, h)
	if c.overlayFenceN != 0 {
		t.Errorf("a hidden toolbox must publish no fence, got depth %d", c.overlayFenceN)
	}
	// A layout editor owns the pointer itself (modalOn) and the toolbox draws
	// post-courtroom there, so the in-pass fence must stand down.
	delete(a.hidden, panelToolbox)
	a.classicEdit = true
	a.fenceCompactToolbox(w, h)
	if c.overlayFenceN != 0 {
		t.Errorf("no in-pass fence while a layout editor is armed, got depth %d", c.overlayFenceN)
	}
	if a.toolboxFence.set {
		t.Error("with an editor armed the POST-courtroom site owns the draw — the in-pass latch must stay unset")
	}
}

// TestCompactToolboxNoFenceUnderCourtroomModal is the draw-vs-fence agreement
// regression. The fence is published at the TOP of the courtroom pass but the
// toolbox paints at the BOTTOM, and drawCourtroomModals returns the pass in
// between: with a modal open and the toolbox pinned, a ~122×22 band of that modal
// used to go click-dead over pixels nothing had painted — precisely what the
// primitive's own header forbids ("publish the rect that is actually PAINTED").
func TestCompactToolboxNoFenceUnderCourtroomModal(t *testing.T) {
	const w, h = int32(1280), int32(720)
	// Every modal in the shared table, so a newly added one can't quietly reopen the
	// hole. Each flag is set on a fresh App: the switch is first-match-wins, and a
	// stale flag would make later cases unreachable.
	for i := range courtroomModals {
		a := testTabApp(t)
		a.hidden = map[string]bool{}
		a.toolboxPinned = true // expanded, so a fence WOULD be published but for the modal
		if !courtroomModals[i].open(a) {
			// Arm this modal via its own flag. The table holds only the predicate, so
			// set the flags directly through the same fields the predicate reads.
			switch i {
			case 0:
				a.showIni = true
			case 1:
				a.bgPick.show = true
			case 2:
				a.showSfxBrowser = true
			default:
				t.Fatalf("courtroomModals grew to %d entries — extend this switch", len(courtroomModals))
			}
		}
		if !a.courtroomModalUp() {
			t.Fatalf("modal %d: the predicate must see the flag it was built from", i)
		}
		a.fenceCompactToolbox(w, h)
		if a.ctx.overlayFenceN != 0 {
			t.Errorf("modal %d: the pass returns before the toolbox draws, so it must publish no fence (depth %d)",
				i, a.ctx.overlayFenceN)
		}
		if a.toolboxFence.draws {
			t.Errorf("modal %d: the latch must record that the toolbox does NOT paint this frame", i)
		}
	}
}

// TestCompactToolboxDrawsFromExactlyOneSite pins the structural replacement for
// the two `c.clicked = false` consumes that wave 1 put in the chip loop. The
// toolbox has two draw sites — in-pass (courtroom) and post-courtroom (over a
// layout editor) — and handleHotkeys can arm the editor from INSIDE the pass, so
// both could fire on the same frame. Drawing twice hit-tests every chip twice on
// one press, which for the Pin chip means toggling straight back off. The latch is
// what makes them mutually exclusive; without it a consume was papering over it.
func TestCompactToolboxDrawsFromExactlyOneSite(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx = ctx
	a.hidden = map[string]bool{}
	a.uiScalePct = 100
	const w, h = int32(1280), int32(720)

	// Park the cursor on the FIRST chip (Pin), which fires in edit mode too — the
	// grip is the editor's drag handle there and deliberately doesn't toggle.
	strip := a.compactToolboxStripRect(w, h)
	grip := compactToolboxGripRect(strip)
	pinChip := sdl.Rect{X: grip.X - compactChipH - 4, Y: grip.Y, W: compactChipH, H: compactChipH}
	ctx.mouseX, ctx.mouseY = pinChip.X+pinChip.W/2, pinChip.Y+pinChip.H/2
	ctx.downX, ctx.downY = ctx.mouseX, ctx.mouseY
	a.classicEdit = true // the post-courtroom site's real context

	// Control: with no in-pass latch, the post-courtroom site owns the draw and the
	// chip fires. (Proves the assertion below isn't vacuous.)
	a.toolboxFence = compactToolboxLatch{}
	ctx.clicked = true
	a.drawCompactToolboxOverEditor(w, h)
	if !a.toolboxPinned {
		t.Fatal("test setup: with no in-pass latch the post-courtroom site must draw and its Pin chip must fire")
	}

	// The real case: the in-pass site already drew this frame (a hotkey armed the
	// editor mid-pass). The post-courtroom site must stand down entirely.
	a.toolboxFence = compactToolboxLatch{set: true, draws: true, expanded: true, strip: strip}
	ctx.clicked = true
	a.drawCompactToolboxOverEditor(w, h)
	if !a.toolboxPinned {
		t.Error("the toolbox drew twice on one frame: the second pass re-fired the Pin chip and unpinned it")
	}
}

// TestCourtroomModalUpMatchesDraw pins that the predicate and the draw are the
// same list — the whole reason courtroomModals is a table. A modal added to the
// draw but not the predicate is the dead-band bug above; one added to the
// predicate but not the draw blanks the courtroom with nothing on screen.
func TestCourtroomModalUpMatchesDraw(t *testing.T) {
	a := testTabApp(t)
	if a.courtroomModalUp() {
		t.Fatal("a fresh App has no modal open")
	}
	for i := range courtroomModals {
		if courtroomModals[i].open == nil || courtroomModals[i].draw == nil {
			t.Fatalf("entry %d must carry BOTH the flag and the draw", i)
		}
	}
	// blockingCourtPopup is a DIFFERENT question and must not be mistaken for this
	// one: it omits the SFX browser and adds classicEdit. Pinned so a future "just
	// reuse it" refactor has to confront that.
	a.showSfxBrowser = true
	if !a.courtroomModalUp() {
		t.Error("the SFX browser IS in the return-to-top modal set (drawCourtroomModals draws it)")
	}
	if a.blockingCourtPopup() {
		t.Error("blockingCourtPopup deliberately does NOT list the SFX browser — do not reuse it as the modal predicate")
	}
	a.showSfxBrowser = false
	a.classicEdit = true
	if a.courtroomModalUp() {
		t.Error("the layout editor is not a return-to-top modal, though blockingCourtPopup counts it")
	}
}
