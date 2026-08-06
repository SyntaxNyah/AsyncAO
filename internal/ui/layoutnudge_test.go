package ui

// Arrow-key nudging in the two layout editors.
//
// Everything here drives the REAL router (editorNudgeKeys) off a real ctx key field
// rather than calling the arms directly, because the routing is half the feature: a
// plain arrow arrives on c.keyPressed — the field a focused text field's caret, the
// IC/OOC recall rings and the pair-ghost offset all read — and a Ctrl+arrow arrives on
// c.hotkey, where a user-rebound action would otherwise pick it up.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// nudgeWidgetKey is the themed key the tests move. It is a real courtroom_design.ini
// element name so nothing about the fixture is special-cased, and it is deliberately a
// PAINTED, non-fixed themeSlots row — themeKeyEditable("emotes") is true.
const nudgeWidgetKey = "emotes"

// nudgeCanvas is the stock AO2 design canvas the clamp measures against.
var nudgeCanvas = theme.Rect{X: 0, Y: 0, W: 714, H: 579}

// TestRegSlotRefusesAnUnnamedSlot pins the classic editor's registry guard.
//
// "" is the editor's own spelling of "nothing selected" (classicEditKey, and the
// reason editTarget carries an explicit space field), so an unnamed registration
// puts a hoverable, draggable box in slotReg that compares EQUAL to the unselected
// state — and its release would call config.SetClassicSlot(""), which keys the
// persisted layout by name. Only a caller passing a COMPUTED key can reach it, which
// is exactly what W7's element work will be doing.
func TestRegSlotRefusesAnUnnamedSlot(t *testing.T) {
	a := testTabApp(t)
	r := sdl.Rect{X: 10, Y: 20, W: 30, H: 40}
	a.regSlot("", r, r)
	if _, present := a.slotReg[""]; present {
		t.Fatal("regSlot registered an unnamed slot — it is indistinguishable from \"nothing " +
			"selected\", and persisting it writes a classic-layout entry keyed by the empty string")
	}
	// A real name still registers, so the guard is a filter and not a stand-down.
	a.regSlot(slotOOC, r, r)
	if got, present := a.slotReg[slotOOC]; !present || got.cur != r {
		t.Fatalf("a named slot did not register: %+v (present=%v)", got, present)
	}
}

// TestOversizeRectIsMovedNotShrunk pins clampDesignRectToCanvas' documented
// translate-vs-limit choice, which had no test at all and reads as a bug at the call
// site: a widget WIDER than the canvas comes back at a NEGATIVE X.
//
// It is deliberate — the clamp moves boxes and the resize gesture sizes them, so a
// move that silently changed a theme's authored width would be unrecoverable — but a
// deliberate surprise with no gate is one refactor away from being "fixed" into a
// silent resize. This states the whole shape, including the two ordinary arms, so the
// oversize row cannot be read as an accident of the other two.
func TestOversizeRectIsMovedNotShrunk(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   theme.Rect
		want theme.Rect
	}{{
		name: "off the top-left corner is pushed back on",
		in:   theme.Rect{X: -40, Y: -10, W: 90, H: 24},
		want: theme.Rect{X: 0, Y: 0, W: 90, H: 24},
	}, {
		name: "off the bottom-right corner is pulled back on",
		in:   theme.Rect{X: 700, Y: 570, W: 90, H: 24},
		want: theme.Rect{X: 624, Y: 555, W: 90, H: 24},
	}, {
		name: "already inside is untouched",
		in:   theme.Rect{X: 101, Y: 403, W: 90, H: 24},
		want: theme.Rect{X: 101, Y: 403, W: 90, H: 24},
	}, {
		// The documented surprise: the box keeps its authored size and parks with its
		// FAR edge flush, overhanging the near one.
		name: "wider than the canvas keeps its width and overhangs the left",
		in:   theme.Rect{X: 10, Y: 100, W: 900, H: 24},
		want: theme.Rect{X: 714 - 900, Y: 100, W: 900, H: 24},
	}, {
		name: "taller than the canvas keeps its height and overhangs the top",
		in:   theme.Rect{X: 10, Y: 10, W: 90, H: 700},
		want: theme.Rect{X: 10, Y: 579 - 700, W: 90, H: 700},
	}} {
		if got := clampDesignRectToCanvas(tc.in, nudgeCanvas); got != tc.want {
			t.Errorf("%s: clamp(%+v) = %+v, want %+v", tc.name, tc.in, got, tc.want)
		}
	}
	// And it never changes a size, on any input — the property the two oversize rows
	// above are instances of.
	for _, in := range []theme.Rect{
		{X: -900, Y: -900, W: 2000, H: 2000},
		{X: 900, Y: 900, W: 1, H: 1},
		{X: 0, Y: 0, W: 714, H: 579},
	} {
		got := clampDesignRectToCanvas(in, nudgeCanvas)
		if got.W != in.W || got.H != in.H {
			t.Errorf("clamp(%+v) resized to %dx%d — sizing belongs to the resize gesture, which has "+
				"its own floor and its own anchored-edge rule", in, got.W, got.H)
		}
	}
}

// nudgeThemeLayout is the stock 714×579 canvas plus one small widget parked OFF the
// editor grid (101, 403 — neither is a multiple of layoutGridDesign) with clear room on
// all four sides. Off-grid is what makes the coarse-step assertion mean something, and
// the clearance is what stops the canvas clamp from silently answering a fine nudge.
func nudgeThemeLayout() map[string]theme.Rect {
	return map[string]theme.Rect{
		"courtroom":    {X: 0, Y: 0, W: 714, H: 579},
		"viewport":     {X: 0, Y: 0, W: 714, H: 382},
		nudgeWidgetKey: {X: 101, Y: 403, W: 90, H: 24},
	}
}

