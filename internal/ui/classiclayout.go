package ui

// Classic-layout slots: the DEFAULT (non-themed) courtroom — and the Legacy
// Developer theme, which shares the same procedural geometry — are laid out
// fresh every frame, so unlike the themed editor there are no design rects to
// drag. Instead each movable widget draws through slotRect / slotMove, which
// return a user override (persisted as a window FRACTION, so the drag is
// resolution-independent) or — when nothing is overridden — the exact rect the
// layout already computed. Un-edited ⇒ pixel-identical to before (the safety
// invariant); the override path is purely additive and off the hot frame.
//
// The editor (drawClassicEditor) reuses the themed editor's feel — drag = move,
// corner grip = resize, right-click = reset a box, Snap, Esc/Done — but works in
// screen space over the live courtroom and persists to config.ClassicLayout.

import (
	"sort"

	"github.com/veandco/go-sdl2/sdl"
)

// Slot names — string literals so the render path never formats a key (which
// would allocate every frame). Keep in sync with the call sites in drawCourtroom.
const (
	slotViewport = "viewport" // the 4:3 stage (move-only; size owned by the View knob)
	slotRightCol = "rightcol" // IC log / right column (both themes)
	slotOOC      = "ooc"      // the new-default OOC box (independent of the log)
)

// classicMoveOnly marks slots whose SIZE is owned elsewhere and must not be
// free-resized. The viewport is 4:3 aspect-locked and already resizes via the
// View knob / divider (which write vpPct); letting the editor resize it too
// would put two masters on its size and break the aspect — so it only MOVES.
var classicMoveOnly = map[string]bool{slotViewport: true}

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
// (cur), the rect it WOULD draw at with no override (def, for reset), and whether
// resize is allowed. Populated only while editing, so the common frame is
// alloc-free.
type slotInfo struct {
	cur      sdl.Rect
	def      sdl.Rect
	moveOnly bool
}

// ensureClassicOv loads the persisted overrides into the App-local snapshot once
// (the editor is the sole writer thereafter). A nil snapshot means "no edits" —
// slotRect / slotMove then just return the computed default. Called every
// courtroom frame; after the first it is a single bool check (alloc-free).
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
	a.slotReg[name] = slotInfo{cur: cur, def: def, moveOnly: classicMoveOnly[name]}
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

// slotMove is slotRect for MOVE-ONLY widgets (the viewport): only X/Y come from
// the override; W/H always track the computed default, whose size is owned by the
// View knob / divider.
func (a *App) slotMove(name string, def sdl.Rect, w, h int32) sdl.Rect {
	cur := def
	if ov, ok := a.classicOv[name]; ok {
		cur.X = int32(ov[0] * float64(w))
		cur.Y = int32(ov[1] * float64(h))
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
		return "Viewport (stage) — move"
	case slotRightCol:
		return "Log / right column"
	case slotOOC:
		return "OOC box"
	default:
		return k
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
	a.classicEditMoved = false
	a.layoutSnap = true // tidy placement by default; toggle off in the editor
	a.pushDebug("edit layout (default courtroom): drag a box to move, corner to resize, Esc to finish")
}

// stopClassicEdit disarms and releases the input fence.
func (a *App) stopClassicEdit() {
	a.classicEdit = false
	a.classicEditKey = ""
	a.classicEditDrag = 0
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
// registered its rect via slotRect / slotMove this frame.
func (a *App) drawClassicEditor(w, h int32) {
	c := a.ctx
	if w <= 0 || h <= 0 {
		a.stopClassicEdit()
		return
	}

	// Banner + chrome (raw-hit chips — the fence blocks kit buttons).
	banner := "EDIT LAYOUT — drag = move · corner grip = resize · right-click = reset a box · Esc = done"
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

	// Begin a drag on press: RESIZE (corner grip, resizable slots only) takes
	// priority, else MOVE the hovered box.
	if pressed && a.classicEditDrag == 0 && c.mouseY > classicBannerH {
		resizeKey := ""
		var gripArea int64 = -1
		for _, k := range keys {
			si := a.slotReg[k]
			if si.moveOnly {
				continue
			}
			r := si.cur
			grip := sdl.Rect{X: r.X + r.W - layoutHandlePx, Y: r.Y + r.H - layoutHandlePx, W: layoutHandlePx, H: layoutHandlePx}
			if pointIn(c.mouseX, c.mouseY, grip) {
				if area := int64(r.W) * int64(r.H); area > gripArea {
					resizeKey, gripArea = k, area
				}
			}
		}
		switch {
		case resizeKey != "":
			a.classicEditKey, a.classicEditDrag = resizeKey, 2
		case hoverKey != "":
			a.classicEditKey, a.classicEditDrag = hoverKey, 1
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

	// Live drag: screen deltas applied directly (screen space), floored, clamped
	// on-stage, snapped, then written to the App-local override (px→frac) so the
	// widget redraws at the new spot NEXT frame.
	if a.classicEditDrag != 0 && c.mouseDown && a.classicEditKey != "" {
		dx := c.mouseX - a.classicEditStart[0]
		dy := c.mouseY - a.classicEditStart[1]
		if dx != 0 || dy != 0 {
			a.classicEditMoved = true
		}
		r := a.classicEditBase
		if a.classicEditDrag == 1 {
			r.X += dx
			r.Y += dy
		} else {
			r.W += dx
			r.H += dy
			if r.W < classicMinPx {
				r.W = classicMinPx
			}
			if r.H < classicMinPx {
				r.H = classicMinPx
			}
		}
		if a.layoutSnap {
			if a.classicEditDrag == 1 {
				r.X = classicSnap(r.X)
				r.Y = classicSnap(r.Y)
			} else {
				r.W = classicSnap(r.W)
				r.H = classicSnap(r.H)
				if r.W < classicMinPx {
					r.W = classicMinPx
				}
				if r.H < classicMinPx {
					r.H = classicMinPx
				}
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
		a.classicEditMoved = false
	}

	// Overlay: outline every slot, label it, show the resize grip on resizables.
	// A slot mid-drag reflects its live (this-frame) override position.
	for _, k := range keys {
		si := a.slotReg[k]
		r := si.cur
		if a.classicEditDrag != 0 && k == a.classicEditKey {
			if ov, ok := a.classicOv[k]; ok {
				r = fracToRect(ov, w, h)
				if si.moveOnly { // size is fixed for move-only slots
					r.W, r.H = si.cur.W, si.cur.H
				}
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
		if !si.moveOnly {
			c.Fill(sdl.Rect{X: r.X + r.W - layoutHandlePx, Y: r.Y + r.H - layoutHandlePx, W: layoutHandlePx, H: layoutHandlePx}, col)
		}
		c.LabelClipped(r.X+4, r.Y+3, r.W-8, classicSlotLabel(k), col)
	}
	if a.classicEditKey != "" {
		c.Label(pad, h-22, "editing: "+classicSlotLabel(a.classicEditKey), ColText)
	} else if len(keys) > 0 {
		c.Label(pad, h-22, "drag any outlined box — moves & sizes save automatically", ColTextDim)
	}
}
