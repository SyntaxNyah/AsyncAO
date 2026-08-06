package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// The server-tab strip inside the THEMED layout editor (#14).
//
// The strip is intrinsically sized — its width comes from the chips, so it changes as
// tabs open, close and get renamed — but it was wired into the theme-rect system as if
// it carried an authored W/H. Everything below drives the REAL editor rather than its
// arithmetic, because the arithmetic was never wrong: `base + delta` was satisfied
// perfectly by a drag that teleported the widget first.

// stripEditFixture is a themed courtroom with a real Ctx, the themed editor armed and
// snap off, ready to be driven by cursor coordinates. Snap off is not a convenience:
// the grid rounds to 8 design px, which would swamp the one-pixel assertions.
func stripEditFixture(t *testing.T) (*App, *Ctx, func()) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	ctx, err := NewCtx(ren)
	if err != nil {
		cleanup()
		t.Skipf("Ctx unavailable: %v", err)
	}
	a := stageTabbarlessCourtroom(t, stripEditTheme)
	a.ctx = ctx
	a.uiScalePct = 100
	a.layoutEdit = true
	a.layoutSnap = false
	return a, ctx, cleanup
}

const (
	stripEditTheme = "stocklike"
	// The fixture window. 714 is the stock AO2 canvas width, so the layout scale is
	// exactly 1 and screen deltas equal design deltas — which is what lets a
	// one-pixel assertion be a one-pixel assertion.
	stripEditW = int32(714)
	stripEditH = int32(760)
)

// dragStrip runs a press → move → release on the strip's editor box and returns the
// box before and after. Every call goes through drawLayoutEditor, so anything the real
// editor does to the key (grab, snap, clamp, persist) is in the result.
func dragStrip(t *testing.T, a *App, ctx *Ctx, dx, dy int32) (before, after sdl.Rect) {
	t.Helper()
	lay := a.themeWindowLayout(stripEditW, stripEditH)
	before, ok := lay.rect(themeTabBarKey)
	if !ok {
		t.Fatal("the themed editor must be handed a box for the strip")
	}
	ctx.mouseX, ctx.mouseY = before.X+before.W/2, before.Y+before.H/2
	ctx.mouseDown, a.editPrev = true, false
	a.drawLayoutEditor(stripEditW, stripEditH, lay)
	if a.editTgt.designKey() != themeTabBarKey || a.editDrag != 1 {
		t.Fatalf("a press on the strip's box must grab it for a MOVE, got key=%q drag=%d", a.editTgt.designKey(), a.editDrag)
	}
	ctx.mouseX, ctx.mouseY = ctx.mouseX+dx, ctx.mouseY+dy
	a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))
	ctx.mouseDown = false
	a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))
	after, ok = a.themeWindowLayout(stripEditW, stripEditH).rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip lost its box during the drag")
	}
	return before, after
}

// TestThemedEditorOnePixelDragMovesTheStripOnePixel is the assertion the previous pass
// missed. Its box came from the LIVE docked rect but its drag origin came from the
// stored design SEED, so the first pixel of movement snapped the widget from one to
// the other: measured at 714x760 on the stock 714x579 canvas with snap off, the box
// went {317,22,79,22} → {238,112,240,22} — 79 px left, 90 px down, and 79 → 240 px
// wide — before the user had moved anything.
func TestThemedEditorOnePixelDragMovesTheStripOnePixel(t *testing.T) {
	a, ctx, cleanup := stripEditFixture(t)
	defer cleanup()

	before, after := dragStrip(t, a, ctx, 1, 1)
	want := sdl.Rect{X: before.X + 1, Y: before.Y + 1, W: before.W, H: before.H}
	if after != want {
		t.Fatalf("one pixel of cursor travel moved the strip's box %+v → %+v, want %+v (no teleport, no resize)",
			before, after, want)
	}
	// The strip is really painted there — the box is not describing a widget that
	// stayed behind.
	rects, _ := a.tabBarRects(stripEditW, stripEditH)
	if len(rects) == 0 || rects[0].X != after.X || rects[0].Y != after.Y {
		t.Fatalf("the strip paints at %+v, want the box's origin (%d,%d)", rects, after.X, after.Y)
	}
}