// stageThemedNudge is a themed courtroom with the THEMED editor armed and a widget
// already selected — the state a user is in after clicking a box, which is the gesture
// the nudge extends.
func stageThemedNudge(t *testing.T) *App {
	t.Helper()
	a := stageTabbarlessCourtroom(t, stripEditTheme)
	a.sess = &courtroom.Session{} // editorDrawSiteRuns: an editor only owns a live courtroom
	applyThemeGeometryForTest(a, nudgeThemeLayout())
	a.layoutEdit = true
	a.editTgt = designTarget(nudgeWidgetKey)
	a.themeWindowLayout(stripEditW, stripEditH) // the cache the nudge reads is built by the pass
	return a
}

// pressNudge feeds one arrow through the real router. coarse routes it as a Ctrl chord
// (c.hotkey), exactly as HandleEvent would.
func pressNudge(t *testing.T, a *App, key sdl.Keycode, coarse bool, w, h int32) bool {
	t.Helper()
	if coarse {
		a.ctx.hotkey = key
	} else {
		a.ctx.keyPressed = key
	}
	took := a.editorNudgeKeys(w, h)
	a.ctx.hotkey, a.ctx.keyPressed = 0, 0
	return took
}

// TestThemedNudgeMovesExactlyOneDesignPixel is the core promise on the themed editor: a
// theme's own widget is stored, drawn and snapped in DESIGN px, so one arrow press is
// one design pixel — in all four directions, with nothing else in the rect touched.
func TestThemedNudgeMovesExactlyOneDesignPixel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    sdl.Keycode
		dx, dy int
	}{
		{"left", sdl.K_LEFT, -1, 0},
		{"right", sdl.K_RIGHT, 1, 0},
		{"up", sdl.K_UP, 0, -1},
		{"down", sdl.K_DOWN, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := stageThemedNudge(t)
			before := a.themeRects[nudgeWidgetKey]
			if !pressNudge(t, a, tc.key, false, stripEditW, stripEditH) {
				t.Fatal("an armed editor must claim the arrow key")
			}
			want := theme.Rect{X: before.X + tc.dx, Y: before.Y + tc.dy, W: before.W, H: before.H}
			if got := a.themeRects[nudgeWidgetKey]; got != want {
				t.Fatalf("one arrow press moved the widget %+v → %+v, want %+v", before, got, want)
			}
		})
	}
}

// TestThemedCoarseNudgeLandsOnTheGrid pins the modifier's contract: Ctrl steps to the
// NEXT grid line rather than by one line's worth, so the coarse nudge lands on exactly
// the lattice snapDesign rounds a snapped drag onto — from an off-grid start, which is
// the only starting point that can tell the two apart.
func TestThemedCoarseNudgeLandsOnTheGrid(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  sdl.Keycode
	}{
		{"right", sdl.K_RIGHT},
		{"left", sdl.K_LEFT},
		{"down", sdl.K_DOWN},
		{"up", sdl.K_UP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := stageThemedNudge(t)
			before := a.themeRects[nudgeWidgetKey]
			if before.X%layoutGridDesign == 0 || before.Y%layoutGridDesign == 0 {
				t.Fatalf("fixture: %+v starts ON the grid, so a coarse step cannot be distinguished from a plain one", before)
			}
			pressNudge(t, a, tc.key, true, stripEditW, stripEditH)
			got := a.themeRects[nudgeWidgetKey]
			moved := got.X != before.X
			landed := got.X
			if got.Y != before.Y {
				moved, landed = true, got.Y
			}
			if !moved {
				t.Fatalf("Ctrl+arrow did not move the widget off %+v", before)
			}
			if landed%layoutGridDesign != 0 {
				t.Errorf("the coarse nudge landed at %d, which is not on the %d px grid a snapped drag uses", landed, layoutGridDesign)
			}
			// Strictly beyond the start, and by less than a full step (a "next line"
			// step, not a "one line's worth" step from an off-grid start).
			for _, d := range []struct {
				axis     string
				from, to int
			}{{"X", before.X, got.X}, {"Y", before.Y, got.Y}} {
				if diff := d.to - d.from; diff < -layoutGridDesign || diff > layoutGridDesign {
					t.Errorf("%s moved %d px, want at most one grid step (%d)", d.axis, diff, layoutGridDesign)
				}
			}
		})
	}
}

// TestThemedNudgePersistsAndUndoes pins the requirement that the nudge rides the SAME
// persist/undo path a drag release does: the per-key override is written (so the move
// survives a theme reload), and the editor's existing Ctrl+Z puts it back.
func TestThemedNudgePersistsAndUndoes(t *testing.T) {
	a := stageThemedNudge(t)
	before := a.themeRects[nudgeWidgetKey]

	pressNudge(t, a, sdl.K_RIGHT, false, stripEditW, stripEditH)
	ov, ok := a.d.Prefs.ThemeRectOverrides(stripEditTheme)[nudgeWidgetKey]
	if !ok {
		t.Fatal("a nudge must persist a rect override, exactly as a drag release does")
	}
	if want := [4]int{before.X + 1, before.Y, before.W, before.H}; ov != want {
		t.Fatalf("persisted override %v, want %v", ov, want)
	}

	a.layoutEditUndo()
	if got := a.themeRects[nudgeWidgetKey]; got != before {
		t.Fatalf("after undo the widget is at %+v, want its pre-nudge %+v", got, before)
	}
	if _, still := a.d.Prefs.ThemeRectOverrides(stripEditTheme)[nudgeWidgetKey]; still {
		t.Error("undo must clear the override too: a key back on the theme's own rect owns no override")
	}
}

