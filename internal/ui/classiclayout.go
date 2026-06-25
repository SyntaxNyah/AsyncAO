package ui

// Classic-layout slots: the DEFAULT (non-themed) courtroom — and the Legacy
// Developer theme, which shares the same procedural geometry — are laid out
// fresh every frame, so unlike the themed editor there are no design rects to
// drag. Instead each movable widget draws through slotRect, which returns a user
// override (persisted as a window FRACTION, so the drag is resolution-
// independent) or — when nothing is overridden — the exact rect the layout
// already computed. Un-edited ⇒ pixel-identical to before (the safety
// invariant); the override path is purely additive and off the hot frame.
//
// The editor (drawClassicEditor) reuses the themed editor's feel — drag = move,
// edge / corner handles = resize (independently horizontal or vertical),
// right-click = reset a box, Snap, Esc/Done — but works in screen space over the
// live courtroom and persists to config.ClassicLayout.

import (
	"sort"

	"github.com/veandco/go-sdl2/sdl"
)

// Slot names — string literals so the render path never formats a key (which
// would allocate every frame). Keep in sync with the call sites in drawCourtroom.
const (
	slotViewport = "viewport" // the stage (free move + resize; the scene fills it)
	slotRightCol = "rightcol" // IC log / right column (both themes)
	slotOOC      = "ooc"      // the new-default OOC box (independent of the log)
	slotEmotes   = "emotes"   // the emote grid (pages within its rect; both themes)
	slotICBar    = "icbar"    // the IC input bar (colour · showname · Immed · emoji/FX/React · text)
	slotOOCBar   = "oocbar"   // the Legacy bottom OOC bar (name + full-width input; Legacy theme only)
	slotControls = "controls" // the two control-button rows (shouts/pair/knobs + utility buttons) as one block
)

// Resize-edge bitmask: which sides of a box a drag moves.
const (
	edgeL uint8 = 1 << iota
	edgeR
	edgeT
	edgeB
)

const (
	// classicSlotRegCap pre-sizes the per-frame slot registry (cosmetic; the
	// durable bound is config.classicSlotCap on what persists).
	classicSlotRegCap = 24
	// classicMinPx floors a resized slot in screen px so a box can't vanish.
	classicMinPx = 48
	// classicBannerH is the editor's top banner height (drags stay below it).
	classicBannerH = 26
)

// slotInfo records, per registered slot this frame, the rect it actually drew at
// (cur) and the rect it WOULD draw at with no override (def, for reset).
// Populated only while editing, so the common frame is alloc-free.
type slotInfo struct {
	cur sdl.Rect
	def sdl.Rect
}

// ensureClassicOv loads the persisted overrides into the App-local snapshot once
// (the editor is the sole writer thereafter). A nil snapshot means "no edits" —
// slotRect then just returns the computed default. Called every courtroom frame;
// after the first it is a single bool check (alloc-free).
func (a *App) ensureClassicOv() {
	if a.classicOvLoaded {
		return
	}
	a.classicOv = a.d.Prefs.ClassicLayoutOverrides()
	a.classicOvLoaded = true
}

// fracToRect converts a stored window-fraction override to screen pixels.
func fracToRect(f [4]float64, w, h int32) sdl.Rect {
	return sdl.Rect{
		X: int32(f[0] * float64(w)),
		Y: int32(f[1] * float64(h)),
		W: int32(f[2] * float64(w)),
		H: int32(f[3] * float64(h)),
	}
}

// rectToFrac is the inverse — screen pixels to window fractions for persistence.
func rectToFrac(r sdl.Rect, w, h int32) [4]float64 {
	if w <= 0 || h <= 0 {
		return [4]float64{}
	}
	return [4]float64{
		float64(r.X) / float64(w),
		float64(r.Y) / float64(h),
		float64(r.W) / float64(w),
		float64(r.H) / float64(h),
	}
}

// regSlot records a slot's drawn/default rects for the editor (edit-only path).
func (a *App) regSlot(name string, cur, def sdl.Rect) {
	if a.slotReg == nil {
		a.slotReg = make(map[string]slotInfo, classicSlotRegCap)
	}
	a.slotReg[name] = slotInfo{cur: cur, def: def}
}

// slotRect returns a movable+resizable widget's rect: the user override (frac→px)
// if present, else the layout's computed default. Reads a.classicOv lock-free; on
// the common (non-edit) frame it touches no map writer and allocates nothing.
func (a *App) slotRect(name string, def sdl.Rect, w, h int32) sdl.Rect {
	cur := def
	if ov, ok := a.classicOv[name]; ok {
		cur = fracToRect(ov, w, h)
	}
	if a.classicEdit {
		a.regSlot(name, cur, def)
	}
	return cur
}