// TestThemedEditorSnappedDragKeepsTheStripInTheChromeBand is the same gesture with the
// shipped default (snap ON). The strip's home is the chrome band ABOVE the design
// canvas, so its design origin is legitimately negative; snapDesign's floor-at-zero
// would round that to the canvas top and re-create the 90 px jump on the first snapped
// pixel, with the grid as the alibi.
func TestThemedEditorSnappedDragKeepsTheStripInTheChromeBand(t *testing.T) {
	a, ctx, cleanup := stripEditFixture(t)
	defer cleanup()
	a.layoutSnap = true

	before, after := dragStrip(t, a, ctx, 1, 1)
	if d := after.Y - before.Y; d < -layoutGridDesign || d > layoutGridDesign {
		t.Fatalf("a snapped one-pixel drag moved the strip %d px vertically (%+v → %+v), want at most one grid step (%d)",
			d, before, after, layoutGridDesign)
	}
	if after.W != before.W || after.H != before.H {
		t.Fatalf("a snapped MOVE resized the strip %+v → %+v", before, after)
	}
}

// TestThemedEditorBoxIsAlwaysThePaintedTabStrip is the "box == widget" invariant, in
// the three states that used to break it. The editor's drag box, its Tab-cycle
// stacking order (sorted by box AREA), its resize-grip probe and its right-click reset
// all read this one rect, so a box the strip has outgrown mis-targets every one of
// them — and a box that is not on the widget is worse than no box.
func TestThemedEditorBoxIsAlwaysThePaintedTabStrip(t *testing.T) {
	a, ctx, cleanup := stripEditFixture(t)
	defer cleanup()

	check := func(stage string) sdl.Rect {
		t.Helper()
		box, ok := a.themeWindowLayout(stripEditW, stripEditH).rect(themeTabBarKey)
		if !ok {
			t.Fatalf("%s: the strip has no editor box", stage)
		}
		rects, add := a.tabBarRects(stripEditW, stripEditH)
		if len(rects) == 0 {
			t.Fatalf("%s: fixture has no chips", stage)
		}
		painted := sdl.Rect{X: rects[0].X, Y: rects[0].Y, W: a.tabStripTotalW(), H: tabBarH}
		if box != painted {
			t.Errorf("%s: the editor's box is %+v but the strip paints %+v (chips %+v, add %+v)",
				stage, box, painted, rects, add)
		}
		return box
	}

	docked := check("docked, untouched")

	// A SECOND session: the strip's intrinsic width changes with no window, band, fit
	// or theme change to invalidate the layout cache on.
	a.tabs = append(a.tabs, &courtTab{state: sessionState{serverName: "second server"}})
	wider := check("docked, after a tab was added")
	if wider.W == docked.W {
		t.Fatalf("fixture: adding a tab must change the strip's intrinsic width (still %d)", wider.W)
	}

	// Parked: the drag moves it onto the canvas, and the box must follow it there.
	dragStrip(t, a, ctx, 40, 300)
	if !a.tabStripThemeParked() {
		t.Fatal("a dragged strip must read as parked")
	}
	parked := check("parked")
	if parked.Y == wider.Y {
		t.Fatalf("fixture: the drag must have left the chrome band (still Y=%d)", parked.Y)
	}

	// A THIRD session, now that it is parked: same invariant, other branch.
	a.tabs = append(a.tabs, &courtTab{state: sessionState{serverName: "third server"}})
	check("parked, after another tab was added")
}

