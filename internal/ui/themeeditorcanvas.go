package ui

// The theme editor's canvas: turns a press, drag and release over the live frame into ops on the timeline.
//
// Elements are probed first, by paint order, then design keys: elements sit on top of the widgets they decorate and
// are often the smallest, so one largest-wins loop would make them unselectable by mouse.
//
// A gesture holds one undo group open for its whole life, because the group id is part of the coalescing identity;
// a fresh group per frame would make every frame its own undo step.
//
// Numbers are written in authored units so a stored value is never a round trip of a scaled one. The magnet alone runs
// on painted boxes, since an element's rect is a signed delta on its anchor; its correction is divided back out.

import (
	"fmt"
	"math"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	editGestureNone uint8 = iota
	editGestureMove
	editGestureResize
)

const (
	// editCanvasRestAlpha outlines every editable box when the pointer is elsewhere; faint so the theme stays visible.
	editCanvasRestAlpha = 70
	// editCanvasHotAlpha outlines the box under the cursor and the selection.
	editCanvasHotAlpha = 255
	// editReadoutH is the gap between the geometry readout and the window's bottom edge, in px.
	editReadoutH = 22
)

// editGesture is one press-drag-release. base holds every selected target's authored rect at the press, so each frame
// recomputes from the same origin and rounding can't accumulate into shear between aligned elements.
type editGesture struct {
	kind  uint8
	edges uint8
	start [2]int32
	// group is the undo group the whole gesture pushes into, held until release (see editorBeginGesture).
	group uint32
	base  [themeSelCap]theme.Rect
	baseN int
}

func (g *editGesture) active() bool { return g.kind != editGestureNone }

// editTargetGeom is one target's edited numbers, the box they paint as, and the ratio between them.
type editTargetGeom struct {
	authored theme.Rect
	painted  sdl.Rect
	sx, sy   float64
}

// editorTargetGeom resolves a target; ok=false when it is not on the canvas this frame. It is the only place the spaces
// diverge. It tests for a built cache, not a valid one: every write here invalidates the layout, so a `lay.valid` test
// would make the second target of a multi-drag, and the overlay, resolve to nothing. lay must come from
// themeWindowLayout this frame.
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
		// A `space = window` element is window px end to end: dividing by the canvas scale would move it at the wrong
		// rate.
		sx, sy := lay.scaleX, lay.scaleY
		if el.Space == theme.SpaceWindow {
			sx, sy = 1, 1
		}
		return editTargetGeom{authored: el.Rect, painted: painted, sx: sx, sy: sy}, true
	}
	return editTargetGeom{}, false
}

// editorSlotKeyEditable is themeKeyEditable minus the server-tab strip: it is client chrome whose placement is a
// preference and whose size comes from its chips, which no [overrides] row can express.
func editorSlotKeyEditable(key string) bool {
	return key != "" && key != themeTabBarKey && themeKeyEditable(key)
}

// project maps an authored rect onto the screen. base.authored projects to base.painted exactly, so a gesture that
// ends where it started ends with the same numbers.
func (g editTargetGeom) project(r theme.Rect) sdl.Rect {
	return sdl.Rect{
		X: g.painted.X + int32(float64(r.X-g.authored.X)*g.sx),
		Y: g.painted.Y + int32(float64(r.Y-g.authored.Y)*g.sy),
		W: g.painted.W + int32(float64(r.W-g.authored.W)*g.sx),
		H: g.painted.H + int32(float64(r.H-g.authored.H)*g.sy),
	}
}

// unprojectDelta rounds rather than truncates: a truncated correction lets a magnet nudge a box on every frame.
func unprojectDelta(d int32, scale float64) int {
	if scale <= 0 {
		return int(d)
	}
	return int(math.Round(float64(d) / scale))
}

// editorProbe returns the target under (mx, my) and the resize edges a press would grip (0 = a move). A grip wins over
// a move, and among grips the largest box, so a big box's corner can't be blocked by a small box on it. Among moves,
// paint order then smallest decides for elements; smallest decides for widgets, which have no z.
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

// editorProbeElements walks the document's elements; a hidden element never hit-tests.
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