// TestThemedNudgeRespectsTheCanvasClamp pins that the keyboard cannot walk a widget off
// the design canvas — the same stop a drag hits — and that a refused nudge leaves no
// phantom undo entry behind (a bare click's no-op discard exists for the same reason).
func TestThemedNudgeRespectsTheCanvasClamp(t *testing.T) {
	a := stageThemedNudge(t)
	r := a.themeRects[nudgeWidgetKey]
	r.X, r.Y = 0, 0 // flush into the canvas's top-left corner
	a.themeRects[nudgeWidgetKey] = r
	a.invalidateThemeCanvases()
	a.themeWindowLayout(stripEditW, stripEditH)

	for _, key := range []sdl.Keycode{sdl.K_LEFT, sdl.K_UP} {
		if !pressNudge(t, a, key, false, stripEditW, stripEditH) {
			t.Fatal("the editor must still CONSUME the key it refuses to act on")
		}
	}
	if got := a.themeRects[nudgeWidgetKey]; got != r {
		t.Fatalf("a widget flush against the canvas edge moved to %+v, want %+v", got, r)
	}
	if len(a.editUndo) != 0 {
		t.Errorf("a refused nudge pushed %d undo entries, want none", len(a.editUndo))
	}
	if _, ov := a.d.Prefs.ThemeRectOverrides(stripEditTheme)[nudgeWidgetKey]; ov {
		t.Error("a refused nudge persisted an override for a widget that never moved")
	}
}

// TestTabStripNudgeMovesOneScreenPixelAtEveryScale is the special case.
//
// The server-tab strip is intrinsically sized CLIENT chrome whose stored X/Y are
// canvas-relative CLIENT px, not design px (themeTabBarKey) — so "one pixel" for it means
// one SCREEN pixel, at every fit mode and every layout scale, not one design pixel that
// the scale would then turn into zero or two. It also has to come out PLACED: the
// persisted override IS its placement flag (tabStripPlacementRecorded), so a nudged strip
// must leave the docked chrome band exactly as a dragged one does.
func TestTabStripNudgeMovesOneScreenPixelAtEveryScale(t *testing.T) {
	for _, tc := range themeFitDragCases {
		t.Run(tc.name, func(t *testing.T) {
			a := stageTabbarlessCourtroom(t, stripEditTheme)
			a.sess = &courtroom.Session{}
			tc.apply(a)
			a.layoutEdit = true
			a.editTgt = designTarget(themeTabBarKey)
			a.themeLay.valid = false

			before, ok := a.themeWindowLayout(tc.w, tc.h).rect(themeTabBarKey)
			if !ok {
				t.Fatal("the themed editor must be handed a box for the strip")
			}
			if a.tabStripThemeParked() {
				t.Fatal("fixture: the strip starts parked, so the placement half is not being tested")
			}
			if !pressNudge(t, a, sdl.K_RIGHT, false, tc.w, tc.h) {
				t.Fatal("an armed editor must claim the arrow key")
			}
			after, ok := a.themeWindowLayout(tc.w, tc.h).rect(themeTabBarKey)
			if !ok {
				t.Fatal("the strip lost its box during the nudge")
			}
			want := sdl.Rect{X: before.X + 1, Y: before.Y, W: before.W, H: before.H}
			if after != want {
				t.Fatalf("scale %.4f: one arrow press moved the strip's box %+v → %+v, want %+v (one SCREEN px, no resize)",
					a.themeWindowLayout(tc.w, tc.h).scaleX, before, after, want)
			}
			if !a.tabStripThemeParked() {
				t.Error("a nudged strip must be marked placed, exactly as a dragged one is — otherwise it snaps back into the chrome band")
			}
			// The box is describing the widget, not a rect the strip stayed behind.
			assertStripBoxIsOnTheStrip(t, a, tc.w, tc.h, "after a one-pixel nudge")
		})
	}
}

// TestTabStripNudgeFollowsAClickSelection drives the whole gesture the user performs —
// click the strip's box to select it, then arrow it — through the real editor, so the
// selection field the nudge reads (a.editTgt) is the one a press actually sets.
func TestTabStripNudgeFollowsAClickSelection(t *testing.T) {
	a, ctx, cleanup := stripEditFixture(t)
	defer cleanup()
	a.sess = &courtroom.Session{}

	// A press-and-release that goes nowhere: it selects without persisting anything.
	before, after := dragStrip(t, a, ctx, 0, 0)
	if after != before {
		t.Fatalf("fixture: a zero-travel click moved the strip %+v → %+v", before, after)
	}
	if a.editTgt.designKey() != themeTabBarKey {
		t.Fatalf("fixture: the click did not leave the strip selected (editKey=%q)", a.editTgt.designKey())
	}
	if a.tabStripThemeParked() {
		t.Fatal("fixture: a discarded click must leave the strip un-parked")
	}

	if !pressNudge(t, a, sdl.K_DOWN, false, stripEditW, stripEditH) {
		t.Fatal("an armed editor must claim the arrow key")
	}
	moved, ok := a.themeWindowLayout(stripEditW, stripEditH).rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip lost its box during the nudge")
	}
	if want := (sdl.Rect{X: before.X, Y: before.Y + 1, W: before.W, H: before.H}); moved != want {
		t.Fatalf("click-then-arrow moved the strip %+v → %+v, want %+v", before, moved, want)
	}
	assertStripBoxIsOnTheStrip(t, a, stripEditW, stripEditH, "after click-then-arrow")
}