// TestThemedEditorBoxFollowsAClassicSlotOverride is the unparked branch's other arm. A
// slotTabBar override can only be MADE in the default-layout editor, but it outlives a
// switch to a theme — and it also zeroes the chrome band, so the strip genuinely floats
// where it was left. The themed editor's box has to follow it there; handing out the
// docked rect would put the box on empty chrome.
func TestThemedEditorBoxFollowsAClassicSlotOverride(t *testing.T) {
	a, _, cleanup := stripEditFixture(t)
	defer cleanup()

	docked, ok := a.themeWindowLayout(stripEditW, stripEditH).rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip has no editor box")
	}
	// Persisted and reloaded the way the classic editor's drag-end does it.
	a.d.Prefs.SetClassicSlot(slotTabBar, [4]float64{0.6, 0.7, 0.2, 0.05})
	a.classicOvLoaded = false
	a.themeLay.valid = false

	box, ok := a.themeWindowLayout(stripEditW, stripEditH).rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip lost its box under a classic override")
	}
	rects, _ := a.tabBarRects(stripEditW, stripEditH)
	painted := sdl.Rect{X: rects[0].X, Y: rects[0].Y, W: a.tabStripTotalW(), H: tabBarH}
	if painted == docked {
		t.Fatalf("fixture: the override must actually move the painted strip (still %+v)", docked)
	}
	if box != painted {
		t.Errorf("the editor's box is %+v but the strip paints %+v (it was docked at %+v)", box, painted, docked)
	}
}

// TestThemedEditorHonoursADragOntoThePristineSeedPosition pins the third defect. Parked
// -ness used to be inferred by comparing the live design rect against the pristine
// seed, so a drag that happened to land on the seed's own coordinates wrote its
// override and was then read back as "never moved": the strip snapped straight back
// into the chrome band, silently discarding the placement.
//
// The fixture moves the PRISTINE seed to where the drag will land, which is the same
// coincidence with fewer moving parts than hunting for a chip width that reproduces it.
func TestThemedEditorHonoursADragOntoThePristineSeedPosition(t *testing.T) {
	a, ctx, cleanup := stripEditFixture(t)
	defer cleanup()

	// Where a (40, 300) drag from the docked box lands, in design space.
	lay := a.themeWindowLayout(stripEditW, stripEditH)
	box, _ := lay.rect(themeTabBarKey)
	landing := theme.Rect{
		X: int(box.X+40) - int(lay.offX),
		Y: int(box.Y+300) - int(lay.offY),
		W: int(box.W), H: int(box.H),
	}
	a.themeRectsOrig[themeTabBarKey] = landing

	_, after := dragStrip(t, a, ctx, 40, 300)
	if got := a.themeRects[themeTabBarKey]; got != landing {
		t.Fatalf("fixture: the drag landed at %+v, want the pristine %+v — the coincidence is not being tested", got, landing)
	}
	if !a.tabStripThemeParked() {
		t.Fatal("a drag that lands on the pristine seed is still a placement, but the strip read as un-parked")
	}
	if want := (sdl.Rect{X: box.X + 40, Y: box.Y + 300, W: box.W, H: box.H}); after != want {
		t.Fatalf("the strip is at %+v, want %+v — landing on the seed must not snap it back into the chrome band", after, want)
	}
	if _, ok := a.d.Prefs.ThemeRectOverrides(stripEditTheme)[themeTabBarKey]; !ok {
		t.Fatal("the placement was not persisted, so it would be lost on the next theme load")
	}
}

// TestThemedEditorRefusesToResizeTheTabStrip: the strip has no authored size to
// resize — its W/H come from the chips — so a corner grip would either do nothing or
// smear it. The classic layout system already registers the SAME widget move-only, and
// the two editors must agree.
func TestThemedEditorRefusesToResizeTheTabStrip(t *testing.T) {
	if slotResizable(slotTabBar) {
		t.Fatal("fixture: the classic slot is supposed to be move-only — the two editors must agree")
	}
	a, ctx, cleanup := stripEditFixture(t)
	defer cleanup()

	lay := a.themeWindowLayout(stripEditW, stripEditH)
	box, ok := lay.rect(themeTabBarKey)
	if !ok {
		t.Fatal("the themed editor must be handed a box for the strip")
	}
	// Press dead on the bottom-right corner grip.
	ctx.mouseX, ctx.mouseY = box.X+box.W-1, box.Y+box.H-1
	ctx.mouseDown, a.editPrev = true, false
	a.drawLayoutEditor(stripEditW, stripEditH, lay)
	if a.editDrag == 2 {
		t.Fatal("the corner grip started a RESIZE on the server-tab strip, which has no authored size")
	}
	if a.editTgt.designKey() != themeTabBarKey || a.editDrag != 1 {
		t.Fatalf("the same press must still MOVE the strip, got key=%q drag=%d", a.editTgt.designKey(), a.editDrag)
	}
	// And dragging from that corner moves it without changing its size.
	ctx.mouseX, ctx.mouseY = ctx.mouseX+30, ctx.mouseY+120
	a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))
	ctx.mouseDown = false
	a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))
	after, _ := a.themeWindowLayout(stripEditW, stripEditH).rect(themeTabBarKey)
	if after.W != box.W || after.H != box.H {
		t.Fatalf("a drag from the grip resized the strip %dx%d → %dx%d", box.W, box.H, after.W, after.H)
	}
	if after.X != box.X+30 || after.Y != box.Y+120 {
		t.Fatalf("a drag from the grip moved the strip to (%d,%d), want (%d,%d)", after.X, after.Y, box.X+30, box.Y+120)
	}
}