// classicSnap rounds a screen coordinate to the editor's grid (shared 8 px).
func classicSnap(v int32) int32 {
	if v < 0 {
		return 0
	}
	const g = layoutGridDesign
	return (v + g/2) / g * g
}

// classicSlotLabel is the human name shown on a slot's outline in the editor.
func classicSlotLabel(k string) string {
	switch k {
	case slotViewport:
		return "Viewport (stage)"
	case slotRightCol:
		return "Log / right column"
	case slotOOC:
		return "OOC box"
	case slotEmotes:
		return "Emote grid"
	case slotICBar:
		return "IC input bar"
	case slotOOCBar:
		return "OOC bar (Legacy)"
	case slotControls:
		return "Control buttons"
	default:
		// Torn-off tab panels carry a "tab:<name>" key.
		for i := range tornTabTable {
			if tornTabTable[i].key == k {
				return tornTabTable[i].title + " (panel)"
			}
		}
		return k
	}
}

// classicEdgeAt reports which sides of r the cursor grips, within margin px. A
// corner returns two adjacent sides; 0 means "inside / not on an edge" (= move).
func classicEdgeAt(mx, my int32, r sdl.Rect, margin int32) uint8 {
	if mx < r.X-margin || mx > r.X+r.W+margin || my < r.Y-margin || my > r.Y+r.H+margin {
		return 0
	}
	var e uint8
	if abs32(mx-r.X) <= margin {
		e |= edgeL
	}
	if abs32(mx-(r.X+r.W)) <= margin {
		e |= edgeR
	}
	if abs32(my-r.Y) <= margin {
		e |= edgeT
	}
	if abs32(my-(r.Y+r.H)) <= margin {
		e |= edgeB
	}
	return e
}

// classicHandles returns the 8 resize handles (4 corners + 4 edge midpoints) of r
// so the editor can paint them — making "drag an edge to resize one dimension"
// discoverable.
func classicHandles(r sdl.Rect) [8]sdl.Rect {
	const hp = layoutHandlePx
	cx := r.X + r.W/2 - hp/2
	cy := r.Y + r.H/2 - hp/2
	return [8]sdl.Rect{
		{X: r.X, Y: r.Y, W: hp, H: hp},                       // top-left
		{X: r.X + r.W - hp, Y: r.Y, W: hp, H: hp},            // top-right
		{X: r.X, Y: r.Y + r.H - hp, W: hp, H: hp},            // bottom-left
		{X: r.X + r.W - hp, Y: r.Y + r.H - hp, W: hp, H: hp}, // bottom-right
		{X: cx, Y: r.Y, W: hp, H: hp},                        // top edge
		{X: cx, Y: r.Y + r.H - hp, W: hp, H: hp},             // bottom edge
		{X: r.X, Y: cy, W: hp, H: hp},                        // left edge
		{X: r.X + r.W - hp, Y: cy, W: hp, H: hp},             // right edge
	}
}

// startClassicEdit arms the default-courtroom slot editor. Open modals close
// (they'd be fenced shut); mirrors startLayoutEdit.
func (a *App) startClassicEdit() {
	a.ensureClassicOv()
	a.classicEdit = true
	a.showUICfg = false
	a.showIni, a.showEvid, a.showModcall, a.showLogin, a.showPair = false, false, false, false, false
	a.showModDash, a.banBoxKind, a.showCMPanel = false, 0, false
	a.bgPick.show = false
	a.classicEditKey = ""
	a.classicEditDrag = 0
	a.classicEditEdges = 0
	a.classicEditMoved = false
	a.layoutSnap = true // tidy placement by default; toggle off in the editor
	a.pushDebug("edit layout (default courtroom): drag to move, edges/corners to resize, Esc to finish")
}

// stopClassicEdit disarms and releases the input fence.
func (a *App) stopClassicEdit() {
	a.classicEdit = false
	a.classicEditKey = ""
	a.classicEditDrag = 0
	a.classicEditEdges = 0
	a.ctx.modalOn = false
}

// classicEditFence claims the pointer for the slot editor BEFORE the default
// courtroom's widgets draw — they see hovering()==false and stay inert while the
// editor reads raw cursor coordinates (pointIn). Mirrors layoutEditFence.
func (a *App) classicEditFence() {
	if a.classicEdit {
		a.ctx.modalOn = true
	}
}