// --- the classic (default-layout) editor ---------------------------------------------

const (
	// The classic fixture's window and the slot it moves. The slot starts off-grid for
	// the same reason the themed one does.
	classicNudgeW = int32(1152)
	classicNudgeH = int32(864)
)

// stageClassicNudge is a live courtroom with the CLASSIC slot editor armed and one slot
// registered and selected — the state a press leaves behind.
func stageClassicNudge(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	a.screen = ScreenCourtroom
	a.room = newRoomForTest(t)
	a.sess = &courtroom.Session{}
	a.classicEdit = true
	a.classicTgt = classicTarget(slotOOC)
	a.regSlot(slotOOC, sdl.Rect{X: 301, Y: 205, W: 320, H: 140}, sdl.Rect{X: 301, Y: 205, W: 320, H: 140})
	return a
}

// classicNudgeRect resolves the slot the way the courtroom's next frame will — through
// the persisted override — so the assertions are about what the user SEES, not about the
// float the editor happened to write.
func classicNudgeRect(a *App) sdl.Rect {
	r, _ := a.classicSlotCurrentRect(slotOOC, classicNudgeW, classicNudgeH)
	return r
}

// TestClassicNudgeMovesExactlyOneScreenPixel is the core promise on the classic editor,
// which works in SCREEN px. It resolves the slot back through the override the nudge
// persisted, so it also pins the fraction round-trip: a truncating frac→px used to come
// back one pixel short on roughly one position in six at some window widths, which for a
// one-pixel gesture is the whole gesture (see fracPx).
func TestClassicNudgeMovesExactlyOneScreenPixel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    sdl.Keycode
		dx, dy int32
	}{
		{"left", sdl.K_LEFT, -1, 0},
		{"right", sdl.K_RIGHT, 1, 0},
		{"up", sdl.K_UP, 0, -1},
		{"down", sdl.K_DOWN, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := stageClassicNudge(t)
			before := classicNudgeRect(a)
			if !pressNudge(t, a, tc.key, false, classicNudgeW, classicNudgeH) {
				t.Fatal("an armed editor must claim the arrow key")
			}
			want := sdl.Rect{X: before.X + tc.dx, Y: before.Y + tc.dy, W: before.W, H: before.H}
			if got := classicNudgeRect(a); got != want {
				t.Fatalf("one arrow press moved the slot %+v → %+v, want %+v", before, got, want)
			}
		})
	}
}

// TestClassicNudgeWalksOnePixelAtATime is the fraction round-trip under repetition: ten
// presses must be ten pixels. A single lossy round-trip is invisible; a held arrow key
// is where it would show up as a widget that stops moving.
func TestClassicNudgeWalksOnePixelAtATime(t *testing.T) {
	a := stageClassicNudge(t)
	before := classicNudgeRect(a)
	const presses = 10
	for i := 0; i < presses; i++ {
		pressNudge(t, a, sdl.K_RIGHT, false, classicNudgeW, classicNudgeH)
		if got := classicNudgeRect(a); got.X != before.X+int32(i)+1 {
			t.Fatalf("press %d put the slot at X=%d, want %d — a persisted pixel was dropped in the fraction round-trip",
				i+1, got.X, before.X+int32(i)+1)
		}
	}
}

// TestClassicCoarseNudgeLandsOnTheGrid pins that the classic coarse step reads the
// editor's OWN, user-tunable grid (the banner's Grid chip) rather than the themed
// editor's fixed one — otherwise Ctrl+arrow would land off the lattice the Snap chip is
// rounding drags onto.
func TestClassicCoarseNudgeLandsOnTheGrid(t *testing.T) {
	for _, grid := range []int{4, 8, 16, 32} {
		a := stageClassicNudge(t)
		a.d.Prefs.SetLayoutGridSize(grid)
		before := classicNudgeRect(a)
		if before.X%int32(grid) == 0 {
			t.Fatalf("fixture: X=%d already sits on the %d px grid", before.X, grid)
		}
		pressNudge(t, a, sdl.K_RIGHT, true, classicNudgeW, classicNudgeH)
		got := classicNudgeRect(a)
		if got.X%int32(grid) != 0 {
			t.Errorf("grid %d: the coarse nudge landed at X=%d, not on the grid a snapped drag uses", grid, got.X)
		}
		if got.X <= before.X || got.X-before.X > int32(grid) {
			t.Errorf("grid %d: X moved %d → %d, want the next line up (at most %d px)", grid, before.X, got.X, grid)
		}
		if got.Y != before.Y {
			t.Errorf("grid %d: a horizontal nudge moved Y %d → %d", grid, before.Y, got.Y)
		}
	}
}

// TestClassicNudgePersistsAndUndoes pins the classic half of the persist/undo
// requirement: the durable pref carries the move (so it survives a relaunch) and the
// editor's existing Ctrl+Z re-syncs both the live map and the pref.
func TestClassicNudgePersistsAndUndoes(t *testing.T) {
	a := stageClassicNudge(t)
	before := classicNudgeRect(a)

	pressNudge(t, a, sdl.K_DOWN, false, classicNudgeW, classicNudgeH)
	if _, ok := a.d.Prefs.ClassicLayoutOverrides()[slotOOC]; !ok {
		t.Fatal("a nudge must persist a slot override, exactly as a drag release does")
	}
	if got := classicNudgeRect(a); got.Y != before.Y+1 {
		t.Fatalf("nudged slot Y=%d, want %d", got.Y, before.Y+1)
	}

	a.classicEditUndo()
	if _, ok := a.classicOv[slotOOC]; ok {
		t.Error("undo must drop the override the nudge minted")
	}
	if len(a.d.Prefs.ClassicLayoutOverrides()) != 0 {
		t.Error("undo must re-sync the durable pref too, or the nudge outlives its undo")
	}
	if got := classicNudgeRect(a); got != before {
		t.Fatalf("after undo the slot is at %+v, want its pre-nudge %+v", got, before)
	}
}

