package ui

// THE EDITOR'S CANVAS, DRIVEN (v1.90.0 W7a).
//
// Every gate here goes through the REAL frame (frameharness_test.go) and the real
// pointer path: a press is a press, a drag is a run of frames with the button down,
// and a release is a frame with it up. Nothing calls editorDragFrame with a
// hand-built gesture, because the wiring — the screen dispatch, the canvas rect, the
// probe's space priority, the undo group's lifetime — is exactly what a gate that
// skipped the frame would stop covering.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const (
	// editorGateBoxPx is the fixture element's size in DESIGN px. Comfortably past
	// editHandleRoomPx (five handle widths) so the box is offered its FULL set of
	// eight grips — a cramped box keeps only the bottom-right one by design, and a
	// gate that used one would be measuring the fallback.
	editorGateBoxPx = 96
	// editorGateSaveWait bounds the save gate's wait on the write goroutine.
	// Generous for one small file, with a hard stop so a stuck write fails loudly.
	editorGateSaveWait = 5 * time.Second
	// editorGateThemeName is the folder name the fixture edits under. Named because
	// the save path is derived from it (themeCatalogEntry.Dir).
	editorGateThemeName = "w7acanvasgate"
)

// editorCanvasElement is one authored element, ABSOLUTE in courtroom space, so its
// rect is its geometry rather than a delta on an anchor (design §6.6 R1).
func editorCanvasElement(id string, r theme.Rect) theme.Element {
	el := editProbeElement()
	el.ID = id
	el.Kind = theme.ElemShape
	el.Band = theme.BandOverlay
	el.Space = theme.SpaceCourtroom
	el.Anchor = ""
	el.Rect = r
	el.Hidden, el.Locked = false, false
	// No visibility condition: the probe fixture must be a plain always-there box, or
	// a gate could pass for the wrong reason on a session state nothing set up.
	el.VisibleWhen = theme.Condition{}
	return el
}

// stageEditorCanvas opens the editor over the themed fixture with one element per
// offset, each a editorGateBoxPx box placed at the canvas centre plus that offset in
// DESIGN px.
//
// The elements are authored BEFORE the open, so the document's baseline contains
// them — which is what makes the exit-without-saving gate measure the right thing.
func stageEditorCanvas(t *testing.T, offsets ...[2]int) (*App, func(), []editTarget) {
	t.Helper()
	a, cleanup := stageFrameDrivenApp(t)
	driveFrame(a) // one frame first: the layout cache builds on its cold path
	if !a.themeLay.valid || a.themeLay.scaleX <= 0 {
		cleanup()
		t.Fatal("the fixture never built a themed layout — there is no canvas to edit")
	}
	if a.themeSidecar == nil {
		a.themeSidecar = theme.NewSidecar()
	}
	_, root := a.d.Prefs.Theme()
	a.d.Prefs.SetTheme(editorGateThemeName, root) // the save path is derived from the name
	var targets []editTarget
	for i, off := range offsets {
		a.themeSidecar.Elements = append(a.themeSidecar.Elements,
			editorCanvasElement("gate"+string(rune('a'+i)), editorCentreRect(a, off[0], off[1])))
		targets = append(targets, elementTarget(len(a.themeSidecar.Elements)-1))
	}
	a.invalidateThemeCanvases()
	if !a.openThemeEditor(ScreenCourtroom) {
		cleanup()
		t.Fatal("openThemeEditor refused a theme that has a sidecar")
	}
	driveFrame(a)
	return a, cleanup, targets
}

// editorCentreRect is a editorGateBoxPx box at the design canvas's centre, offset by
// (ox, oy) design px so several can be placed apart.
func editorCentreRect(a *App, ox, oy int) theme.Rect {
	lay := &a.themeLay
	w := int(float64(lay.area.W) / lay.scaleX)
	h := int(float64(lay.area.H) / lay.scaleY)
	return theme.Rect{
		X: w/2 - editorGateBoxPx/2 + ox,
		Y: h/2 - editorGateBoxPx/2 + oy,
		W: editorGateBoxPx, H: editorGateBoxPx,
	}
}

// editorPainted is a target's box on screen, through the production resolver.
func editorPainted(t *testing.T, a *App, tgt editTarget) sdl.Rect {
	t.Helper()
	g, ok := a.editorTargetGeom(&a.themeLay, tgt)
	if !ok {
		t.Fatalf("%v has no box on the canvas — the gate cannot press it", tgt)
	}
	return g.painted
}

