package ui

// THE EDITOR'S CANVAS (v1.90.0 W7a) — docs/wip/THEME-EDITOR-DESIGN.md §3.2.
//
// ONE concern: what the POINTER means over the live frame. The screen's chrome is
// themeeditor.go, the document themeeditordoc.go, the timeline themeeditorop.go;
// this file turns a press, a drag and a release into ops on that timeline.
//
// SPACE PRIORITY, NOT LARGEST-WINS. W6 left the trap written down at the legacy
// overlay's own probe (layoutedit.go): that probe walks the DESIGN-KEY set and
// resolves ties by the largest gripped box, which is right when every candidate is
// an AO2 widget. Free elements are a second population in the same pixels and they
// invert both rules — an element is authored ON TOP of the widget it decorates
// (that is what a decoration is) and a theme's densest elements are its smallest —
// so appending them to one flat largest-wins loop would hand every press on a 12x12
// badge to the 490x98 emote grid underneath it, and the badge would be unselectable
// by mouse for the whole wave. Elements are therefore probed FIRST, as their own
// population, with paint order (band, then z, then declaration) deciding inside it;
// design keys are probed only when no element answered, keeping their own tie-break.
// A theme with no elements takes byte-identical decisions to the legacy overlay's.
//
// ONE GESTURE, ONE UNDO STEP. Every frame of a drag calls the same editorApply the
// inspector and the keyboard use, so the ring's coalescing window merges the whole
// gesture into one op — and the group opened at the press is held OPEN for the
// gesture's whole life rather than reopened per frame, because the group id is part
// of the coalescing identity: a fresh group each frame would give every frame a
// different key, defeat the merge, and fill a 128-entry ring in two seconds of
// dragging three elements.
//
// THE GESTURE RUNS IN AUTHORED UNITS, WITH THE MAGNET FOLDED BACK. The numbers we
// write are the authored ones (design px for a widget or a courtroom-space element,
// window px for a `space = window` element), so the grid means the design grid and a
// stored value is never the round trip of a scaled one — the rounding trap the
// server-tab strip's whole drag path exists to document. The MAGNET is the one step
// that cannot live there: an element's rect is a signed delta on its anchor (design
// §6.6 R1), so two elements' authored numbers are not comparable at all, while their
// painted boxes always are. So the magnet runs on the PAINTED projection and its
// correction is divided back out — one conversion, at one step, named here.