// controlsBlockOrigin computes the control-button block's draw origin (clusterX,
// blockY), its vertical offset from the default top (dy), and the row-wrap edge
// (clusterRight) from the block's slot override, if present. The content width is
// held CONSTANT at w-2*pad, so the wrap structure — and therefore the block's row
// count and height — is invariant to the move; that is what lets drawICControls
// recover the un-moved bottom as (y2 - dy) and stay byte-identical when un-edited
// (no override ⇒ clusterX==pad, dy==0, clusterRight==w-pad). Width/height of the
// override are ignored by design (the block stays full width). Pure + alloc-free so
// the invariant is unit-pinnable; the drawICControls call site reads classicOv first.
func controlsBlockOrigin(ov [4]float64, ok bool, w, h, defY int32) (clusterX, blockY, dy, clusterRight int32) {
	clusterX, blockY = pad, defY
	if ok {
		r := fracToRect(ov, w, h)
		clusterX, blockY = r.X, r.Y
	}
	dy = blockY - defY
	clusterRight = clusterX + (w - 2*pad)
	return
}

// viewportOverridden reports whether the user has dragged/resized the stage in
// the classic editor. The View knob and the edge divider then defer to that
// override (it would otherwise change vpPct silently, which the override shadows)
// until the box is reset.
func (a *App) viewportOverridden() bool {
	_, ok := a.classicOv[slotViewport]
	return ok
}

// clearClassicSlot drops one slot's override from both the durable pref and the
// App-local snapshot so it reverts to the computed default the same frame.
func (a *App) clearClassicSlot(name string) {
	a.d.Prefs.ClearClassicSlot(name)
	if a.classicOv != nil {
		delete(a.classicOv, name)
	}
}