// editorAuthored is a target's authored rect, through the document.
func editorAuthored(t *testing.T, a *App, tgt editTarget) theme.Rect {
	t.Helper()
	g, ok := a.editorTargetGeom(&a.themeLay, tgt)
	if !ok {
		t.Fatalf("%v has no geometry", tgt)
	}
	return g.authored
}

// editorCentreOf is a rect's middle, i.e. where a gate presses to grab it.
func editorCentreOf(r sdl.Rect) (int32, int32) { return r.X + r.W/2, r.Y + r.H/2 }

// driveMouse runs ONE real frame with the pointer at (x, y) and the button in the
// given state, seeded exactly where HandleEvent seeds it: after BeginFrame (which
// re-reads the cursor from SDL and snapshots last frame's button) and before
// App.Frame.
func driveMouse(a *App, x, y int32, down bool) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.mouseX, a.ctx.mouseY = x, y
	if down && !a.ctx.mouseDown {
		a.ctx.downX, a.ctx.downY = x, y
	}
	a.ctx.mouseDown = down
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// driveDrag is a whole ordinary gesture: press, two moves, release. TWO moves
// rather than one because a single-frame drag cannot see a coalescing bug — the
// second frame is the one that would produce a second undo entry.
func driveDrag(a *App, x0, y0, x1, y1 int32) {
	driveMouse(a, x0, y0, true)
	driveMouse(a, (x0+x1)/2, (y0+y1)/2, true)
	driveMouse(a, x1, y1, true)
	driveMouse(a, x1, y1, false)
}

// drivePreciseDrag is the same gesture with Shift held for the MOVES only.
//
// The split is the interaction's own: at PRESS time Shift means "add to the
// selection" (§3.2), and during the drag it means "pixel-precise — bypass the grid
// and the magnet". A gate that held Shift over the press would never start a drag at
// all. magnetBypassed reads SDL's own modifier state rather than the kit's snapshot,
// which is why the modifier is set here and not on a.ctx.
func drivePreciseDrag(t *testing.T, a *App, x0, y0, x1, y1 int32) {
	t.Helper()
	driveMouse(a, x0, y0, true)
	sdl.SetModState(sdl.KMOD_LSHIFT)
	if !magnetBypassed() {
		sdl.SetModState(sdl.KMOD_NONE)
		driveMouse(a, x1, y1, false)
		t.Skip("this SDL build does not honour SetModState; the precise-drag gates need it")
	}
	driveMouse(a, (x0+x1)/2, (y0+y1)/2, true)
	driveMouse(a, x1, y1, true)
	sdl.SetModState(sdl.KMOD_NONE)
	driveMouse(a, x1, y1, false)
}

// editorFixtureCatalog publishes a catalog snapshot that puts the fixture theme in
// dir with the given origin — i.e. it answers the ONE question the save path asks.
//
// It waits for the real scan first. openThemeEditor kicks one so Save has an answer
// on hand, and that goroutine stores its own snapshot when it finishes; without the
// wait it would land over the fixture's a moment later and the gate would fail on a
// timing accident rather than on anything about the editor.
func editorFixtureCatalog(t *testing.T, a *App, dir string, origin themeOrigin) {
	t.Helper()
	deadline := time.Now().Add(editorGateSaveWait)
	for a.themeCatBusy.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	a.themeCat.Store(&themeCatalog{
		Entries:   []themeCatalogEntry{{Name: a.te.doc.name, Dir: dir, Origin: origin}},
		WriteRoot: dir,
	})
}