// TestClassicNudgeRespectsTheWindowClamp is the classic clamp: the keyboard cannot walk
// a slot off the window, and a refused nudge leaves no undo entry and no override.
func TestClassicNudgeRespectsTheWindowClamp(t *testing.T) {
	a := stageClassicNudge(t)
	a.regSlot(slotOOC, sdl.Rect{X: 0, Y: 0, W: 320, H: 140}, sdl.Rect{X: 0, Y: 0, W: 320, H: 140})
	before := classicNudgeRect(a)

	for _, key := range []sdl.Keycode{sdl.K_LEFT, sdl.K_UP} {
		if !pressNudge(t, a, key, false, classicNudgeW, classicNudgeH) {
			t.Fatal("the editor must still CONSUME the key it refuses to act on")
		}
	}
	if got := classicNudgeRect(a); got != before {
		t.Fatalf("a slot flush against the window edge moved to %+v, want %+v", got, before)
	}
	if len(a.classicUndo) != 0 {
		t.Errorf("a refused nudge pushed %d undo entries, want none", len(a.classicUndo))
	}
	if len(a.d.Prefs.ClassicLayoutOverrides()) != 0 {
		t.Error("a refused nudge persisted an override for a slot that never moved")
	}
}

// TestArrowsAreUntouchedWithNoEditorArmed is the other half of the routing rule. The
// nudge claims c.keyPressed and c.hotkey, which the IC/OOC recall rings, the pair-ghost
// offset, the emote-preview cycler, every focused text field's caret and any user-bound
// Ctrl chord also read — so it must claim NOTHING on a frame where an armed editor's own
// draw does not reach the screen. Every arm of editorDrawSiteRuns is exercised, because
// each one is a state an editor flag can outlive.
func TestArrowsAreUntouchedWithNoEditorArmed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(*App)
	}{
		{"no editor armed", func(a *App) { a.classicEdit, a.layoutEdit = false, false }},
		{"not on the courtroom screen", func(a *App) { a.screen = ScreenSettings }},
		{"no room", func(a *App) { a.room = nil }},
		{"no session", func(a *App) { a.sess = nil }},
		{"theater mode owns the stage", func(a *App) { a.theaterOn = true }},
		{"a gif export preempts the screen dispatch", func(a *App) { a.gifExporting = true }},
		{"the scene maker preempts the screen dispatch", func(a *App) { a.makerOpen = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := stageClassicNudge(t)
			tc.spoil(a)
			before := classicNudgeRect(a)

			a.ctx.keyPressed = sdl.K_RIGHT
			if a.editorNudgeKeys(classicNudgeW, classicNudgeH) {
				t.Fatal("the nudge claimed a key on a frame where no editor owns the screen")
			}
			if a.ctx.keyPressed != sdl.K_RIGHT {
				t.Error("the plain arrow was consumed: list scrolling, the recall rings and text-field carets never see it")
			}
			a.ctx.hotkey, a.ctx.keyPressed = sdl.K_LEFT, 0
			if a.editorNudgeKeys(classicNudgeW, classicNudgeH) {
				t.Fatal("the nudge claimed a Ctrl chord on a frame where no editor owns the screen")
			}
			if a.ctx.hotkey != sdl.K_LEFT {
				t.Error("the Ctrl chord was consumed: a user-rebound Ctrl+arrow hotkey would be dead")
			}
			a.ctx.hotkey = 0
			if got := classicNudgeRect(a); got != before {
				t.Fatalf("nothing was armed, yet the slot moved %+v → %+v", before, got)
			}
		})
	}
}

// TestNudgeConsumesTheKeyItOwns is the complement: while an editor DOES own the screen,
// the arrow never survives to a second consumer — the same promise editorUndoChord makes
// for Ctrl+Z/Y, and the reason both live pre-screen instead of in the editors' draw
// bodies (which run after every other plain-key consumer has already looked).
func TestNudgeConsumesTheKeyItOwns(t *testing.T) {
	a := stageClassicNudge(t)
	a.ctx.keyPressed = sdl.K_DOWN
	if !a.editorNudgeKeys(classicNudgeW, classicNudgeH) {
		t.Fatal("an armed editor must claim the arrow key")
	}
	if a.ctx.keyPressed != 0 {
		t.Errorf("keyPressed = %v after the nudge, want it consumed", a.ctx.keyPressed)
	}
	a.ctx.hotkey = sdl.K_UP
	if !a.editorNudgeKeys(classicNudgeW, classicNudgeH) {
		t.Fatal("an armed editor must claim the Ctrl chord")
	}
	if a.ctx.hotkey != 0 {
		t.Errorf("hotkey = %v after the coarse nudge, want it consumed", a.ctx.hotkey)
	}
	// Nothing selected is still a consumed key: a bound action must not fire mid-edit.
	a.classicTgt = noTarget()
	a.ctx.keyPressed = sdl.K_LEFT
	if !a.editorNudgeKeys(classicNudgeW, classicNudgeH) || a.ctx.keyPressed != 0 {
		t.Error("with no widget selected the editor must still swallow its own arrow key")
	}
}