// elemPickBeats: a grip beats a plain hit, then paint order (band, z, declaration index), then the smaller box,
// which keeps a full-canvas wallpaper from swallowing every press.
func elemPickBeats(el *theme.Element, g editTargetGeom, bestEl *theme.Element, best editTargetGeom, grip, bestGrip bool) bool {
	if grip != bestGrip {
		return grip
	}
	if grip {
		// Among grips the largest box wins, so a big element's corner is reachable through a small one on it.
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

// editorProbeSlots walks the AO2 widgets: the smallest box wins a move, the largest gripped box a resize.
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

// slotPickBeats ends on the key because lay.r is a map: identical rects would otherwise resolve differently per frame.
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

// editorCanvasRect is the frame minus the header band and the rails, whose opaque chrome owns presses over it.
func editorCanvasRect(w, h int32) sdl.Rect {
	r := sdl.Rect{X: editRailW, Y: editBannerH, W: w - editRailW*2, H: h - editBannerH}
	if r.W < 0 { // a window narrower than both rails: no canvas to press on at all
		r.W = 0
	}
	return r
}

// editorCanvasInput advances a live gesture first, since a drag off the canvas must keep running, then takes a press.
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
	// The canvas ignores presses behind a panel: this probe uses raw pointIn, which c.modalOn does not stop. A live
	// gesture already returned above, because a drag started before the panel opened must still be able to end.
	if a.editorPanelUp() {
		return
	}
	// The file browser is a modal (editorPanelUp excludes modals) and also draws over the canvas.
	if demoBrowser.open {
		return
	}
	// The compact toolbox is also invisible to raw pointIn: a press on its grip must not grab what lies beneath.
	if a.editOverToolbox(w, h) {
		return
	}
	t, edges := a.editorProbe(lay, c.mouseX, c.mouseY)
	if !t.armed() {
		// Empty canvas: clear, unless a modifier says the user is building a selection.
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

// editorBeginGesture records the whole selection's rects. A locked target records too and is never written, which
// keeps base[] and sel[] at the same indices.
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
	// The pressed target leads the gesture, not whichever is first in the selection.
	e.selectPrimary(t)
	for i := 0; i < e.selN; i++ {
		if geom, ok := a.editorTargetGeom(lay, e.sel[i]); ok {
			g.base[i] = geom.authored
		}
	}
	g.baseN = e.selN
	// One group until release; a per-frame group would make a hundred-frame drag a hundred undo steps.
	g.group = e.ring.begin()
}

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
	// The base is the press-time rect: accumulating rounded deltas makes a snapped drag creep away from the cursor.
	base := g.base[0]
	dx := unprojectDelta(a.ctx.mouseX-g.start[0], primary.sx)
	dy := unprojectDelta(a.ctx.mouseY-g.start[1], primary.sy)

	r := base
	if g.kind == editGestureResize {
		r = resizeDesignRect(base, g.edges, dx, dy)
	} else {
		r.X, r.Y = base.X+dx, base.Y+dy
	}
	// Shift bypasses the grid and the magnet together, as both legacy editors do.
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
	// Other targets take the delta the primary actually moved, post-grid, magnet and clamp, so the group doesn't shear.
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

// editorRescale carries a delta between targets' units; only a `space = window` element mixed with a design one
// differs.
func editorRescale(d int, from, to float64) int {
	if from == to || from <= 0 || to <= 0 {
		return d
	}
	return int(math.Round(float64(d) * from / to))
}

// editorMagnet snaps to every other editable box in painted space and folds the correction back to authored units.
// Canvas-local because alignRect's extent is measured from the origin: window coordinates would misplace the right
// edge by the letterbox bar. The guides come back local too.
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
	// alignRect takes the gripped edges, not a fixed bottom-right pair.
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

// editorClampGeometry keeps a widget on the stage and leaves an element alone: themeLayoutIn clamps widgets because Qt
// cut off overhanging children, while an element's rect is a signed delta on its anchor and may reach outside its
// widget.
func (a *App) editorClampGeometry(t editTarget, r theme.Rect) theme.Rect {
	if t.space != spaceDesign {
		return r
	}
	if court, ok := a.themeRectsOrig["courtroom"]; ok {
		return clampDesignRectToCanvas(r, court)
	}
	return r
}

// editorWriteGeometry refuses a locked element here rather than at the press, so it can still be selected and unlocked.
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

// editorEndGesture closes the undo group; every frame already wrote, and this makes the next edit a separate step.
func (a *App) editorEndGesture() {
	if a.te == nil {
		return
	}
	a.te.ring.end()
	a.te.drag = editGesture{}
	a.alignGuides = a.alignGuides[:0]
	a.uiDirty = true
}

// drawEditorCanvasOverlay outlines what is editable, draws the handles and readout. Drawn after the canvas and before
// the rails, so the rails cover it as editorCanvasRect does for input.
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
	// The guides are painted, canvas-local px (editorMagnet): add the origin back and do not scale.
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

// drawEditorBox gives the box being worked the full handle set and every other box the single bottom-right grip.
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

// drawEditorReadout names the unit because the four numbers mean design px, window px or a signed delta on an anchor.
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