// editorWidgetTarget finds an AO2 widget the canvas can actually be pressed on: one
// whose painted centre is inside the canvas rect AND which the probe resolves to
// itself, so a gate can never silently measure a different box.
func editorWidgetTarget(t *testing.T, a *App) editTarget {
	t.Helper()
	lay := &a.themeLay
	keys := make([]string, 0, len(lay.r))
	for k := range lay.r {
		if editorSlotKeyEditable(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	canvas := editorCanvasRect(frameHarnessW, frameHarnessH)
	for _, k := range keys {
		cx, cy := editorCentreOf(lay.r[k])
		if !pointIn(cx, cy, canvas) {
			continue
		}
		if got, _ := a.editorProbe(lay, cx, cy); got == designTarget(k) {
			return got
		}
	}
	t.Fatal("no editable AO2 widget is reachable on the fixture canvas — every widget gate would be vacuous")
	return noTarget()
}

// ---------------------------------------------------------------------------
// The probe
// ---------------------------------------------------------------------------

// TestCanvasProbePutsElementsAheadOfWidgets is THE hazard W6 wrote down at the
// legacy probe's own site: a flat largest-wins loop hands every press on a small
// decoration to the big widget underneath it, and the decoration is unselectable by
// mouse for the whole wave.
//
// The fixture is exactly that shape — a 12x12 element parked on a full widget — and
// the gate asserts BOTH directions, because a priority that made widgets
// unreachable would be the same bug with the roles swapped.
func TestCanvasProbePutsElementsAheadOfWidgets(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	el := tgts[0]

	widget := editorWidgetTarget(t, a)
	box := editorPainted(t, a, widget)
	cx, cy := editorCentreOf(box)
	lay := &a.themeLay
	a.themeSidecar.Elements[el.elem].Rect = theme.Rect{
		X: int(float64(cx-lay.area.X)/lay.scaleX) - 6,
		Y: int(float64(cy-lay.area.Y)/lay.scaleY) - 6,
		W: 12, H: 12,
	}
	a.invalidateThemeCanvases()
	driveFrame(a)

	if got, _ := a.editorProbe(&a.themeLay, cx, cy); got != el {
		t.Fatalf("a press on a 12x12 element sitting on %s resolved to %v, want the element — "+
			"largest-wins across the two populations makes every decoration unselectable by mouse",
			widget.designKey(), got)
	}
	if got, _ := a.editorProbe(&a.themeLay, box.X+2, box.Y+2); got != widget {
		t.Fatalf("the widget's own pixels resolved to %v, want %v — space priority must not make "+
			"widgets unreachable either", got, widget)
	}
}

// TestCanvasProbeSkipsHiddenElementsAndTheClientTabStrip pins the two refusals §3.2
// states: a hidden element never hit-tests, and the server-tab strip is not part of
// any theme document.
func TestCanvasProbeSkipsHiddenElementsAndTheClientTabStrip(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	el := tgts[0]
	cx, cy := editorCentreOf(editorPainted(t, a, el))
	if got, _ := a.editorProbe(&a.themeLay, cx, cy); got != el {
		t.Fatalf("the visible element did not answer its own pixels (%v)", got)
	}

	a.themeSidecar.Elements[el.elem].Hidden = true
	a.invalidateThemeCanvases()
	driveFrame(a)
	if got, _ := a.editorProbe(&a.themeLay, cx, cy); got == el {
		t.Error("a HIDDEN element still hit-tests — you would be dragging something you cannot see")
	}

	if editorSlotKeyEditable(themeTabBarKey) {
		t.Errorf("the editor offers %s as an authorable widget — the server-tab strip is CLIENT "+
			"chrome whose placement is a preference and whose size is derived from live chips; no "+
			"[overrides] row can express it", themeTabBarKey)
	}
}

// ---------------------------------------------------------------------------
// Drag, resize, nudge
// ---------------------------------------------------------------------------

// TestCanvasDragMovesTheSelectedElement drives the wave's headline interaction end
// to end: press, move, release, through App.Frame.
func TestCanvasDragMovesTheSelectedElement(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	el := tgts[0]
	before := editorAuthored(t, a, el)
	cx, cy := editorCentreOf(editorPainted(t, a, el))

	drivePreciseDrag(t, a, cx, cy, cx+40, cy+24)

	after := editorAuthored(t, a, el)
	if after.X <= before.X || after.Y <= before.Y {
		t.Fatalf("the element is at %+v after a +40,+24 drag, want it moved right and down from %+v",
			after, before)
	}
	if after.W != before.W || after.H != before.H {
		t.Errorf("a MOVE changed the size (%+v -> %+v)", before, after)
	}
	if !a.te.selectedIs(el) {
		t.Error("the press did not select the box it grabbed")
	}
	if a.te.drag.active() {
		t.Error("the gesture is still live after the button came up")
	}
}

// TestCanvasDragIsOneUndoStepThroughTheRealChord is the coalescing gate at the level
// that matters: a multi-frame drag, undone by the chord HandleEvent actually
// delivers (c.hotkey, never c.keyPressed).
func TestCanvasDragIsOneUndoStepThroughTheRealChord(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	el := tgts[0]
	before := editorAuthored(t, a, el)
	cx, cy := editorCentreOf(editorPainted(t, a, el))

	drivePreciseDrag(t, a, cx, cy, cx+60, cy+36)
	if a.te.ring.n != 1 {
		t.Fatalf("a four-frame drag produced %d undo entries, want 1 — Ctrl+Z would step one frame at "+
			"a time through a gesture the user made once", a.te.ring.n)
	}

	a.ctx.hotkey = sdl.K_z
	if !a.editorUndoChord() {
		t.Fatal("the editor did not claim Ctrl+Z")
	}
	if got := editorAuthored(t, a, el); got != before {
		t.Fatalf("one Ctrl+Z left the element at %+v, want the pre-drag %+v", got, before)
	}
}

// TestDragHoldsOneUndoGroupForTheWholeGesture pins the mechanism the gate above
// rests on: the group id is part of the coalescing identity, so a group opened per
// FRAME would silently defeat the merge and fill the ring in two seconds.
func TestDragHoldsOneUndoGroupForTheWholeGesture(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	cx, cy := editorCentreOf(editorPainted(t, a, tgts[0]))

	driveMouse(a, cx, cy, true)
	if !a.te.drag.active() {
		t.Fatal("a press on an element did not start a gesture")
	}
	first := a.te.drag.group
	if first == 0 {
		t.Fatal("the gesture opened no undo group")
	}
	driveMouse(a, cx+20, cy+10, true)
	if got := a.te.drag.group; got != first {
		t.Fatalf("frame two of the drag runs in group %d, want the press's %d", got, first)
	}
	if got := a.te.ring.group; got != first {
		t.Fatalf("the ring's open group is %d mid-drag, want %d — a group that closes between frames "+
			"gives every frame a different coalescing key", got, first)
	}
	driveMouse(a, cx+20, cy+10, false)
	if a.te.ring.group != 0 {
		t.Error("the release did not close the gesture's group — the NEXT edit would undo with this drag")
	}
}

// TestEveryHandleResizesItsOwnEdges is W6's first named consumer: eight grips, each
// moving exactly the edges its mask names and nothing else.
//
// The fixture box is sized past editHandleRoomPx precisely so the full set is
// offered (see editorGateBoxPx) — on a cramped box the design keeps one grip, and a
// gate that used one would be measuring the fallback.
func TestEveryHandleResizesItsOwnEdges(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	el := tgts[0]
	origin := editorAuthored(t, a, el)
	for i := 0; i < handleCount; i++ {
		// The fixture box goes back to its authored rect between grips, so each handle
		// is measured from the same starting geometry.
		a.themeSidecar.Elements[el.elem].Rect = origin
		a.invalidateThemeCanvases()
		driveFrame(a)
		before := editorAuthored(t, a, el)
		mask := handleEdgeMask[i]
		hnd := classicHandles(editorPainted(t, a, el))[i]
		hx, hy := editorCentreOf(hnd)
		// Drag AWAY from the box, so every gripped edge GROWS and no minimum floors.
		var dx, dy int32
		if mask&edgeL != 0 {
			dx = -32
		}
		if mask&edgeR != 0 {
			dx = 32
		}
		if mask&edgeT != 0 {
			dy = -32
		}
		if mask&edgeB != 0 {
			dy = 32
		}
		drivePreciseDrag(t, a, hx, hy, hx+dx, hy+dy)
		after := editorAuthored(t, a, el)

		wantX, wantY := mask&edgeL != 0, mask&edgeT != 0
		wantW, wantH := mask&(edgeL|edgeR) != 0, mask&(edgeT|edgeB) != 0
		if (after.X != before.X) != wantX || (after.Y != before.Y) != wantY {
			t.Errorf("handle %d (mask %#x): origin went %+v -> %+v, want X moved=%v Y moved=%v",
				i, mask, before, after, wantX, wantY)
		}
		if (after.W != before.W) != wantW || (after.H != before.H) != wantH {
			t.Errorf("handle %d (mask %#x): size went %+v -> %+v, want W changed=%v H changed=%v — "+
				"seven of the eight grips were dead before W6 wired handleEdgeMask to the design path",
				i, mask, before, after, wantW, wantH)
		}
	}
}

// TestMultiDragMovesEverySelectedTargetByTheSameDelta is the anti-shear gate: two
// elements selected, one dragged, both must land at the same offset from where they
// started — a group translates by ONE delta, never by each box's own decisions.
func TestMultiDragMovesEverySelectedTargetByTheSameDelta(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{-140, 0}, [2]int{140, 0})
	defer cleanup()
	first, second := tgts[0], tgts[1]
	beforeA, beforeB := editorAuthored(t, a, first), editorAuthored(t, a, second)
	a.te.selectOnly(first)
	a.te.selectAdd(second)

	cx, cy := editorCentreOf(editorPainted(t, a, first))
	drivePreciseDrag(t, a, cx, cy, cx+48, cy+32)

	afterA, afterB := editorAuthored(t, a, first), editorAuthored(t, a, second)
	dxa, dya := afterA.X-beforeA.X, afterA.Y-beforeA.Y
	dxb, dyb := afterB.X-beforeB.X, afterB.Y-beforeB.Y
	if dxa == 0 && dya == 0 {
		t.Fatal("the dragged element did not move at all")
	}
	if dxa != dxb || dya != dyb {
		t.Fatalf("the group sheared: the dragged element moved (%d,%d) and its partner (%d,%d)",
			dxa, dya, dxb, dyb)
	}
	if a.te.selN != 2 {
		t.Errorf("the press collapsed a 2-box selection to %d — grabbing one member of a selection "+
			"must not throw the rest away", a.te.selN)
	}
}

// TestAnUnmodifiedDragSnapsWhereAPreciseOneDoesNot pins the Shift contract from both
// sides: an ordinary drag lands on the design grid or flush against a sibling (grid
// first, then magnet — the legacy editors' own order), and a Shift drag does neither.
func TestAnUnmodifiedDragSnapsWhereAPreciseOneDoesNot(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{3, 5}) // deliberately off-grid
	defer cleanup()
	el := tgts[0]
	cx, cy := editorCentreOf(editorPainted(t, a, el))

	driveMouse(a, cx, cy, true)
	driveMouse(a, cx+53, cy+37, true)
	moved := editorAuthored(t, a, el)
	caught := len(a.alignGuides)
	driveMouse(a, cx+53, cy+37, false)

	onGrid := moved.X%layoutGridDesign == 0 && moved.Y%layoutGridDesign == 0
	if !onGrid && caught == 0 {
		t.Fatalf("an unmodified drag landed at %d,%d — off the %d px grid AND with no magnet guide, "+
			"so neither snap ran", moved.X, moved.Y, layoutGridDesign)
	}
}