// TestNudgeIgnoresADegenerateBox pins that the nudge asks the SAME question editBaseFor
// asks — lay.rect, which rejects W<=0 || H<=0 — and not a bare map probe.
//
// A degenerate entry passing a bare probe is not harmless: editBaseFor would fall
// through to a.themeRects[key], and for the server-tab strip that entry is the inert
// synthesized SEED (seedTabBarDesignRect) that nothing ever paints or hit-tests. The
// nudge would then move and PERSIST the seed — and for this key the override IS the
// placement flag (tabStripPlacementRecorded), so the strip would come out "placed" at a
// coordinate that never appeared on screen, and leave the docked chrome band for it.
// That is exactly the phantom the drag's no-op discard exists to prevent.
func TestNudgeIgnoresADegenerateBox(t *testing.T) {
	a := stageTabbarlessCourtroom(t, stripEditTheme)
	a.sess = &courtroom.Session{}
	a.layoutEdit = true
	a.editTgt = designTarget(themeTabBarKey)
	a.themeLay.valid = false
	lay := a.themeWindowLayout(stripEditW, stripEditH)
	if _, ok := lay.rect(themeTabBarKey); !ok {
		t.Fatal("fixture: the strip must start with a real box, or the collapse below proves nothing")
	}
	if a.tabStripThemeParked() {
		t.Fatal("fixture: the strip starts parked, so the phantom-placement half is untestable")
	}

	// Collapse the cached box the way a hidden/omitted/clamped-out element leaves it: the
	// key is still PRESENT in the map, which is precisely what a bare probe cannot tell.
	box := lay.r[themeTabBarKey]
	box.W = 0
	lay.r[themeTabBarKey] = box

	if !pressNudge(t, a, sdl.K_RIGHT, false, stripEditW, stripEditH) {
		t.Fatal("the editor must still CONSUME the key it refuses to act on")
	}
	if _, ov := a.d.Prefs.ThemeRectOverrides(stripEditTheme)[themeTabBarKey]; ov {
		t.Error("a nudge against a box nothing painted persisted an override")
	}
	if a.tabStripThemeParked() {
		t.Error("the strip was marked placed by a nudge that moved only the synthesized seed")
	}
	if len(a.editUndo) != 0 {
		t.Errorf("the refused nudge pushed %d undo entries, want none", len(a.editUndo))
	}
}

// TestPaletteKeepsItsArrowsWhileAnEditorIsArmed pins the yield rule.
//
// An armed editor is not exclusive: handleHotkeys runs unconditionally from both
// courtroom passes and editorUndoChord only diverts Ctrl+Z/Y, so Ctrl+Space opens the
// command palette mid-edit; the palette is not in courtroomModals and
// closeEditorBlockingOverlays leaves it open. It draws AFTER the courtroom pass — on
// top of the editor — and steers with ↑/↓ while its search field steers a caret with
// ←/→, so a pre-screen nudge claiming those keys silently strips the palette of both.
func TestPaletteKeepsItsArrowsWhileAnEditorIsArmed(t *testing.T) {
	a := stageClassicNudge(t)
	before := classicNudgeRect(a)
	a.togglePalette() // the real front door: Ctrl+Space reaches this with an editor armed
	if !a.paletteOpen {
		t.Fatal("fixture: the palette did not open")
	}

	for _, key := range []sdl.Keycode{sdl.K_DOWN, sdl.K_UP, sdl.K_LEFT, sdl.K_RIGHT} {
		a.ctx.keyPressed = key
		if a.editorNudgeKeys(classicNudgeW, classicNudgeH) {
			t.Fatalf("the nudge claimed %v while the palette owned the keyboard", key)
		}
		if a.ctx.keyPressed != key {
			t.Errorf("%v was consumed: the palette loses row navigation and its caret", key)
		}
		a.ctx.keyPressed = 0
	}
	if got := classicNudgeRect(a); got != before {
		t.Fatalf("the palette's arrows moved the slot %+v → %+v", before, got)
	}

	// A blurred search field is NOT the same question: clicking the palette's own
	// chrome drops the focus while its row selection stays live, so paletteOpen has to
	// be checked as well as focusID, not instead of it.
	a.ctx.focusID = ""
	a.ctx.keyPressed = sdl.K_DOWN
	if a.editorNudgeKeys(classicNudgeW, classicNudgeH) || a.ctx.keyPressed != sdl.K_DOWN {
		t.Error("with the palette open but its field blurred, ↓ still belongs to the palette")
	}
	a.ctx.keyPressed = 0

	// And the nudge comes straight back when the palette closes — the yield is scoped
	// to the frames another surface actually owns.
	a.togglePalette()
	if a.paletteOpen {
		t.Fatal("fixture: the palette did not close")
	}
	if !pressNudge(t, a, sdl.K_DOWN, false, classicNudgeW, classicNudgeH) {
		t.Fatal("with the palette closed the armed editor must claim the arrow again")
	}
	if got := classicNudgeRect(a); got.Y != before.Y+1 {
		t.Errorf("post-palette nudge put the slot at Y=%d, want %d", got.Y, before.Y+1)
	}
}