// TestTabStripResetPathsUnparkTheStrip: right-click reset and "Reset all" both delete
// the per-key override, and that override IS the placement flag — so both must send the
// strip home to the chrome band, at the docked size, not leave a 240 px ghost box on
// the canvas.
func TestTabStripResetPathsUnparkTheStrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reset func(*App, *Ctx)
	}{
		{"right-click reset", func(a *App, ctx *Ctx) {
			box, _ := a.themeWindowLayout(stripEditW, stripEditH).rect(themeTabBarKey)
			ctx.mouseX, ctx.mouseY = box.X+box.W/2, box.Y+box.H/2
			ctx.rightClicked = true
			a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))
			ctx.rightClicked = false
		}},
		{"reset all", func(a *App, ctx *Ctx) {
			// The banner's "Reset all" chip, hit the way the editor hit-tests it.
			ctx.mouseX, ctx.mouseY = stripEditW-70-pad-96+45, 12
			ctx.clicked = true
			a.drawLayoutEditor(stripEditW, stripEditH, a.themeWindowLayout(stripEditW, stripEditH))
			ctx.clicked = false
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, ctx, cleanup := stripEditFixture(t)
			defer cleanup()
			docked, parked := dragStrip(t, a, ctx, 40, 300)
			if !a.tabStripThemeParked() {
				t.Fatal("fixture: the drag did not park the strip")
			}
			tc.reset(a, ctx)
			if a.tabStripThemeParked() {
				t.Fatal("the reset left the strip parked — its override is its placement flag")
			}
			back, ok := a.themeWindowLayout(stripEditW, stripEditH).rect(themeTabBarKey)
			if !ok {
				t.Fatal("the strip lost its box after the reset")
			}
			if back != docked {
				t.Fatalf("after the reset the box is %+v (it was parked at %+v), want the docked %+v", back, parked, docked)
			}
			rects, _ := a.tabBarRects(stripEditW, stripEditH)
			if len(rects) == 0 || rects[0].X != back.X || rects[0].Y != back.Y {
				t.Fatalf("the strip paints at %+v, want the docked box's origin (%d,%d)", rects, back.X, back.Y)
			}
		})
	}
}

// TestTabStripPlacementSurvivesAnUnrelatedUndo. Parked-ness is recorded, not derived,
// so the undo stack has to carry it: restoring rects alone would re-derive it from
// geometry through restoreLayout's "back at its original rect ⇒ drop the override"
// rule, and un-park a strip the user placed on its seed's coordinates.
func TestTabStripPlacementSurvivesAnUnrelatedUndo(t *testing.T) {
	a, ctx, cleanup := stripEditFixture(t)
	defer cleanup()

	lay := a.themeWindowLayout(stripEditW, stripEditH)
	box, _ := lay.rect(themeTabBarKey)
	a.themeRectsOrig[themeTabBarKey] = theme.Rect{
		X: int(box.X+40) - int(lay.offX),
		Y: int(box.Y+300) - int(lay.offY),
		W: int(box.W), H: int(box.H),
	}
	dragStrip(t, a, ctx, 40, 300) // lands exactly on the (rewritten) pristine seed
	if !a.tabStripThemeParked() {
		t.Fatal("fixture: the placement did not take")
	}
	placed := a.themeRects[themeTabBarKey]

	// An unrelated edit, then undo it. restoreLayout walks every key, so the strip
	// passes through its rect-equals-original branch on the way.
	a.pushLayoutUndo()
	a.themeRects["viewport"] = theme.Rect{X: 10, Y: 10, W: 200, H: 150}
	a.layoutEditUndo()

	if !a.tabStripThemeParked() {
		t.Fatal("undoing an unrelated edit un-parked the server-tab strip")
	}
	if got := a.themeRects[themeTabBarKey]; got != placed {
		t.Fatalf("the strip's design rect drifted through the undo: %+v, want %+v", got, placed)
	}
}