// TestArrowNudgeMovesTheSelectionOnTheEditorScreen drives the keyboard through the
// screen's own handler.
//
// The keys are claimed BEFORE the canvas draws, which is the point: the courtroom is
// this screen's canvas, and its own arrow consumers (the IC/OOC recall rings, the
// emote-preview cycler) draw first and would otherwise have spent the key — the same
// trap layoutnudge.go hoisted the legacy nudge out of a draw body for.
func TestArrowNudgeMovesTheSelectionOnTheEditorScreen(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	el := tgts[0]
	a.te.selectOnly(el)
	before := editorAuthored(t, a, el)

	const taps = 3
	for i := 0; i < taps; i++ {
		a.ctx.BeginFrame(frameHarnessDt)
		a.ctx.keyPressed = sdl.K_RIGHT
		a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
	}
	after := editorAuthored(t, a, el)
	if want := before.X + taps*layoutNudgePx; after.X != want {
		t.Fatalf("%d right-arrow taps moved X to %d, want %d", taps, after.X, want)
	}
	if a.te.ring.n != 1 {
		t.Errorf("a %d-tap nudge burst produced %d undo entries, want 1", taps, a.te.ring.n)
	}
}

// ---------------------------------------------------------------------------
// The AO2 tier — [overrides]
// ---------------------------------------------------------------------------