import (
	"fmt"
	"math"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Named constants (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// editGestureNone / Move / Resize are the three states of the pointer.
	editGestureNone uint8 = iota
	editGestureMove
	editGestureResize
)

const (
	// editCanvasRestAlpha is the alpha every editable box is outlined at when the
	// pointer is elsewhere. Faint: a forty-widget theme outlined at full strength is
	// three hundred bright lines and no theme underneath them.
	editCanvasRestAlpha = 70
	// editCanvasHotAlpha is the outline alpha for the box under the cursor and for
	// the selection — the classic editor's own doctrine, a quiet outline at rest and
	// the full treatment on the box being worked.
	editCanvasHotAlpha = 255
	// editReadoutH is the gap the geometry readout keeps from the window's bottom
	// edge, in px.
	editReadoutH = 22
)

// ---------------------------------------------------------------------------
// The gesture
// ---------------------------------------------------------------------------

// editGesture is one press-drag-release. A VALUE on the editor, so it dies with the
// screen and costs a closed editor nothing.
//
// base holds the authored rect of EVERY selected target as it stood at the press —
// a fixed array, never a slice, for the same reason the selection is one. Recording
// the whole selection at the press is what makes a multi-target move rigid: every
// frame recomputes from the same origin, so a rounding wobble can never accumulate
// into shear between two elements that started aligned.
type editGesture struct {
	kind  uint8
	edges uint8
	// start is the press point in window px.
	start [2]int32
	// group is the ring group the whole gesture pushes into. Held for the gesture's
	// whole life (see editorBeginGesture) and read by the gate that pins it.
	group uint32
	base  [themeSelCap]theme.Rect
	baseN int
}

func (g *editGesture) active() bool { return g.kind != editGestureNone }

// ---------------------------------------------------------------------------
// Target geometry — the one place the three spaces are reconciled
// ---------------------------------------------------------------------------

// editTargetGeom is everything a gesture needs to know about one target: the
// numbers it edits, the box those numbers paint as, and the ratio between them.
type editTargetGeom struct {
	authored theme.Rect
	painted  sdl.Rect
	sx, sy   float64
}

// editorTargetGeom resolves a target. ok=false for anything that is not on the
// canvas this frame — an inert element, a widget this build does not place, a
// document that does not own the key.
//
// IT IS THE ONLY PLACE THE SPACES DIVERGE. Everything downstream works in authored
// units plus a scale, so the drag, the resize, the magnet fold-back and the readout
// are one implementation for widgets and elements alike.
//
// A BUILT CACHE, NOT A VALID ONE, and the difference is deliberate: every write
// this file makes invalidates the layout (the geometry it edits is what the cache
// derives), so a `lay.valid` test here would make the SECOND target of a multi-drag
// — and the whole overlay that draws after the edit — silently resolve to nothing.
// The contract is instead that the caller obtained lay from themeWindowLayout THIS
// frame, so it is either fresh or freshly invalidated by us, and the two differ only
// in the numbers this gesture is about to overwrite anyway. drawThemeEditor re-asks
// for the cache between the input and the overlay, which is what rebuilds it.
func (a *App) editorTargetGeom(lay *themeLayoutCache, t editTarget) (editTargetGeom, bool) {
	if a.te == nil || lay == nil || lay.scaleX <= 0 || lay.scaleY <= 0 {
		return editTargetGeom{}, false
	}
	switch t.space {
	case spaceDesign:
		key := t.designKey()
		if !editorSlotKeyEditable(key) {
			return editTargetGeom{}, false
		}
		authored, ok := a.te.doc.slotRect(key)
		if !ok {
			return editTargetGeom{}, false
		}
		painted, drawn := lay.rect(key)
		if !drawn {
			return editTargetGeom{}, false
		}
		return editTargetGeom{authored: authored, painted: painted, sx: lay.scaleX, sy: lay.scaleY}, true
	case spaceElement:
		el := a.te.doc.element(t)
		if el == nil || el.Inert() {
			return editTargetGeom{}, false
		}
		painted, ok := a.resolveElementRect(lay, a.te.doc.side, el)
		if !ok || painted.W <= 0 || painted.H <= 0 {
			return editTargetGeom{}, false
		}
		// A `space = window` element is window px end to end (resolveElementRect's own
		// first arm): no canvas fit, no scale, so its authored number IS its painted
		// one and dividing by the canvas scale would move it at the wrong rate.
		sx, sy := lay.scaleX, lay.scaleY
		if el.Space == theme.SpaceWindow {
			sx, sy = 1, 1
		}
		return editTargetGeom{authored: el.Rect, painted: painted, sx: sx, sy: sy}, true
	}
	return editTargetGeom{}, false
}

// editorSlotKeyEditable reports that the EDITOR may author a design key.
//
// It is themeKeyEditable plus one refusal: the server-tab strip is CLIENT chrome,
// not part of any theme. Its placement is a preference (tabStripPlacementRecorded),
// its size is derived from the live chips rather than authored, and its whole drag
// runs in screen space with a stored value that is the inverse of a painted box —
// three properties no [overrides] row can express, and one the AO2-parity rule says
// a theme has no business declaring anyway. The legacy Ctrl+F2 overlay keeps owning
// it; the editor does not offer it at all, which is why nothing downstream of here
// carries the strip's special cases.
func editorSlotKeyEditable(key string) bool {
	return key != "" && key != themeTabBarKey && themeKeyEditable(key)
}

// project maps an authored rect back onto the screen, given the geometry the press
// captured. The base pair is a fixed point of the map by construction (base.authored
// projects to base.painted exactly), so a gesture that ends where it started ends
// with the SAME numbers it started with — the property the tab strip's own drag path
// had to be rewritten twice to get.
func (g editTargetGeom) project(r theme.Rect) sdl.Rect {
	return sdl.Rect{
		X: g.painted.X + int32(float64(r.X-g.authored.X)*g.sx),
		Y: g.painted.Y + int32(float64(r.Y-g.authored.Y)*g.sy),
		W: g.painted.W + int32(float64(r.W-g.authored.W)*g.sx),
		H: g.painted.H + int32(float64(r.H-g.authored.H)*g.sy),
	}
}

// unproject converts a painted-space correction back into authored units. Rounded,
// not truncated: a −0.4 px correction must come back as 0 rather than as 0 in one
// direction and −1 in the other, which is how a magnet ends up nudging a box every
// frame it fails to catch anything.
func unprojectDelta(d int32, scale float64) int {
	if scale <= 0 {
		return int(d)
	}
	return int(math.Round(float64(d) / scale))
}

// ---------------------------------------------------------------------------
// The probe
// ---------------------------------------------------------------------------

// editorProbe answers "what is under (mx, my)": the target, and the resize edges a
// press there would grip (0 = a move).
//
// See the file header for why elements go first. Within each population:
//
//   - a RESIZE grip wins over a move, and among grips the LARGEST box wins, so a big
//     box's corner cannot be blocked by a small box sitting on it (the legacy rule,
//     kept verbatim);
//   - among moves, paint order decides for elements (topmost band, then highest z,
//     then the smallest box) and the smallest box decides for widgets (the legacy
//     stack's own sort), because a widget stack has no z at all.
func (a *App) editorProbe(lay *themeLayoutCache, mx, my int32) (editTarget, uint8) {
	if a.te == nil || lay == nil {
		return noTarget(), 0
	}
	if t, edges, ok := a.editorProbeElements(lay, mx, my); ok {
		return t, edges
	}
	if t, edges, ok := a.editorProbeSlots(lay, mx, my); ok {
		return t, edges
	}
	return noTarget(), 0
}

// editorProbeElements walks the document's own elements. A HIDDEN element never
// hit-tests (design §3.2) — Inert() covers it, and editorTargetGeom refuses it.
func (a *App) editorProbeElements(lay *themeLayoutCache, mx, my int32) (editTarget, uint8, bool) {
	best, bestEdges, bestGeom := noTarget(), uint8(0), editTargetGeom{}
	bestSrc := (*theme.Element)(nil)
	for i := 0; i < a.te.doc.elementCount(); i++ {
		t := elementTarget(i)
		g, ok := a.editorTargetGeom(lay, t)
		if !ok {
			continue
		}
		el := a.te.doc.element(t)
		edges := handleGripAt(g.painted, resizeEdgesFor(t), mx, my)
		if edges == 0 && !pointIn(mx, my, g.painted) {
			continue
		}
		if best.armed() && !elemPickBeats(el, g, bestSrc, bestGeom, edges != 0, bestEdges != 0) {
			continue
		}
		best, bestEdges, bestGeom, bestSrc = t, edges, g, el
	}
	return best, bestEdges, best.armed()
}

// elemPickBeats is the element population's ordering, in one place so the probe reads
// as a policy rather than as a comparison chain.
//
// A GRIP ALWAYS BEATS A PLAIN HIT, then paint order (band, z, declaration index —
// the same three keys the bake sorts by), then the smaller box. The last one is what
// keeps a full-canvas wallpaper from swallowing every press inside it.
func elemPickBeats(el *theme.Element, g editTargetGeom, bestEl *theme.Element, best editTargetGeom, grip, bestGrip bool) bool {
	if grip != bestGrip {
		return grip
	}
	if grip {
		// Among grips the LARGEST box wins, so a big element's corner is reachable
		// through a small one parked on it — the legacy probe's rule, kept.
		return area64(g.painted) > area64(best.painted)
	}
	if el.Band != bestEl.Band {
		return el.Band > bestEl.Band
	}
	if el.Z != bestEl.Z {
		return el.Z > bestEl.Z
	}
	return area64(g.painted) <= area64(best.painted)
}

// editorProbeSlots walks the AO2 widgets: the smallest box wins a move and the
// largest gripped box wins a resize, which is exactly the legacy overlay's pair of
// rules — its stack sorts smallest-first for the move and takes the largest grip.
func (a *App) editorProbeSlots(lay *themeLayoutCache, mx, my int32) (editTarget, uint8, bool) {
	best, bestEdges, bestRect := noTarget(), uint8(0), sdl.Rect{}
	for key, r := range lay.r {
		if !editorSlotKeyEditable(key) || r.W <= 0 || r.H <= 0 {
			continue
		}
		t := designTarget(key)
		edges := handleGripAt(r, resizeEdgesFor(t), mx, my)
		if edges == 0 && !pointIn(mx, my, r) {
			continue
		}
		if best.armed() && !slotPickBeats(key, r, edges != 0, best.designKey(), bestRect, bestEdges != 0) {
			continue
		}
		best, bestEdges, bestRect = t, edges, r
	}
	return best, bestEdges, best.armed()
}

// slotPickBeats is the widget population's ordering.
//
// THE KEY IS THE FINAL TIE-BREAK, and it is not decoration: lay.r is a MAP, so a
// theme with two identical editable rects under the cursor would otherwise resolve
// to whichever one Go's randomised iteration happened to reach first — a different
// widget on different frames, which reads as the editor picking at random.
func slotPickBeats(key string, r sdl.Rect, grip bool, bestKey string, best sdl.Rect, bestGrip bool) bool {
	if grip != bestGrip {
		return grip
	}
	if area64(r) != area64(best) {
		if grip {
			return area64(r) > area64(best) // a big box's grip beats a small box parked on it
		}
		return area64(r) < area64(best) // the smallest box is the one the user meant
	}
	return key < bestKey
}

func area64(r sdl.Rect) int64 { return int64(r.W) * int64(r.H) }

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

// editorCanvasRect is the region of the window the canvas owns for INPUT: the frame
// minus the header band and the two rails.
//
// PUBLISH THE RECT THAT IS ACTUALLY PAINTED (overlayfence.go's rule, applied by
// arithmetic rather than by a fence): the rails are opaque chrome drawn over the
// canvas, so a press on them belongs to them even when a theme's element extends
// underneath. Without this the same click would both select an element and press an
// inspector button.
func editorCanvasRect(w, h int32) sdl.Rect {
	r := sdl.Rect{X: editRailW, Y: editBannerH, W: w - editRailW*2, H: h - editBannerH}
	if r.W < 0 { // a window narrower than both rails: no canvas to press on at all
		r.W = 0
	}
	return r
}

// editorCanvasInput resolves this frame's pointer over the canvas.
//
// Order: finish or advance a live gesture FIRST (a drag that left the canvas rect,
// or crossed a rail, must keep running — the button is still down), then take a new
// press.
func (a *App) editorCanvasInput(w, h int32, lay *themeLayoutCache) {
	c := a.ctx
	if a.te == nil {
		return
	}
	g := &a.te.drag
	if g.active() {
		if c.mouseDown {
			a.editorDragFrame(lay)
		} else {
			a.editorEndGesture()
		}
		return
	}
	if lay == nil || !lay.valid {
		return
	}
	press := c.mouseDown && !c.prevMouseDown
	if !press || !pointIn(c.mouseX, c.mouseY, editorCanvasRect(w, h)) {
		return
	}
	// A PANEL IS UP, so the canvas behind it is CLICK-DEAD (design §3.1). The rails go
	// dead through c.modalOn, which hovering() honours — but this probe hit-tests with
	// raw pointIn and would see straight through that, so it refuses the press itself.
	//
	// The shape it closes: editorFontPanelRect centres the panel INSIDE the canvas rect,
	// so every `<` / `>` / `x` on a font row also ran editorProbe + editorBeginGesture
	// on whatever element or AO2 widget sat beneath it — the art moved while a face was
	// being picked. A live gesture is untouched (it returned above): the button is still
	// down, and a drag that started before the panel opened must still be able to end.
	if a.editorPanelUp() {
		return
	}
	// The compact toolbox hit-tests with raw pointIn and therefore sees through every
	// fence — the same guard the legacy editor's press site carries, and for the same
	// reason: a press on its grip must not also grab whatever the theme parked under
	// the bottom-right corner.
	if a.editOverToolbox(w, h) {
		return
	}
	t, edges := a.editorProbe(lay, c.mouseX, c.mouseY)
	if !t.armed() {
		// Empty canvas: clear, unless a modifier says the user is building a
		// selection. (The marquee that would otherwise start here is W7b.)
		if !c.ctrlHeld && !c.shiftHeld {
			a.te.selectClear()
		}
		return
	}
	switch {
	case c.ctrlHeld:
		a.te.selectToggle(t)
		return // a modifier press EDITS the selection; it never starts a drag
	case c.shiftHeld:
		a.te.selectAdd(t)
		return
	}
	if !a.te.selectedIs(t) {
		a.te.selectOnly(t)
	}
	a.editorBeginGesture(lay, t, edges)
}

// editorBeginGesture captures the press.
//
// It records the authored rect of the WHOLE selection, not just the pressed target,
// because a multi-target move recomputes every frame from these origins (see
// editGesture). A locked target records its rect too and is simply never written —
// that keeps the indices of base[] and sel[] the same array position, which is what
// lets the drag loop be a single indexed walk.
func (a *App) editorBeginGesture(lay *themeLayoutCache, t editTarget, edges uint8) {
	e := a.te
	g := &e.drag
	*g = editGesture{}
	if edges != 0 && resizeEdgesFor(t)&edges == edges {
		g.kind, g.edges = editGestureResize, edges
	} else {
		g.kind = editGestureMove
	}
	g.start = [2]int32{a.ctx.mouseX, a.ctx.mouseY}
	// The PRIMARY is the pressed target, so the gesture is led by the box under the
	// cursor rather than by whatever happens to be first in the selection.
	e.selectPrimary(t)
	for i := 0; i < e.selN; i++ {
		if geom, ok := a.editorTargetGeom(lay, e.sel[i]); ok {
			g.base[i] = geom.authored
		}
	}
	g.baseN = e.selN
	// ONE GROUP FOR THE WHOLE GESTURE. Held open until the release: the group id is
	// part of the coalescing identity, so a per-frame group would give every frame a
	// different key and a hundred-frame drag would become a hundred undo steps.
	g.group = e.ring.begin()
}

// editorDragFrame is one frame of a live gesture.
func (a *App) editorDragFrame(lay *themeLayoutCache) {
	e := a.te
	g := &e.drag
	if lay == nil || !lay.valid || g.baseN == 0 {
		return
	}
	primary, ok := a.editorTargetGeom(lay, e.primary())
	if !ok {
		return
	}
	// THE BASE IS THE PRESS-TIME RECT, never the live one: recomputing the whole
	// gesture from its origin every frame is what keeps a drag continuous under a
	// grid, a magnet and a clamp that each round. Accumulating deltas instead makes
	// every rounded frame the next frame's starting point, which is how a snapped
	// drag creeps away from the cursor.
	base := g.base[0]
	dx := unprojectDelta(a.ctx.mouseX-g.start[0], primary.sx)
	dy := unprojectDelta(a.ctx.mouseY-g.start[1], primary.sy)

	r := base
	if g.kind == editGestureResize {
		r = resizeDesignRect(base, g.edges, dx, dy)
	} else {
		r.X, r.Y = base.X+dx, base.Y+dy
	}
	// SHIFT IS PIXEL-PRECISE: it bypasses the grid AND the magnet together, exactly
	// as both legacy editors gate them on one check.
	if !magnetBypassed() {
		if g.kind == editGestureResize {
			r = snapDesignResize(r, g.edges)
		} else {
			r.X, r.Y = snapDesign(r.X), snapDesign(r.Y)
		}
		r = a.editorMagnet(lay, primary, r, g.kind, g.edges)
	}
	r = a.editorClampGeometry(e.primary(), r)
	appliedX, appliedY := r.X-base.X, r.Y-base.Y
	a.editorWriteGeometry(e.primary(), r)
	if g.kind == editGestureResize {
		return // a resize is the primary's alone; a multi-box resize is not one gesture
	}
	// EVERY OTHER SELECTED TARGET TAKES THE SAME APPLIED DELTA, in its own units. The
	// delta is what the primary actually moved — post-grid, post-magnet, post-clamp —
	// so the group translates rigidly instead of each box making its own decisions
	// and shearing apart, which is the classic multi-drag bug.
	for i := 1; i < g.baseN && i < e.selN; i++ {
		t := e.sel[i]
		geom, ok := a.editorTargetGeom(lay, t)
		if !ok {
			continue
		}
		next := g.base[i]
		next.X += editorRescale(appliedX, primary.sx, geom.sx)
		next.Y += editorRescale(appliedY, primary.sy, geom.sy)
		a.editorWriteGeometry(t, a.editorClampGeometry(t, next))
	}
}

// editorRescale carries a delta expressed in one target's units into another's. It
// is the identity for every pair in the same space — which is every ordinary
// multi-selection — and only does arithmetic when a `space = window` element is
// dragged alongside a design-space one.
func editorRescale(d int, from, to float64) int {
	if from == to || from <= 0 || to <= 0 {
		return d
	}
	return int(math.Round(float64(d) * from / to))
}

// editorMagnet snaps the candidate flush to its siblings, in PAINTED space, and
// folds the correction back into authored units (see the file header).
//
// The sibling set is every OTHER editable box on the canvas — widgets and elements
// together — because "flush with the log column" and "flush with that badge" are the
// same request.
//
// CANVAS-LOCAL, not window-absolute: alignRect's extent argument is a width and a
// height measured from the origin, so feeding it painted window coordinates with the
// canvas's own W/H would put the "right edge" magnet at x = canvas width instead of
// at the canvas's right edge — every letterboxed fit mode off by the bar. Shifting
// every box by the canvas origin costs one subtraction each and makes the extent
// mean the stage. The guides come back local too; the draw adds the origin back.
func (a *App) editorMagnet(lay *themeLayoutCache, primary editTargetGeom, r theme.Rect, kind, edges uint8) theme.Rect {
	self := a.te.primary()
	ox, oy := lay.area.X, lay.area.Y
	a.themeAlignScratch = a.themeAlignScratch[:0]
	for key, box := range lay.r {
		if !editorSlotKeyEditable(key) || designTarget(key) == self || box.W <= 0 || box.H <= 0 {
			continue
		}
		box.X, box.Y = box.X-ox, box.Y-oy
		a.themeAlignScratch = append(a.themeAlignScratch, box)
	}
	for i := 0; i < a.te.doc.elementCount(); i++ {
		t := elementTarget(i)
		if t == self {
			continue
		}
		if g, ok := a.editorTargetGeom(lay, t); ok {
			g.painted.X, g.painted.Y = g.painted.X-ox, g.painted.Y-oy
			a.themeAlignScratch = append(a.themeAlignScratch, g.painted)
		}
	}
	painted := primary.project(r)
	painted.X, painted.Y = painted.X-ox, painted.Y-oy
	var moveEdges uint8
	if kind == editGestureResize {
		moveEdges = edges
	}
	// EVERY HANDLE FEEDS THE MAGNET (design §W6): alignRect is handed the gripped
	// edges, not the constant bottom-right pair a single-corner editor could assume.
	snapped, guides := alignRect(painted, a.themeAlignScratch, lay.area.W, lay.area.H,
		kind == editGestureMove, moveEdges, a.alignGuides[:0])
	a.alignGuides = guides
	if snapped == painted {
		return r
	}
	r.X += unprojectDelta(snapped.X-painted.X, primary.sx)
	r.Y += unprojectDelta(snapped.Y-painted.Y, primary.sy)
	r.W += unprojectDelta(snapped.W-painted.W, primary.sx)
	r.H += unprojectDelta(snapped.H-painted.H, primary.sy)
	return r
}

// editorClampGeometry keeps a WIDGET on the stage and leaves an ELEMENT alone.
//
// The asymmetry is the format's, not a shortcut: themeLayoutIn clamps every
// courtroom-relative widget into the canvas because Qt amputated overhanging
// children and themes rely on it, whereas an element's rect is a signed delta on its
// anchor (§6.6 R1) and the inflate idiom `-6, -6, 12, 12` is decoration deliberately
// reaching outside its widget. Clamping an element would forbid values the format
// defines — layoutnudge.go's nudge arm carries the same rule for the keyboard.
func (a *App) editorClampGeometry(t editTarget, r theme.Rect) theme.Rect {
	if t.space != spaceDesign {
		return r
	}
	if court, ok := a.themeRectsOrig["courtroom"]; ok {
		return clampDesignRectToCanvas(r, court)
	}
	return r
}

// editorWriteGeometry routes one target's new rect through the ONE mutation funnel.
// A locked element refuses here rather than at the press, so a locked box can still
// be selected (and unlocked) with the mouse — §3.2's rule.
func (a *App) editorWriteGeometry(t editTarget, r theme.Rect) {
	switch t.space {
	case spaceDesign:
		a.editorApply(mutateSlotRect(t, r))
	case spaceElement:
		if el := a.te.doc.element(t); el == nil || el.Locked {
			return
		}
		a.editorApply(mutateElemField(t, editFieldRect,
			editValueRect(int32(r.X), int32(r.Y), int32(r.W), int32(r.H))))
	}
}

// editorEndGesture closes the gesture and its undo group.
//
// Nothing is "committed" here and nothing needs to be: every frame already wrote
// through editorApply, so the document has been correct the whole way down. What the
// release does is close the group, which is what makes the NEXT edit a separate undo
// step instead of merging into this drag.
func (a *App) editorEndGesture() {
	if a.te == nil {
		return
	}
	a.te.ring.end()
	a.te.drag = editGesture{}
	a.alignGuides = a.alignGuides[:0]
	a.uiDirty = true
}

// ---------------------------------------------------------------------------
// Paint
// ---------------------------------------------------------------------------

// drawEditorCanvasOverlay outlines what can be edited, draws the handles the box
// under the pointer offers, and prints the geometry readout.
//
// It draws AFTER the canvas and BEFORE the rails, so the rails cover it exactly
// where they cover the frame — the same agreement editorCanvasRect makes for input.
func (a *App) drawEditorCanvasOverlay(w, h int32, lay *themeLayoutCache) {
	if a.te == nil || lay == nil || !lay.valid {
		return
	}
	c := a.ctx
	hover := noTarget()
	if !a.te.drag.active() && pointIn(c.mouseX, c.mouseY, editorCanvasRect(w, h)) {
		hover, _ = a.editorProbe(lay, c.mouseX, c.mouseY)
	}
	for key := range lay.r {
		if !editorSlotKeyEditable(key) {
			continue
		}
		a.drawEditorBox(lay, designTarget(key), hover)
	}
	for i := 0; i < a.te.doc.elementCount(); i++ {
		a.drawEditorBox(lay, elementTarget(i), hover)
	}
	// The magnet's guides, in the space the magnet that produced them ran in: painted
	// px, canvas-local (editorMagnet). The canvas origin is added back and NOTHING is
	// scaled — the legacy overlay has to scale its guides because its magnet runs in
	// design space, and scaling these would put the hairline nowhere near the edge the
	// box actually snapped to.
	if a.te.drag.active() {
		for _, g := range a.alignGuides {
			if g.vertical {
				c.Fill(sdl.Rect{X: lay.area.X + g.pos, Y: editBannerH, W: 1, H: h - editBannerH}, ColTierGreen)
			} else {
				c.Fill(sdl.Rect{X: editRailW, Y: lay.area.Y + g.pos, W: w - editRailW*2, H: 1}, ColTierGreen)
			}
		}
	}
	a.drawEditorReadout(w, h, lay)
}

// drawEditorBox outlines ONE box and paints the handles it offers.
//
// The full handle set goes on the box being WORKED (selected or hovered) and the
// single bottom-right grip on every other, which is the classic editor's doctrine:
// a quiet outline at rest, the full treatment under the cursor.
func (a *App) drawEditorBox(lay *themeLayoutCache, t, hover editTarget) {
	g, ok := a.editorTargetGeom(lay, t)
	if !ok {
		return
	}
	c := a.ctx
	col, active := ColAccent, false
	switch {
	case a.te.selectedIs(t):
		col, active = ColDanger, true
	case t == hover:
		col, active = ColTierYellow, true
	}
	col.A = editCanvasRestAlpha
	if active {
		col.A = editCanvasHotAlpha
	}
	c.Border(g.painted, col)
	m := handleGripMask(g.painted, resizeEdgesFor(t))
	if m == 0 {
		return
	}
	for i, hnd := range classicHandles(g.painted) {
		if m&(1<<uint(i)) == 0 {
			continue
		}
		if !active && i != handleBottomRight {
			continue
		}
		c.Fill(hnd, col)
	}
}

// drawEditorReadout prints the primary selection's numbers, in ITS OWN UNITS.
//
// Naming the unit is not decoration: a widget's rect is design px, a `space = window`
// element's is window px, and an anchored element's is a signed DELTA on its anchor
// (§6.6 R1) — three different meanings for four numbers, and a readout that implied
// one of them would teach the user the wrong model of the format.
func (a *App) drawEditorReadout(w, h int32, lay *themeLayoutCache) {
	t := a.te.primary()
	if !t.armed() {
		return
	}
	g, ok := a.editorTargetGeom(lay, t)
	if !ok {
		return
	}
	label := editorTargetLabel(a, t)
	line := fmt.Sprintf("%s: x=%d y=%d w=%d h=%d (%s)", label,
		g.authored.X, g.authored.Y, g.authored.W, g.authored.H, editorUnitName(a, t))
	if n := a.te.selN; n > 1 {
		line = fmt.Sprintf("%s  +%d more selected", line, n-1)
	}
	a.ctx.LabelClipped(editRailW+editRowPad, h-editReadoutH, w-editRailW*2-editRowPad*2, line, ColText)
}

// editorTargetLabel is what the readout and the rail call a target.
func editorTargetLabel(a *App, t editTarget) string {
	if key := t.designKey(); key != "" {
		return key
	}
	if el := a.te.doc.element(t); el != nil && el.ID != "" {
		return el.ID
	}
	if idx, ok := t.elemIdx(); ok {
		return fmt.Sprintf("element %d", idx)
	}
	return ""
}

// editorUnitName names the space a target's numbers live in.
func editorUnitName(a *App, t editTarget) string {
	if t.space == spaceDesign {
		return "design px"
	}
	el := a.te.doc.element(t)
	switch {
	case el == nil:
		return "design px"
	case el.Anchor != "":
		return "offset from " + el.Anchor
	case el.Space == theme.SpaceWindow:
		return "window px"
	}
	return "design px"
}