// TestTabStripUndoOfTheFirstDragSendsItHome is the OTHER half of the parked-flag
// contract, and the half the path that actually produces snapshots gets wrong.
//
// TestTabStripPlacementSurvivesAnUnrelatedUndo above pushes its snapshot by hand, with
// no drag in flight, and asserts a placed strip STAYS placed. It therefore cannot see
// the press site's ordering: drawLayoutEditor assigns editTgt/editDrag and only THEN
// calls pushLayoutUndo, so the snapshot of the PRE-PRESS state is taken while
// tabStripThemeParked's "a placement is happening right now" arm is already true. An
// un-parked, docked strip is recorded as parked=true, and restoreLayout's parked arm
// then WRITES the override — which is the placement flag — instead of clearing it.
//
// Measured at Native 714x760 with snap off before the fix: docked {317 22 79 22},
// drag (+40,+300) → {357 322 79 22}, one undo step with parked=TRUE, and after
// layoutEditUndo the strip read parked with its box back at the pristine SEED's canvas
// spot {237 112 79 22} — not the docked box the press started from.
func TestTabStripUndoOfTheFirstDragSendsItHome(t *testing.T) {
	a, ctx, cleanup := stripEditFixture(t)
	defer cleanup()

	docked, moved := dragStrip(t, a, ctx, 40, 300)
	if moved == docked {
		t.Fatalf("fixture: the drag did not move the strip (still %+v)", docked)
	}
	if !a.tabStripThemeParked() {
		t.Fatal("fixture: the drag did not park the strip")
	}
	if len(a.editUndo) != 1 {
		t.Fatalf("fixture: a press-move-release must leave exactly one undo step, got %d", len(a.editUndo))
	}
	if a.editUndo[0].parked {
		t.Error("the pre-press snapshot recorded parked=true, but the strip was docked in the chrome band when the press landed")
	}

	a.layoutEditUndo()

	if a.tabStripThemeParked() {
		t.Error("undoing the first drag left the strip parked — the undo re-placed it instead of sending it home")
	}
	back, ok := a.themeWindowLayout(stripEditW, stripEditH).rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip lost its box through the undo")
	}
	if back != docked {
		t.Fatalf("after the undo the strip's box is %+v (it was dragged to %+v), want the pre-press docked %+v",
			back, moved, docked)
	}
	rects, _ := a.tabBarRects(stripEditW, stripEditH)
	if len(rects) == 0 || rects[0].X != docked.X || rects[0].Y != docked.Y {
		t.Fatalf("the strip paints at %+v, want the pre-press docked origin (%d,%d)", rects, docked.X, docked.Y)
	}
}

// --- which of the two strip paint sites owns the frame -------------------------