// drawClassicEditor paints the slot-editor overlay and owns every interaction.
// Called LAST from drawCourtroom (default layout only), after every widget has
// registered its rect via slotRect this frame.
func (a *App) drawClassicEditor(w, h int32) {
	c := a.ctx
	if w <= 0 || h <= 0 {
		a.stopClassicEdit()
		return
	}

	// Banner + chrome (raw-hit chips — the fence blocks kit buttons).
	banner := "EDIT LAYOUT — drag = move · edges/corners = resize · right-click = reset a box · Esc = done"
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: classicBannerH}, sdl.Color{R: 0, G: 0, B: 0, A: 210})
	c.Label(pad, 5, banner, ColTierYellow)
	doneBtn := sdl.Rect{X: w - 70 - pad, Y: 2, W: 70, H: 22}
	resetBtn := sdl.Rect{X: doneBtn.X - 96, Y: 2, W: 90, H: 22}
	snapBtn := sdl.Rect{X: resetBtn.X - 106, Y: 2, W: 100, H: 22}
	snapLabel := "Snap: off"
	if a.layoutSnap {
		snapLabel = "Snap: on"
	}
	a.rawChip(doneBtn, "Done")
	a.rawChip(resetBtn, "Reset all")
	a.rawChip(snapBtn, snapLabel)

	pressed := c.mouseDown && !a.classicEditPrev
	a.classicEditPrev = c.mouseDown

	if c.escPressed || (c.clicked && pointIn(c.mouseX, c.mouseY, doneBtn)) {
		a.stopClassicEdit()
		return
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, resetBtn) {
		a.d.Prefs.ClearClassicSlot("")
		a.classicOv = nil
		a.pushDebug("edit layout: all boxes reset to default")
		return
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, snapBtn) {
		a.layoutSnap = !a.layoutSnap
	}

	// Pop-out tray (bottom): tear a log tab out into its own movable panel, or
	// redock it. overTray suppresses a slot-move when you press a chip. (torntabs.go)
	overTray := a.drawClassicTabTray(w, h)

	// Slot names this frame, stable order.
	keys := make([]string, 0, len(a.slotReg))
	for k := range a.slotReg {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Hover: the SMALLEST box under the cursor (so a small box sitting on a big
	// one is still grabbable). The slice-1 slots don't overlap, so no Tab-cycle.
	hoverKey := ""
	if a.classicEditDrag != 0 {
		hoverKey = a.classicEditKey // mid-drag: keep the grabbed box highlighted
	} else {
		var best int64 = -1
		for _, k := range keys {
			r := a.slotReg[k].cur
			if pointIn(c.mouseX, c.mouseY, r) {
				if area := int64(r.W) * int64(r.H); best < 0 || area < best {
					best, hoverKey = area, k
				}
			}
		}
	}

	// Begin a drag on press: RESIZE (an edge/corner of some box is gripped) takes
	// priority over MOVE. Among gripped boxes, the SMALLEST wins so a small box's
	// handle isn't swallowed by a big box behind it.
	if pressed && a.classicEditDrag == 0 && c.mouseY > classicBannerH && !overTray {
		resizeKey, resizeEdges, best := "", uint8(0), int64(-1)
		for _, k := range keys {
			r := a.slotReg[k].cur
			if e := classicEdgeAt(c.mouseX, c.mouseY, r, layoutHandlePx); e != 0 {
				if area := int64(r.W) * int64(r.H); best < 0 || area < best {
					resizeKey, resizeEdges, best = k, e, area
				}
			}
		}
		switch {
		case resizeKey != "":
			a.classicEditKey, a.classicEditDrag, a.classicEditEdges = resizeKey, 2, resizeEdges
		case hoverKey != "":
			a.classicEditKey, a.classicEditDrag, a.classicEditEdges = hoverKey, 1, 0
		}
		if a.classicEditDrag != 0 {
			a.classicEditStart = [2]int32{c.mouseX, c.mouseY}
			a.classicEditBase = a.slotReg[a.classicEditKey].cur
			a.classicEditMoved = false
		}
	}

	// Right-click resets the hovered slot to its computed default.
	if c.rightClicked && hoverKey != "" {
		a.clearClassicSlot(hoverKey)
	}

	// Live drag: screen deltas applied directly (screen space), clamped on-stage,
	// snapped, then written to the App-local override (px→frac) so the widget
	// redraws at the new spot NEXT frame.
	if a.classicEditDrag != 0 && c.mouseDown && a.classicEditKey != "" {
		dx := c.mouseX - a.classicEditStart[0]
		dy := c.mouseY - a.classicEditStart[1]
		if dx != 0 || dy != 0 {
			a.classicEditMoved = true
		}
		base := a.classicEditBase
		r := base
		if a.classicEditDrag == 1 { // move
			r.X = base.X + dx
			r.Y = base.Y + dy
		} else { // resize the gripped edges (one or both dimensions)
			e := a.classicEditEdges
			if e&edgeR != 0 {
				r.W = base.W + dx
			}
			if e&edgeL != 0 {
				r.X = base.X + dx
				r.W = base.W - dx
			}
			if e&edgeB != 0 {
				r.H = base.H + dy
			}
			if e&edgeT != 0 {
				r.Y = base.Y + dy
				r.H = base.H - dy
			}
			if r.W < classicMinPx { // floor without inverting; keep the anchored edge fixed
				if e&edgeL != 0 {
					r.X = base.X + base.W - classicMinPx
				}
				r.W = classicMinPx
			}
			if r.H < classicMinPx {
				if e&edgeT != 0 {
					r.Y = base.Y + base.H - classicMinPx
				}
				r.H = classicMinPx
			}
		}
		if a.layoutSnap {
			r.X, r.Y, r.W, r.H = classicSnap(r.X), classicSnap(r.Y), classicSnap(r.W), classicSnap(r.H)
			if r.W < classicMinPx {
				r.W = classicMinPx
			}
			if r.H < classicMinPx {
				r.H = classicMinPx
			}
		}
		// Keep it on-screen (solid feel; below the editor banner).
		if r.X < 0 {
			r.X = 0
		}
		if r.Y < classicBannerH {
			r.Y = classicBannerH
		}
		if r.X+r.W > w {
			r.X = w - r.W
		}
		if r.Y+r.H > h {
			r.Y = h - r.H
		}
		if a.classicEditMoved {
			if a.classicOv == nil {
				a.classicOv = make(map[string][4]float64, classicSlotRegCap)
			}
			a.classicOv[a.classicEditKey] = rectToFrac(r, w, h)
		}
	}

	// Release persists the edit (a no-move click changes nothing).
	if a.classicEditDrag != 0 && !c.mouseDown {
		if a.classicEditMoved && a.classicEditKey != "" {
			if ov, ok := a.classicOv[a.classicEditKey]; ok {
				a.d.Prefs.SetClassicSlot(a.classicEditKey, ov)
			}
		}
		a.classicEditDrag = 0
		a.classicEditEdges = 0
		a.classicEditMoved = false
	}

	// Overlay: outline every slot, label it, paint the 8 resize handles. A slot
	// mid-drag reflects its live (this-frame) override position.
	for _, k := range keys {
		r := a.slotReg[k].cur
		if a.classicEditDrag != 0 && k == a.classicEditKey {
			if ov, ok := a.classicOv[k]; ok {
				r = fracToRect(ov, w, h)
			}
		}
		col := ColAccent
		switch {
		case k == a.classicEditKey:
			col = ColDanger
		case k == hoverKey:
			col = ColTierYellow
		}
		c.Border(r, col)
		for _, hnd := range classicHandles(r) {
			c.Fill(hnd, col)
		}
		c.LabelClipped(r.X+4, r.Y+3, r.W-8, classicSlotLabel(k), col)
	}
	if a.classicEditKey != "" {
		c.Label(pad, h-22, "editing: "+classicSlotLabel(a.classicEditKey), ColText)
	} else if len(keys) > 0 {
		c.Label(pad, h-22, "drag a box to move · grab an edge or corner to resize · saves automatically", ColTextDim)
	}
}