// TestDraggingAWidgetWritesAnOverrideRowNotAPreference pins the design's ownership
// split: the editor authors the THEME, the legacy Ctrl+F2 overlay tweaks the
// PLAYER's preferences, and neither writes into the other's store.
func TestDraggingAWidgetWritesAnOverrideRowNotAPreference(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t)
	defer cleanup()
	name, _ := a.d.Prefs.Theme()
	widget := editorWidgetTarget(t, a)
	key := widget.designKey()
	before := editorAuthored(t, a, widget)
	cx, cy := editorCentreOf(editorPainted(t, a, widget))

	drivePreciseDrag(t, a, cx, cy, cx+40, cy+24)

	row, ok := a.themeSidecar.OverrideRect(key)
	if !ok {
		t.Fatalf("dragging %s wrote no [overrides] row — the author's courtroom_design.ini must stay "+
			"byte-identical, so the edit has nowhere else to live", key)
	}
	if row == before {
		t.Fatalf("the row still holds the pre-drag rect %+v", before)
	}
	if live := a.themeRects[key]; live != row {
		t.Errorf("the resolved geometry is %+v while the row says %+v — an edit that writes only the "+
			"row does not move a pixel until the theme is switched and back", live, row)
	}
	if ov := a.d.Prefs.ThemeRectOverrides(name); len(ov) != 0 {
		t.Errorf("the editor wrote %d preference override(s) — authoring a theme must not change how "+
			"that theme behaves for somebody who only uses it", len(ov))
	}
}