// TestTabStripPaintSiteIsExclusiveWhileEditing pins the gate that decides between the
// strip's two draw sites: App.Frame's over-everything paint (after the screen dispatch)
// and the in-pass one under a layout editor's overlay. Exactly one may run per frame —
// running both paints the strip twice, running neither loses it.
//
// The over-everything site used to be gated on classicEdit alone, so with the THEMED
// editor armed the strip painted AFTER drawLayoutEditor and buried its banner. The
// predicate must not simply be "an editor is armed" either: an editor flag outlives
// the frames where its draw site actually runs (drawCourtroom bails to the lobby with
// no room, returns early in theater, and gif export / replay / the scene maker preempt
// the whole screen dispatch), and the strip would then vanish on those frames.
func TestTabStripPaintSiteIsExclusiveWhileEditing(t *testing.T) {
	arm := map[string]func(*App){
		"themed editor":  func(a *App) { a.layoutEdit = true },
		"classic editor": func(a *App) { a.classicEdit = true },
	}
	for editor, armEditor := range arm {
		t.Run(editor, func(t *testing.T) {
			base := func() *App {
				a := testTabApp(t)
				a.tabs = []*courtTab{{}}
				a.activeTab = 0
				a.screen = ScreenCourtroom
				a.room = newRoomForTest(t)
				a.sess = &courtroom.Session{}
				armEditor(a)
				return a
			}
			if a := base(); !a.tabStripPaintsUnderEditor() {
				t.Fatal("on a live courtroom the editor's own pass must paint the strip, or its banner is buried")
			}
			for name, break_ := range map[string]func(*App){
				"lobby":       func(a *App) { a.screen = ScreenLobby },
				"no room":     func(a *App) { a.room = nil },
				"no session":  func(a *App) { a.sess = nil },
				"theater":     func(a *App) { a.theaterOn = true },
				"gif export":  func(a *App) { a.gifExporting = true },
				"replay":      func(a *App) { a.replaying, a.replayRoom = true, a.room },
				"scene maker": func(a *App) { a.makerOpen = true },
			} {
				a := base()
				break_(a)
				if a.tabStripPaintsUnderEditor() {
					t.Errorf("%s: the editor's draw site does not run there, so the strip would never be painted", name)
				}
			}
			// And with no editor armed the in-pass site must stay closed.
			a := testTabApp(t)
			a.tabs = []*courtTab{{}}
			a.activeTab = 0
			a.screen = ScreenCourtroom
			a.room = newRoomForTest(t)
			a.sess = &courtroom.Session{}
			if a.tabStripPaintsUnderEditor() {
				t.Error("with no editor armed the strip belongs to App.Frame's over-everything paint")
			}
		})
	}
}

// readRegion copies one rectangle of the current render target back into Go memory.
func readRegion(t *testing.T, ren *sdl.Renderer, r sdl.Rect) []byte {
	t.Helper()
	surf, err := sdl.CreateRGBSurfaceWithFormat(0, r.W, r.H, 32, uint32(sdl.PIXELFORMAT_ABGR8888))
	if err != nil {
		t.Skipf("surface unavailable: %v", err)
	}
	defer surf.Free()
	if err := ren.ReadPixels(&r, surf.Format.Format, surf.Data(), int(surf.Pitch)); err != nil {
		t.Skipf("ReadPixels unavailable: %v", err)
	}
	out := make([]byte, len(surf.Pixels()))
	copy(out, surf.Pixels())
	return out
}