// TestFocusedFieldKeepsItsCaretKeys is the same rule for the surface the palette is
// only one instance of: any focused text field steers its caret with ←/→, and
// `c.focusID != ""` is the house test every other plain-key claim already yields on
// (handleJukeboxKeys, handleMacroKeys, handleICPhraseKeys, the style-preset and
// showname binds).
func TestFocusedFieldKeepsItsCaretKeys(t *testing.T) {
	a := stageClassicNudge(t)
	before := classicNudgeRect(a)
	a.ctx.focusID = "icmsg"

	a.ctx.keyPressed = sdl.K_LEFT
	if a.editorNudgeKeys(classicNudgeW, classicNudgeH) {
		t.Fatal("the nudge claimed an arrow while a text field held focus")
	}
	if a.ctx.keyPressed != sdl.K_LEFT {
		t.Error("the caret key was consumed: the focused field cannot move its caret")
	}
	a.ctx.keyPressed = 0
	if got := classicNudgeRect(a); got != before {
		t.Fatalf("a focused field's arrow moved the slot %+v → %+v", before, got)
	}
}

// TestArmingAnEditorDropsTheFocusedField is what stops the yield above from stranding
// the nudge. A field keeps eating keys while it holds focus — the editors' fence blanks
// the POINTER, not the keyboard — so an IC box focused before Ctrl+F2 would otherwise
// swallow every nudge for the whole edit, from a caret the editor's chrome covers.
// Both arm paths share closeEditorBlockingOverlays, so both are pinned here.
func TestArmingAnEditorDropsTheFocusedField(t *testing.T) {
	for _, tc := range []struct {
		name string
		arm  func(*App)
	}{
		{"classic", (*App).startClassicEdit},
		{"themed", (*App).startLayoutEdit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := stageClassicNudge(t)
			a.classicEdit, a.layoutEdit = false, false
			a.ctx.focusID = "icmsg"
			tc.arm(a)
			if a.ctx.focusID != "" {
				t.Fatalf("focusID = %q after arming, want the field dropped", a.ctx.focusID)
			}
		})
	}

	// End to end on the classic editor: arm with a field focused, re-select the slot
	// the way a user's click does, and the very first arrow must still nudge.
	a := stageClassicNudge(t)
	a.classicEdit = false
	a.ctx.focusID = "icmsg"
	a.startClassicEdit()
	a.classicTgt = classicTarget(slotOOC) // the arm clears the selection; the user's click re-sets it
	before := classicNudgeRect(a)
	if !pressNudge(t, a, sdl.K_RIGHT, false, classicNudgeW, classicNudgeH) {
		t.Fatal("a freshly armed editor must own its arrow keys")
	}
	if got := classicNudgeRect(a); got.X != before.X+1 {
		t.Errorf("nudge after arming put the slot at X=%d, want %d", got.X, before.X+1)
	}
}

// TestFracPxRoundTripsEveryPixel pins the arithmetic the classic nudge stands on.
//
// A classic slot's position is persisted as a window FRACTION, so every nudge is a
// pixel → fraction → pixel round trip. With a truncating frac→px that round trip is not
// the identity: the quotient-times-extent is only within one ulp of the original, and
// when the ulp falls short a whole pixel is thrown away. 714 (the stock AO2 canvas
// width) was the worst of the common sizes, losing roughly one position in six — enough
// that a held arrow key visibly stalls. 391 and 943 are the two other awkward widths
// from the theme corpus.
func TestFracPxRoundTripsEveryPixel(t *testing.T) {
	for _, extent := range []int32{256, 391, 640, 714, 800, 943, 1152, 1366, 1918} {
		for px := int32(0); px <= extent; px++ {
			if got := fracPx(float64(px)/float64(extent), extent); got != px {
				t.Fatalf("extent %d: pixel %d stored as a fraction came back as %d — a one-pixel nudge would be dropped here",
					extent, px, got)
			}
		}
	}
}

// TestGridStepToLandsOnTheLatticeFromAnywhere pins the coarse step's arithmetic
// directly, including the negative range: the server-tab strip's stored Y is legitimately
// negative while it is docked in the chrome band above the canvas, and Go's
// truncate-toward-zero division would step the wrong way there.
func TestGridStepToLandsOnTheLatticeFromAnywhere(t *testing.T) {
	const g = layoutGridDesign
	for v := -3 * g; v <= 3*g; v++ {
		up := gridStepTo(v, 1, g)
		down := gridStepTo(v, -1, g)
		if up%g != 0 || down%g != 0 {
			t.Fatalf("v=%d: stepped to %d / %d, neither on the %d px lattice", v, up, down, g)
		}
		if up <= v || up-v > g {
			t.Fatalf("v=%d: forward step landed at %d, want the next line strictly above and within %d", v, up, g)
		}
		if down >= v || v-down > g {
			t.Fatalf("v=%d: backward step landed at %d, want the next line strictly below and within %d", v, down, g)
		}
	}
	// A junk grid falls back to the editor's own default rather than dividing by zero.
	if got := gridStepTo(5, 1, 0); got != layoutGridDesign {
		t.Errorf("gridStepTo with no grid = %d, want the %d px default", got, layoutGridDesign)
	}
}

// --- free elements: the third space (v1.90.0 W6) --------------------------------------

// elemNudgeRect is the element fixture's starting rect. Deliberately the SAME
// coordinates the themed-widget fixture starts from (nudgeThemeLayout), because the
// whole point of the parity test below is that one number cannot move differently from
// the other; off-grid on both axes, so a coarse step is distinguishable from a fine one.
var elemNudgeRect = theme.Rect{X: 101, Y: 403, W: 90, H: 24}