// TestUndoOfAWidgetDragRemovesTheRowItCreated is the one-bit gate: an undo has to
// put the ROW back the way it was, not just the numbers. A row restating what the
// AO2 tier already says is a diff in the author's file for an edit they took back.
func TestUndoOfAWidgetDragRemovesTheRowItCreated(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t)
	defer cleanup()
	widget := editorWidgetTarget(t, a)
	key := widget.designKey()
	before := editorAuthored(t, a, widget)
	if _, had := a.themeSidecar.OverrideRect(key); had {
		t.Fatalf("%s already carries a row; this gate is about the row a drag CREATES", key)
	}
	cx, cy := editorCentreOf(editorPainted(t, a, widget))
	drivePreciseDrag(t, a, cx, cy, cx+40, cy+24)

	a.ctx.hotkey = sdl.K_z
	if !a.editorUndoChord() {
		t.Fatal("the editor did not claim Ctrl+Z")
	}
	if _, still := a.themeSidecar.OverrideRect(key); still {
		t.Errorf("undoing the drag left an [overrides] row for %s behind", key)
	}
	if got := a.themeRects[key]; got != before {
		t.Errorf("undo left the resolved rect at %+v, want %+v", got, before)
	}
}

// editorHoldFrames is how long the hold-still gate keeps the button down without
// moving the pointer. Several, not one: the FIRST drag frame legitimately moves the
// box (the grid snaps it), and it is every frame after that — the ones a human hand
// spends not moving — that asks for a rect the model already holds.
const editorHoldFrames = 5

// TestHoldingAWidgetStillNeverCriesRefused is the false-alarm gate.
//
// THE DEFECT IT CLOSES: editorApply exempted only opElemField from the refusal chip,
// while applySlotRect refuses a no-change request by exactly the same rule — so the
// second and every later frame of a widget gesture that holds still (i.e. every real
// human drag) and every frame a drag sat against clampDesignRectToCanvas posted
// "this slot rect was refused — a cap or a lock is in the way", which then stayed on
// the rail for editStatusMs. That inverts design §3.3's own rule (name the offender,
// name the limit) into naming a limit that is not there, on the wave's headline
// interaction.
//
// The mutation it answers to: chipping on any result other than applyRefused.
func TestHoldingAWidgetStillNeverCriesRefused(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t)
	defer cleanup()
	widget := editorWidgetTarget(t, a)
	cx, cy := editorCentreOf(editorPainted(t, a, widget))

	driveMouse(a, cx, cy, true) // the press
	if !a.te.drag.active() {
		t.Fatal("a press on an editable AO2 widget started no gesture — the gate would hold nothing still")
	}
	if !a.te.selectedIs(widget) {
		t.Fatalf("the press selected %v rather than %v", a.te.primary(), widget)
	}
	a.te.status, a.te.statusAt = "", time.Time{} // the press itself is not what is under test
	for i := 0; i < editorHoldFrames; i++ {
		driveMouse(a, cx, cy, true) // the hand, not moving
		if a.te.status != "" {
			t.Fatalf("hold frame %d posted %q — a drag that changes nothing is not a refusal, and this "+
				"chip sits on the rail for %d ms telling the user about a cap that is not involved",
				i+1, a.te.status, editStatusMs)
		}
	}
	driveMouse(a, cx, cy, false) // the release
	if a.te.status != "" {
		t.Fatalf("the release posted %q", a.te.status)
	}

	// …and the rule itself, asked straight through the funnel. The loop above holds a
	// real gesture still, which is the reported symptom, but where the box lands
	// depends on the grid and the magnet; this cannot be flaky and cannot be vacuous.
	// A request for the rect the widget already holds is precisely what every
	// motionless frame of a drag produces.
	cur, ok := a.te.doc.slotRect(widget.designKey())
	if !ok {
		t.Fatalf("the document does not own %s", widget.designKey())
	}
	if a.editorApply(mutateSlotRect(widget, cur)) {
		t.Fatal("a request for the rect the widget already holds reported an edit — it would take an " +
			"undo step and mark the theme dirty for a gesture that moved nothing")
	}
	if a.te.status != "" {
		t.Fatalf("a no-change request posted %q — nothing refused it, so there is no offender to name "+
			"and no limit to state", a.te.status)
	}
}