func sameRegion(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// textCacheHasLabel reports whether a string was rasterized into the label cache since
// the last purge — the cheapest honest way to ask "did that widget draw this frame?"
// for one whose own paint is alpha-blended (so a pixel comparison cannot tell one
// paint from two) and whose tooltip is suppressed by the editor's input fence.
func textCacheHasLabel(c *Ctx, text string) bool {
	for k := range c.textCache {
		if k.text == text {
			return true
		}
	}
	return false
}

// TestLayoutEditorBannerSurvivesTheTabStrip is the pixel proof, per editor. The docked
// strip owns the window's Y=22..44 band, which is exactly where BOTH editors put their
// banner (Y=0..26) and their Done / Reset all / Snap / Magnet / Profile row (Y=2..24):
// measured at 1152x864 with one session, a chip at {545,22,28,22} buried 4 px of banner
// and the bottom 2 px of every chip, and a strip wide enough marches into the Done
// cluster itself.
//
// Rendering the SAME frame twice, differing only in whether the strip is painted after
// the editor, is what makes this exact: if the two agree, the strip's chips never reach
// those pixels and the test is vacuous, so the assertion is that they DIFFER — the
// strip does land there, and production is not the order that puts it on top.
func TestLayoutEditorBannerSurvivesTheTabStrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		arm  func(*App)
		done func(w int32) sdl.Rect
	}{
		{
			name: "themed editor",
			arm: func(a *App) {
				a.d.Prefs.SetTheme(stripEditTheme, "")
				a.layoutEdit, a.layoutSnap = true, false
			},
			// Mirrors drawLayoutEditor's own banner geometry.
			done: func(w int32) sdl.Rect { return sdl.Rect{X: w - 70 - pad, Y: 2, W: 70, H: 22} },
		},
		{
			name: "classic editor",
			arm: func(a *App) {
				a.d.Prefs.SetThemeLayout(false) // send drawCourtroom down the classic branch
				a.classicEdit = true
			},
			// Mirrors drawClassicEditor's own banner geometry.
			done: func(w int32) sdl.Rect {
				return sdl.Rect{X: w - 62 - pad, Y: 2, W: 62, H: classicBannerH - 4}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, cleanup := stageThemedCourtroom(t)
			defer cleanup()
			ren := a.ctx.Ren
			w, h, err := ren.GetOutputSize()
			if err != nil {
				t.Skipf("output size unavailable: %v", err)
			}
			// Enough sessions that the centred strip reaches the Done chip.
			a.serverName = "first session"
			a.tabs = []*courtTab{
				{},
				{state: sessionState{serverName: "second session"}},
				{state: sessionState{serverName: "third session"}},
			}
			a.activeTab = 0
			tc.arm(a)

			banner := sdl.Rect{X: 0, Y: 0, W: w, H: layoutBannerH}
			doneBtn := tc.done(w)
			// The collision has to be real, or everything below proves nothing.
			rects, _ := a.tabBarRects(w, h)
			if len(rects) == 0 {
				t.Fatal("fixture: no chips")
			}
			strip := sdl.Rect{X: rects[0].X, Y: rects[0].Y, W: a.tabStripTotalW(), H: tabBarH}
			if _, hits := strip.Intersect(&banner); !hits {
				t.Fatalf("fixture: the docked strip %+v does not reach the editor banner %+v", strip, banner)
			}
			if _, hits := strip.Intersect(&doneBtn); !hits {
				t.Fatalf("fixture: the docked strip %+v does not reach the Done chip %+v — widen it or this proves nothing", strip, doneBtn)
			}

			render := func(stripOnTop bool) (bannerPix, donePix []byte) {
				_ = ren.SetDrawColor(0, 0, 0, 255)
				_ = ren.Clear()
				a.drawCourtroom(w, h)
				if stripOnTop {
					a.drawTabBar(w, h) // the pre-fix layering: App.Frame's site, after the editor
				}
				return readRegion(t, ren, banner), readRegion(t, ren, doneBtn)
			}
			// Determinism first: two identical renders must agree, or a difference
			// below would mean nothing.
			b1, d1 := render(false)
			b2, d2 := render(false)
			if !sameRegion(b1, b2) || !sameRegion(d1, d2) {
				t.Skip("the fixture frame is not deterministic on this renderer")
			}
			bOver, dOver := render(true)
			if sameRegion(b1, bOver) {
				t.Error("painting the strip after the editor changes nothing in the banner — the strip is already on top of it")
			}
			if sameRegion(d1, dOver) {
				t.Error("painting the strip after the editor changes nothing over Done — Done is already buried")
			}
			// …and the strip is not simply MISSING. Its chip labels are rasterized
			// nowhere else, so a fresh cache that holds one is proof the courtroom pass
			// painted the strip (underneath, per the two assertions above) rather than
			// dropping it — the other way this gate could go wrong.
			a.ctx.purgeTextCache()
			_ = ren.SetDrawColor(0, 0, 0, 255)
			_ = ren.Clear()
			a.drawCourtroom(w, h)
			if label := a.tabChipLabel(1); !textCacheHasLabel(a.ctx, label) {
				t.Errorf("the courtroom pass never painted the strip: no chip label %q was rasterized", label)
			}
		})
	}
}