// stageElementNudge is the THEMED editor armed with a FREE ELEMENT selected — the state
// W7's editor leaves behind when the user clicks a decoration. W6 ships no UI that can
// produce it, which is exactly why it is constructed here: the machinery has to be
// correct before the screen that drives it exists.
//
// No layout cache is staged because the element arm needs none: an element's authored
// rect is the thing being moved, and the bake is what turns it into pixels afterwards.
func stageElementNudge(t *testing.T, mutate func(*theme.Element)) *App {
	t.Helper()
	a := testTabApp(t)
	a.screen = ScreenCourtroom
	a.room = newRoomForTest(t)
	a.sess = &courtroom.Session{}
	a.layoutEdit = true
	el := theme.Element{ID: "badge", Kind: theme.ElemShape, Rect: elemNudgeRect}
	if mutate != nil {
		mutate(&el)
	}
	a.themeSidecar = &theme.Sidecar{Elements: []theme.Element{el}}
	a.editTgt = elementTarget(0)
	return a
}

// TestElementNudgeMatchesTheSlotNudge is the wave's parity requirement: an arrow key
// must address a free element EXACTLY as it addresses a themed slot.
//
// Both arms are driven through the real router from the same off-grid start, and the
// two deltas are compared rather than each being checked against a hand-written number
// — a test that asserted "1" twice would still pass if the two arms drifted onto
// different grids or different modifiers.
func TestElementNudgeMatchesTheSlotNudge(t *testing.T) {
	for _, coarse := range []bool{false, true} {
		for _, key := range []sdl.Keycode{sdl.K_LEFT, sdl.K_RIGHT, sdl.K_UP, sdl.K_DOWN} {
			name := "fine"
			if coarse {
				name = "coarse"
			}
			t.Run(name, func(t *testing.T) {
				slotApp := stageThemedNudge(t)
				slotBefore := slotApp.themeRects[nudgeWidgetKey]
				if slotBefore != elemNudgeRect {
					t.Fatalf("fixture drift: the slot starts at %+v, the element at %+v — the two must match for a parity claim",
						slotBefore, elemNudgeRect)
				}
				if !pressNudge(t, slotApp, key, coarse, stripEditW, stripEditH) {
					t.Fatal("an armed editor must claim the arrow key")
				}
				slotAfter := slotApp.themeRects[nudgeWidgetKey]

				elemApp := stageElementNudge(t, nil)
				if !pressNudge(t, elemApp, key, coarse, stripEditW, stripEditH) {
					t.Fatal("an element selection must claim the arrow key too")
				}
				elemAfter := elemApp.themeSidecar.Elements[0].Rect
				if elemAfter != slotAfter {
					t.Fatalf("the element moved to %+v where the slot moved to %+v — same key, same start, same grid",
						elemAfter, slotAfter)
				}
				if elemAfter.W != elemNudgeRect.W || elemAfter.H != elemNudgeRect.H {
					t.Errorf("a nudge resized the element %+v → %+v", elemNudgeRect, elemAfter)
				}
			})
		}
	}
}

// TestElementNudgeIsOnePixelAndReBakes pins the two halves parity alone cannot: the
// plain step is layoutNudgePx of the element's OWN space, and the move invalidates the
// layout so the next frame re-bakes the element's absolute rect from it.
func TestElementNudgeIsOnePixelAndReBakes(t *testing.T) {
	a := stageElementNudge(t, nil)
	a.themeLay.valid = true // whatever a previous frame left behind
	if !pressNudge(t, a, sdl.K_RIGHT, false, stripEditW, stripEditH) {
		t.Fatal("an armed editor must claim the arrow key")
	}
	want := elemNudgeRect
	want.X += layoutNudgePx
	if got := a.themeSidecar.Elements[0].Rect; got != want {
		t.Fatalf("one arrow press moved the element %+v → %+v, want %+v", elemNudgeRect, got, want)
	}
	if a.themeLay.valid {
		t.Error("a moved element left the layout cache valid — the bake resolves its absolute rect, so nothing would redraw")
	}
}

// TestElementNudgeRefusesALockedElementButStillEatsTheKey pins the editor-only lock
// (theme.Element.Locked) on the keyboard path. Consuming the key regardless is the same
// promise every other refused nudge makes: while an editor is armed, a key it owns can
// never also reach a rebound hotkey or the IC recall ring.
func TestElementNudgeRefusesALockedElementButStillEatsTheKey(t *testing.T) {
	a := stageElementNudge(t, func(el *theme.Element) { el.Locked = true })
	if !pressNudge(t, a, sdl.K_RIGHT, false, stripEditW, stripEditH) {
		t.Fatal("the editor must still CONSUME the key it refuses to act on")
	}
	if got := a.themeSidecar.Elements[0].Rect; got != elemNudgeRect {
		t.Fatalf("a locked element moved %+v → %+v", elemNudgeRect, got)
	}
}

// TestElementNudgeSurvivesAThemeWithNoElements is the degenerate case the router must
// not panic on: a selection left over from a theme that has just been swapped for a
// stock AO2 one (no sidecar at all), or an index past the end of a shorter list.
func TestElementNudgeSurvivesAThemeWithNoElements(t *testing.T) {
	for _, tc := range []struct {
		name string
		prep func(*App)
	}{
		{"no sidecar at all", func(a *App) { a.themeSidecar = nil }},
		{"an index past the end", func(a *App) { a.editTgt = elementTarget(len(a.themeSidecar.Elements) + 5) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := stageElementNudge(t, nil)
			tc.prep(a)
			if !pressNudge(t, a, sdl.K_RIGHT, false, stripEditW, stripEditH) {
				t.Fatal("the editor must still consume its own arrow key")
			}
		})
	}
}