// TestAGenuineCapRefusalStillChips is the other half of the same seam, and it is what
// keeps the fix above from becoming "the editor never complains".
//
// A widget with no [overrides] row needs an APPEND, and theme.OverrideCap refuses one
// past its bound (setOverrideRow refuses, it never evicts — the reader's own ruling).
// That is a real limit with a name, so the rail must say so: silence here is an edit
// the user makes, does not get, and is told nothing about.
func TestAGenuineCapRefusalStillChips(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t)
	defer cleanup()
	widget := editorWidgetTarget(t, a)
	key := widget.designKey()
	if _, had := a.themeSidecar.OverrideRect(key); had {
		t.Fatalf("%s already carries a row; this gate is about the row a first edit must APPEND", key)
	}
	// Fill [overrides] to its cap with rows addressing keys no layout carries, so the
	// fixture's own geometry is untouched (sidecarOverrideTarget refuses them) and the
	// only thing under test is the append.
	for i := len(a.themeSidecar.Overrides); i < theme.OverrideCap; i++ {
		a.themeSidecar.Overrides = append(a.themeSidecar.Overrides,
			theme.KeyRect{Key: "w7acapfill" + itoa32(int32(i)), Rect: theme.Rect{W: 1, H: 1}})
	}
	base, ok := a.te.doc.slotRect(key)
	if !ok {
		t.Fatalf("the document does not own %s", key)
	}
	// A rect that really differs AFTER the clamp, tried both ways: a widget already
	// flush with the canvas edge would clamp straight back to `base`, and the gate
	// would then be measuring a no-op instead of a cap.
	moved := base
	moved.X += editorGateBoxPx
	want := a.editorClampGeometry(widget, moved)
	if want == base {
		moved.X = base.X - editorGateBoxPx
		want = a.editorClampGeometry(widget, moved)
	}
	if want == base {
		t.Fatalf("no clamped rect differs from %s's own %+v — the gate would measure a no-op", key, base)
	}
	a.te.selectOnly(widget)
	a.te.status, a.te.statusAt = "", time.Time{}

	if a.editorApply(mutateSlotRect(widget, want)) {
		t.Fatalf("the funnel accepted a %d'th [overrides] row past the cap of %d",
			len(a.themeSidecar.Overrides)+1, theme.OverrideCap)
	}
	if a.te.status == "" {
		t.Fatal("a genuine theme.OverrideCap refusal was silent — the edit did not land and nothing on " +
			"screen says why, which is the bare \"failed\" design §3.3 forbids")
	}
}

// TestWidgetEditsSurviveAThemeReapply is the slot half of the durability seam. The
// element half is TestEditsSurviveAThemeReapply; this one exercises the GEOMETRY
// tier, which lives somewhere else entirely (a.themeRects) and is replaced wholesale
// by the same background apply.
func TestWidgetEditsSurviveAThemeReapply(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t)
	defer cleanup()
	widget := editorWidgetTarget(t, a)
	key := widget.designKey()
	cx, cy := editorCentreOf(editorPainted(t, a, widget))
	drivePreciseDrag(t, a, cx, cy, cx+40, cy+24)
	edited := a.themeRects[key]

	// The apply, exactly as pollThemeApply does it: a freshly resolved map lands over
	// the live one carrying only what is on DISK…
	a.themeRects = cloneRects(a.themeRectsOrig)
	// …and the reclaim puts the document back on top of it.
	a.reclaimThemeDoc()

	if got := a.themeRects[key]; got != edited {
		t.Fatalf("a background theme apply discarded the unsaved widget edit (%+v, want %+v) — applies "+
			"are not user-initiated, so this is work vanishing with nothing on screen to say why",
			got, edited)
	}
	if got := a.te.doc.live[key]; got != edited {
		t.Errorf("the document is still bound to the replaced map (it reads %+v, want %+v)", got, edited)
	}
}

// ---------------------------------------------------------------------------
// Leaving, and saving
// ---------------------------------------------------------------------------

// TestBackTwiceDiscardsUnsavedEditsAndOnceDoesNot pins the exit contract: work is
// never thrown away by a single press, and the confirmation costs no modal (the
// design allows exactly two in the whole editor and spends both elsewhere).
func TestBackTwiceDiscardsUnsavedEditsAndOnceDoesNot(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	el := tgts[0]
	baseline := editorAuthored(t, a, el)
	a.te.selectOnly(el)
	a.editorNudgeSelection(1, 0, false)
	edited := editorAuthored(t, a, el)
	if edited == baseline {
		t.Fatal("the nudge did not change anything; the gate would pass vacuously")
	}

	a.requestEditorExit()
	if a.te == nil {
		t.Fatal("one Back press with unsaved edits LEFT — a keystroke must not be able to discard work")
	}
	if got := editorAuthored(t, a, el); got != edited {
		t.Fatalf("the arming press already reverted the document (%+v)", got)
	}
	a.requestEditorExit()
	if a.te != nil {
		t.Fatal("the second Back press did not leave")
	}
	if got := a.themeSidecar.Elements[el.elem].Rect; got != baseline {
		t.Fatalf("leaving without saving left the theme at %+v, want the state it was opened in %+v — "+
			"an unsaved edit that survives the exit is an edit the user cannot get rid of", got, baseline)
	}
	if a.screen != ScreenCourtroom {
		t.Errorf("the exit landed on screen %d, want the screen the editor was opened FROM (%d)",
			a.screen, ScreenCourtroom)
	}
}

// TestExitWithNoEditsLeavesImmediately is the other side: a confirmation that fires
// when there is nothing to confirm is a broken Back button.
func TestExitWithNoEditsLeavesImmediately(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t)
	defer cleanup()
	a.requestEditorExit()
	if a.te != nil {
		t.Fatal("Back on a clean document did not leave")
	}
}

// TestSaveWritesTheSidecarThroughTheGuard drives the real save: the bytes come from
// Sidecar.Bytes (which validates and refuses before writing anything), the path from
// the catalog, and the file that lands parses back through the real reader with the
// edit in it.
func TestSaveWritesTheSidecarThroughTheGuard(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	el := tgts[0]
	dir := t.TempDir()
	editorFixtureCatalog(t, a, dir, themeOriginYours)
	a.te.selectOnly(el)
	a.editorNudgeSelection(1, 0, false)
	want := a.themeSidecar.Elements[el.elem].Rect

	a.editorSave()
	if !a.editorAwaitSave(editorGateSaveWait) {
		t.Fatalf("the save did not land: %s", a.te.status)
	}
	if a.te.doc.dirty {
		t.Error("the document is still dirty after a successful save")
	}
	b, err := os.ReadFile(filepath.Join(dir, theme.SidecarFileName))
	if err != nil {
		t.Fatalf("the save wrote no file: %v", err)
	}
	sc, err := theme.ParseSidecar(b)
	if err != nil {
		t.Fatalf("the saved file does not read back: %v — Bytes() validates before it writes, so this "+
			"cannot happen unless the guard was bypassed", err)
	}
	found := false
	for i := range sc.Elements {
		if sc.Elements[i].ID != "gatea" {
			continue
		}
		found = true
		if sc.Elements[i].Rect != want {
			t.Errorf("the saved element is at %+v, want the edited %+v", sc.Elements[i].Rect, want)
		}
	}
	if !found {
		t.Error("the saved theme has no element named gatea — the edit did not reach the file")
	}
}

// TestSaveRefusesAThemeThatIsNotYours is design §6 R3's guard: the highest-blast-
// radius risk in the whole feature is the editor writing into somebody else's
// folder, and the refusal names the offender and offers the fix rather than failing
// quietly.
func TestSaveRefusesAThemeThatIsNotYours(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	dir := t.TempDir()
	editorFixtureCatalog(t, a, dir, themeOriginBundled)
	a.te.selectOnly(tgts[0])
	a.editorNudgeSelection(1, 0, false)

	a.editorSave()
	if a.te.save != nil {
		t.Fatal("a bundled theme started a write")
	}
	if _, err := os.Stat(filepath.Join(dir, theme.SidecarFileName)); !os.IsNotExist(err) {
		t.Fatal("the editor wrote into a theme folder that is not ours")
	}
	if a.te.status == "" {
		t.Error("the refusal was silent — name the offender, name the limit, offer the fix")
	}
}

// ---------------------------------------------------------------------------
// Encapsulation
// ---------------------------------------------------------------------------

// TestOnlyTheDocumentWritesTheOverridesTier is the anti-second-writer census for the
// geometry tier — the twin of TestOnlyTheDocumentUsesTheFieldTables for elements.
//
// themeDoc.writeSlotRect is the ONE function that may move an AO2 widget in a
// document under edit. Anything else reaching into Sidecar.Overrides is a writer
// with no undo entry and no live map update: half an edit, silently.
func TestOnlyTheDocumentWritesTheOverridesTier(t *testing.T) {
	const owner = "themeeditordoc.go"
	needles := []string{".Overrides = append", ".Overrides[i].Rect ="}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing the package: %v", err)
	}
	for _, name := range files {
		if name == owner || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, needle := range needles {
			if strings.Contains(string(src), needle) {
				t.Errorf("%s writes Sidecar.Overrides directly (%q) — every geometry edit must go "+
					"through themeDoc.writeSlotRect, which is what fills in the undo entry and keeps "+
					"the row and the resolved rect in step", name, needle)
			}
		}
	}
	own, err := os.ReadFile(owner)
	if err != nil {
		t.Fatalf("reading %s: %v", owner, err)
	}
	for _, needle := range needles {
		if !strings.Contains(string(own), needle) {
			t.Fatalf("the census no longer matches %s (%q is gone) — it would pass vacuously", owner, needle)
		}
	}
}
