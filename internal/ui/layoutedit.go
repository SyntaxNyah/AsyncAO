package ui

// Live layout editor for themed widgets: edits are design-space overrides saved per theme and applied over
// courtroom_design.ini on load, so the theme's own file is never touched.
// While on, modalOn keeps real widgets inert and the editor reads raw cursor coordinates.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// layoutHandlePx is the resize grip (bottom-right corner), screen px.
	layoutHandlePx = 12
	// layoutMinDesignPx floors edited widgets in design space, above the layout engine's degenerate-rect rejection.
	layoutMinDesignPx = 16
	// layoutGridDesign is the snap grid in design px.
	layoutGridDesign = 8
	// layoutBannerH is the themed editor's banner height, also the floor of the drag hit test, so a press in the banner
	// hits a chip, not a widget under it.
	layoutBannerH = 26
)

// snapDesign rounds a design-space coordinate to the nearest grid line.
func snapDesign(v int) int {
	if v < 0 {
		return 0
	}
	return (v + layoutGridDesign/2) / layoutGridDesign * layoutGridDesign
}

// snapTabStripScreen rounds the tab strip's stored origin so its painted box lands on the window's grid, the space the
// rest of its gesture uses; a canvas-phased grid would shift with window size and fit mode. A theme's own widgets keep
// the design grid.
// snapDesign's floor is right here: the painted value is never negative, and zero is the window's top edge.
func snapTabStripScreen(r theme.Rect, lay *themeLayoutCache) theme.Rect {
	r.X = snapDesign(r.X+int(lay.offX)) - int(lay.offX)
	r.Y = snapDesign(r.Y+int(lay.offY)) - int(lay.offY)
	return r
}

// themedKeyResizable reports whether a key honours resize; the tab strip's size comes from its chips, matching the
// classic layout's move-only box.
func themedKeyResizable(key string) bool { return key != themeTabBarKey }

// resizeDesignRect applies a resize drag's delta to the gripped edges in design px; at the minimum the anchored edge
// stays fixed, so the box can't invert.
func resizeDesignRect(base theme.Rect, edges uint8, dx, dy int) theme.Rect {
	r := base
	if edges&edgeR != 0 {
		r.W = base.W + dx
	}
	if edges&edgeL != 0 {
		r.X = base.X + dx
		r.W = base.W - dx
	}
	if edges&edgeB != 0 {
		r.H = base.H + dy
	}
	if edges&edgeT != 0 {
		r.Y = base.Y + dy
		r.H = base.H - dy
	}
	if r.W < layoutMinDesignPx {
		if edges&edgeL != 0 {
			r.X = base.X + base.W - layoutMinDesignPx
		}
		r.W = layoutMinDesignPx
	}
	if r.H < layoutMinDesignPx {
		if edges&edgeT != 0 {
			r.Y = base.Y + base.H - layoutMinDesignPx
		}
		r.H = layoutMinDesignPx
	}
	return r
}

// snapDesignResize snaps the size and holds the anchored edge, keeping a theme's off-grid origin; ungripped axes are
// untouched.
func snapDesignResize(r theme.Rect, edges uint8) theme.Rect {
	if edges&(edgeL|edgeR) != 0 {
		right := r.X + r.W
		r.W = snapDesign(r.W)
		if r.W < layoutMinDesignPx {
			r.W = layoutMinDesignPx
		}
		if edges&edgeL != 0 {
			r.X = right - r.W
		}
	}
	if edges&(edgeT|edgeB) != 0 {
		bottom := r.Y + r.H
		r.H = snapDesign(r.H)
		if r.H < layoutMinDesignPx {
			r.H = layoutMinDesignPx
		}
		if edges&edgeT != 0 {
			r.Y = bottom - r.H
		}
	}
	return r
}

// layoutUndoCap bounds the editor's undo and redo stacks (hard rule 4).
const layoutUndoCap = 64

func cloneRects(m map[string]theme.Rect) map[string]theme.Rect {
	cp := make(map[string]theme.Rect, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// layoutSnapshot is one undo step. The tab strip's placed flag isn't recoverable from the rects (a placement can land
// on the seed's coordinates), so restoring rects alone would un-park the strip.
type layoutSnapshot struct {
	rects  map[string]theme.Rect
	parked bool
}

// layoutSnapshotNow captures the editable state. It uses tabStripPlacementRecorded, not tabStripThemeParked: the press
// site pushes after arming the drag, so the live check would record parked for a strip still docked.
func (a *App) layoutSnapshotNow() layoutSnapshot {
	return layoutSnapshot{rects: cloneRects(a.themeRects), parked: a.tabStripPlacementRecorded()}
}

// pushLayoutUndo snapshots the rects before an edit and drops the redo stack.
func (a *App) pushLayoutUndo() {
	a.editUndo = append(a.editUndo, a.layoutSnapshotNow())
	if len(a.editUndo) > layoutUndoCap {
		a.editUndo = a.editUndo[1:]
	}
	a.editRedo = a.editRedo[:0]
}

// layoutEditUndo / layoutEditRedo swap the live rects with a history snapshot; driven by editorUndoChord because Ctrl
// chords never reach keyPressed.
func (a *App) layoutEditUndo() {
	themeName, _ := a.d.Prefs.Load().Theme()
	if len(a.editUndo) == 0 || themeName == "" {
		return
	}
	a.editRedo = append(a.editRedo, a.layoutSnapshotNow())
	snap := a.editUndo[len(a.editUndo)-1]
	a.editUndo = a.editUndo[:len(a.editUndo)-1]
	a.restoreLayout(themeName, snap)
}

func (a *App) layoutEditRedo() {
	themeName, _ := a.d.Prefs.Load().Theme()
	if len(a.editRedo) == 0 || themeName == "" {
		return
	}
	a.editUndo = append(a.editUndo, a.layoutSnapshotNow())
	snap := a.editRedo[len(a.editRedo)-1]
	a.editRedo = a.editRedo[:len(a.editRedo)-1]
	a.restoreLayout(themeName, snap)
}

// restoreLayout applies a snapshot and re-syncs saved overrides so undo survives a theme reload.
func (a *App) restoreLayout(themeName string, snap layoutSnapshot) {
	for k := range a.themeRects {
		r, ok := snap.rects[k]
		if !ok {
			continue
		}
		a.themeRects[k] = r
		if k == themeTabBarKey && a.tabBarSeeded {
			continue // placement is explicit, not geometric — restored below
		}
		if r == a.themeRectsOrig[k] {
			a.d.Prefs.ClearThemeRectOverride(themeName, k)
		} else {
			a.d.Prefs.SetThemeRectOverride(themeName, k, [4]int{r.X, r.Y, r.W, r.H})
		}
	}
	// The strip's override is its placement flag, so equal-to-original can't decide it; restore the recorded flag.
	// Only meaningful when the key was synthesized: a theme that declares it is parked unconditionally.
	if r, present := a.themeRects[themeTabBarKey]; present && a.tabBarSeeded {
		if snap.parked {
			a.d.Prefs.SetThemeRectOverride(themeName, themeTabBarKey, [4]int{r.X, r.Y, r.W, r.H})
		} else {
			a.d.Prefs.ClearThemeRectOverride(themeName, themeTabBarKey)
		}
	}
	a.invalidateThemeCanvases()
}

// startLayoutEdit arms the themed editor, closing open modals, which would be unusable behind it and stop the overlay
// drawing.
func (a *App) startLayoutEdit() {
	a.layoutEdit = true
	// The compact toolbox stays as the user set it; it draws after the courtroom pass, with input unblocked.
	a.closeEditorBlockingOverlays()
	a.editTgt = noTarget()
	a.editDrag = 0
	a.editEdges = 0
	a.layoutSnap = true        // tidy placement by default; toggle off in the editor
	a.layoutProfileCursor = -1 // no saved profile applied via the banner chip yet this edit
	// layoutMagnetOff is not reset: the sibling magnet also applies in normal play, so its setting must persist.
	a.editUndo, a.editRedo = nil, nil
}

// closeEditorBlockingOverlays shuts everything that would strand a layout editor or float, inert, over it, for both
// editors.
// Return-to-top modals end the pass before the overlay draws; floating panels would paint over the stage with dead
// widgets.
// The menu bar would hold the modal block invisibly. A focused field would keep eating keys, including the nudge and
// Esc.
func (a *App) closeEditorBlockingOverlays() {
	a.closeCourtroomModals()
	a.showEvid, a.showModcall, a.showPair = false, false, false
	a.showModDash, a.banBoxKind, a.showCMPanel = false, 0, false
	a.showDebugPanel, a.showFxPicker = false, false
	a.closeMenuBar()
	a.ctx.focusID = ""
}

// stopLayoutEdit disarms the editor and unblocks input.
func (a *App) stopLayoutEdit() {
	a.layoutEdit = false
	a.editTgt = noTarget()
	a.editDrag = 0
	a.editEdges = 0
	a.ctx.modalOn = false
}

// openLayoutEditor opens from a menu: the theme's design rects on a themed courtroom, otherwise the classic box editor.
func (a *App) openLayoutEditor() {
	// Leave theater first, or the editor arms behind a view that never draws it.
	if a.theaterOn {
		a.setTheater(false)
	}
	if a.themeLay.valid && a.d.Prefs.Load().ThemeLayoutOn {
		a.startLayoutEdit()
		return
	}
	a.startClassicEdit()
}

// layoutEditFence claims the pointer for the editor before themed widgets draw, so they stay inert.
func (a *App) layoutEditFence() {
	if a.layoutEdit {
		a.ctx.modalOn = true // hovering() blanks everywhere; the editor uses raw pointIn
	}
}

// pointIn is the editor's raw hit test, because hovering() is deliberately blocked.
func pointIn(x, y int32, r sdl.Rect) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// drawLayoutEditor paints the overlay and owns every interaction; called last from the themed courtroom draw.
func (a *App) drawLayoutEditor(w, h int32, lay *themeLayoutCache) {
	c := a.ctx
	themeName, _ := a.d.Prefs.Load().Theme()
	if themeName == "" || lay.scaleX <= 0 || lay.scaleY <= 0 {
		a.stopLayoutEdit()
		return
	}

	// Banner chips use raw hit tests. The nudge hint sits early because the line clips from the right on a narrow
	// window.
	banner := "LAYOUT EDIT — drag = move, arrows = nudge 1 px (Ctrl = grid), corner grip = resize, Tab = cycle, R = rotate, right-click = reset, Ctrl+Z/Y = undo, Esc = exit"
	a.noteEditorBanner() // the top band is ours this frame, so the menu bar stands down (menubar.go)
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: layoutBannerH}, sdl.Color{R: 0, G: 0, B: 0, A: 210})
	doneBtn := sdl.Rect{X: w - 70 - pad, Y: 2, W: 70, H: 22}
	resetBtn := sdl.Rect{X: doneBtn.X - 96, Y: 2, W: 90, H: 22}
	snapBtn := sdl.Rect{X: resetBtn.X - 106, Y: 2, W: 100, H: 22}
	magnetBtn := sdl.Rect{X: snapBtn.X - editChipMagnetW - 6, Y: 2, W: editChipMagnetW, H: 22}
	profileBtn := sdl.Rect{X: magnetBtn.X - editChipProfileW - 6, Y: 2, W: editChipProfileW, H: 22}
	snapLabel := "Snap: off"
	if a.layoutSnap {
		snapLabel = "Snap: on"
	}
	magnetLabel := "Magnet: on"
	if a.layoutMagnetOff {
		magnetLabel = "Magnet: off"
	}
	// The help line clips before the leftmost chip drawn, or it runs under them on a small window.
	leftmostChipX := magnetBtn.X
	if a.hasLayoutProfiles() {
		leftmostChipX = profileBtn.X
	}
	c.LabelClipped(pad, 5, leftmostChipX-pad-editBannerHintGap, banner, ColTierYellow)
	a.rawChip(doneBtn, "Done")
	a.rawChip(resetBtn, "Reset all")
	a.rawChip(snapBtn, snapLabel)
	a.rawChip(magnetBtn, magnetLabel)
	if name := a.currentLayoutProfileLabel(); name != "" {
		a.rawChip(profileBtn, name)
	}

	pressed := c.mouseDown && !a.editPrev
	a.editPrev = c.mouseDown

	// Undo and redo fire from editorUndoChord: Ctrl chords ride c.hotkey, never c.keyPressed.

	if c.escPressed || (c.clicked && pointIn(c.mouseX, c.mouseY, doneBtn)) {
		a.stopLayoutEdit()
		return
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, resetBtn) {
		a.pushLayoutUndo()
		a.d.Prefs.ClearThemeRectOverride(themeName, "")
		// Reset all clears tilt overrides too, as the classic editor does.
		a.d.Prefs.ClearThemeRectRotation(themeName, "")
		for k, r := range a.themeRectsOrig {
			a.themeRects[k] = r
		}
		a.invalidateThemeCanvases()
		a.pushDebug("layout edit: all overrides reset for " + themeName)
		return
	}

	if c.clicked && pointIn(c.mouseX, c.mouseY, snapBtn) {
		a.layoutSnap = !a.layoutSnap
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, magnetBtn) {
		a.layoutMagnetOff = !a.layoutMagnetOff // persistent sibling-magnet toggle (session-only, like Snap)
	}
	if c.clicked && a.hasLayoutProfiles() && pointIn(c.mouseX, c.mouseY, profileBtn) {
		a.cycleLayoutProfile() // apply the next saved full-state profile (applyProfile)
	}

	// Editable keys (skip the design canvas + chatbox children).
	keys := make([]string, 0, len(lay.r))
	for k := range lay.r {
		if themeKeyEditable(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	// Boxes under the cursor, smallest first; Tab cycles them, so a big box under a small one is reachable.
	var stack []string
	for _, k := range keys {
		if pointIn(c.mouseX, c.mouseY, lay.r[k]) {
			stack = append(stack, k)
		}
	}
	sort.SliceStable(stack, func(i, j int) bool {
		ri, rj := lay.r[stack[i]], lay.r[stack[j]]
		return int64(ri.W)*int64(ri.H) < int64(rj.W)*int64(rj.H)
	})
	// The selected design key; an element target is invisible to this overlay.
	editKey := a.editTgt.designKey()
	hoverKey := ""
	switch {
	case a.editDrag != 0:
		hoverKey = editKey // mid-drag: keep the grabbed box highlighted
	case len(stack) > 0:
		if sig := strings.Join(stack, "\x00"); sig != a.editPickSig {
			a.editPickSig, a.editPickIdx = sig, 0 // a new stack under the cursor
		}
		if c.keyPressed == sdl.K_TAB {
			a.editPickIdx++
			c.keyPressed = 0
		}
		a.editPickIdx %= len(stack)
		hoverKey = stack[a.editPickIdx]
	default:
		a.editPickSig, a.editPickIdx = "", 0
	}

	// R rotates the hovered widget's art 90 degrees, Shift+R by 15, on rotatable keys only.
	// Not undoable, like the classic rotation; consumed so a char keybind on R can't fire.
	if a.editDrag == 0 && hoverKey != "" && c.keyPressed == sdl.K_r {
		a.cycleThemeRotation(themeName, hoverKey, sdl.GetModState()&sdl.KMOD_SHIFT != 0)
		c.keyPressed = 0
	}

	// A press over the compact toolbox must not also grab or reset the box under it.
	overToolbox := a.editOverToolbox(w, h)

	// Begin a drag on press. Resize takes priority and reaches the largest box gripped, so a small box on its corner
	// can't block it; otherwise move the armed box.
	// Design keys only: this overlay edits prefs and can't persist an element edit, and elements would need a priority
	// of their own (editorProbe).
	if pressed && a.editDrag == 0 && c.mouseY > layoutBannerH && !overToolbox {
		resizeKey, resizeEdges := "", uint8(0)
		var gripArea int64 = -1
		for _, k := range keys {
			r := lay.r[k]
			e := handleGripAt(r, resizeEdgesFor(designTarget(k)), c.mouseX, c.mouseY)
			if e == 0 {
				continue
			}
			if area := int64(r.W) * int64(r.H); area > gripArea {
				resizeKey, resizeEdges, gripArea = k, e, area
			}
		}
		switch {
		case resizeKey != "":
			a.editTgt, a.editDrag, a.editEdges = designTarget(resizeKey), 2, resizeEdges // resize
			editKey = resizeKey
		case hoverKey != "":
			a.editTgt, a.editDrag, a.editEdges = designTarget(hoverKey), 1, 0 // move
			editKey = hoverKey
		}
		if a.editDrag != 0 {
			a.editStart = [2]int32{c.mouseX, c.mouseY}
			a.editBase = a.editBaseFor(editKey, lay)
			a.pushLayoutUndo() // snapshot before the move/resize (popped at release if it was a no-op)
		}
	}
	// Right-click resets the hovered widget to the theme's own rect.
	if c.rightClicked && hoverKey != "" && !overToolbox {
		if orig, ok := a.themeRectsOrig[hoverKey]; ok {
			a.pushLayoutUndo()
			a.themeRects[hoverKey] = orig
			a.d.Prefs.ClearThemeRectOverride(themeName, hoverKey)
			// A per-piece reset clears the piece's tilt too, mirroring clearClassicSlot.
			a.d.Prefs.ClearThemeRectRotation(themeName, hoverKey)
			a.invalidateThemeCanvases()
		}
	}

	// Live drag: theme widgets map screen deltas back through the layout scale. The tab strip takes the raw cursor
	// delta because its drag is screen-space throughout and the stored value is tabStripCacheRect's exact inverse, so
	// nothing rounds.
	if a.editDrag != 0 && c.mouseDown && editKey != "" {
		screenDrag := editKey == themeTabBarKey
		dx, dy := int(c.mouseX-a.editStart[0]), int(c.mouseY-a.editStart[1])
		if !screenDrag {
			dx = int(float64(c.mouseX-a.editStart[0]) / lay.scaleX)
			dy = int(float64(c.mouseY-a.editStart[1]) / lay.scaleY)
		}
		r := a.editBase
		if a.editDrag == 1 {
			r.X += dx
			r.Y += dy
		} else {
			r = resizeDesignRect(a.editBase, a.editEdges, dx, dy)
		}
		a.alignGuides = a.alignGuides[:0] // reset this drag frame's guides (mirror classiclayout.go)
		// Shift bypasses the grid and the magnet together, like the classic editor.
		if a.layoutSnap && !magnetBypassed() { // round to the grid so widgets line up cleanly
			if a.editDrag == 1 {
				// A theme's widgets round in design space; the tab strip rounds so its painted box lands on the
				// window's grid.
				if screenDrag {
					r = snapTabStripScreen(r, lay)
				} else {
					r.X = snapDesign(r.X)
					r.Y = snapDesign(r.Y)
				}
			} else {
				r = snapDesignResize(r, a.editEdges)
			}
			// Piece-to-piece magnet after the grid. Theme widgets align in design space with the design courtroom as
			// extent; the strip aligns in screen space against painted rects with the window as extent.
			// alignRect floors a snapped resize at classicMinPx=20, not layoutMinDesignPx=16: cosmetic, on the snapped
			// axis only.
			switch court, ok := a.themeRectsOrig["courtroom"]; {
			case screenDrag:
				a.themeAlignScratch = a.themeAlignScratch[:0]
				for k, sr := range lay.r {
					if k == editKey || !themeKeyEditable(k) {
						continue
					}
					a.themeAlignScratch = append(a.themeAlignScratch, sr)
				}
				// The painted size, never r.W/r.H, which are inert for this key, or the magnet would snap the wrong
				// edge.
				dr := sdl.Rect{X: int32(r.X) + lay.offX, Y: int32(r.Y) + lay.offY, W: lay.tabStripW, H: tabBarH}
				dr, a.alignGuides = alignRect(dr, a.themeAlignScratch, w, h, true, 0, a.alignGuides)
				r.X, r.Y = int(dr.X-lay.offX), int(dr.Y-lay.offY)
			case ok:
				a.themeAlignScratch = a.themeAlignScratch[:0]
				for k, tr := range a.themeRects {
					if k == editKey || !themeKeyEditable(k) {
						continue
					}
					a.themeAlignScratch = append(a.themeAlignScratch, a.magnetSiblingRect(k, tr, lay))
				}
				dr := sdl.Rect{X: int32(r.X), Y: int32(r.Y), W: int32(r.W), H: int32(r.H)}
				// The gripped edges feed the magnet, so every handle can snap flush; alignRect moves each of the four.
				var edges uint8
				if a.editDrag != 1 {
					edges = a.editEdges
				}
				dr, a.alignGuides = alignRect(dr, a.themeAlignScratch, int32(court.W), int32(court.H), a.editDrag == 1, edges, a.alignGuides)
				r.X, r.Y, r.W, r.H = int(dr.X), int(dr.Y), int(dr.W), int(dr.H)
			}
		}
		// Keep it on the stage. The tab strip is client chrome whose home is above the canvas, so it clamps to the
		// window.
		if screenDrag {
			r = clampTabStripScreen(r, lay, w, h)
		} else if court, ok := a.themeRectsOrig["courtroom"]; ok {
			r = clampDesignRectToCanvas(r, court) // shared with the arrow-key nudge (layoutnudge.go)
		}
		a.themeRects[editKey] = r
		a.invalidateThemeCanvases()
	}
	// Release persists the edit.
	if a.editDrag != 0 && !c.mouseDown {
		if editKey != "" {
			r := a.themeRects[editKey]
			if r == a.editBase { // a click with no move: discard the begin snapshot
				if n := len(a.editUndo); n > 0 {
					// The strip's drag base is its painted box, so even a no-move drag changed themeRects; put back the
					// pre-press value.
					if editKey == themeTabBarKey {
						if pre, ok := a.editUndo[n-1].rects[editKey]; ok {
							a.themeRects[editKey] = pre
						}
					}
					a.editUndo = a.editUndo[:n-1]
				}
			} else {
				a.d.Prefs.SetThemeRectOverride(themeName, editKey, [4]int{r.X, r.Y, r.W, r.H})
			}
		}
		a.editDrag = 0
		a.editEdges = 0
		// The release changes the layout: the strip's in-flight state drops and a no-op drag restores the stored rect,
		// so invalidate now instead of waiting for the next key probe.
		a.invalidateThemeCanvases()
	}

	// Overlay: every editable rect outlined and named. All handles only on the active box, one grip on the rest, so a
	// forty-widget theme doesn't become hundreds of bright squares.
	for _, k := range keys {
		r := lay.r[k]
		col := ColAccent
		active := false
		switch k {
		case editKey:
			col, active = ColDanger, true
		case hoverKey:
			col, active = ColTierYellow, true
		}
		c.Border(r, col)
		// A move-only key paints no grip: an affordance that does nothing is worse than none.
		if m := handleGripMask(r, resizeEdgesFor(designTarget(k))); m != 0 {
			for i, hnd := range classicHandles(r) {
				if m&(1<<uint(i)) == 0 {
					continue
				}
				if !active && i != handleBottomRight {
					continue
				}
				c.Fill(hnd, col)
			}
		}
		c.LabelClipped(r.X+3, r.Y+2, r.W-6, k, col)
	}
	// Magnet guides, drawn in the space the magnet ran in: design guides map through the layout scale, the strip's are
	// already screen px.
	if a.editDrag != 0 {
		screenGuides := editKey == themeTabBarKey
		for _, g := range a.alignGuides {
			if g.vertical {
				x := lay.offX + int32(float64(g.pos)*lay.scaleX)
				if screenGuides {
					x = g.pos
				}
				c.Fill(sdl.Rect{X: x, Y: 0, W: 1, H: h}, ColTierGreen)
			} else {
				y := lay.offY + int32(float64(g.pos)*lay.scaleY)
				if screenGuides {
					y = g.pos
				}
				c.Fill(sdl.Rect{X: 0, Y: y, W: w, H: 1}, ColTierGreen)
			}
		}
	}
	if editKey != "" {
		r := a.themeRects[editKey]
		// The strip is stored in canvas-relative client px, so the readout must not claim design px.
		units := "design px"
		if editKey == themeTabBarKey {
			units = "client px from the canvas origin"
		}
		c.Label(pad, h-22, fmt.Sprintf("%s: x=%d y=%d w=%d h=%d (%s)", editKey, r.X, r.Y, r.W, r.H, units), ColText)
	}
	// Stacked-boxes hint: when several boxes overlap under the cursor, surface that Tab cycles them.
	if a.editDrag == 0 && len(stack) > 1 {
		c.Label(pad, h-40, fmt.Sprintf("%s — %d boxes stacked here, Tab to cycle (%d/%d)", hoverKey, len(stack), a.editPickIdx+1, len(stack)), ColTierYellow)
	}
	// Rotation readout beside the banner chips, shown only when the hovered or selected widget has a nonzero angle.
	rotKey := hoverKey
	if editKey != "" {
		rotKey = editKey
	}
	if rotKey != "" && a.themeLay.ang != nil {
		if label := rotationChipLabel(a.themeLay.ang[rotKey]); label != "" {
			// Left of the profile or magnet chip so it never paints over them.
			rotRightX := magnetBtn.X
			if a.hasLayoutProfiles() {
				rotRightX = profileBtn.X
			}
			a.rawChip(sdl.Rect{X: rotRightX - 84, Y: 2, W: 78, H: 22}, label)
		}
	}
}

// editBaseFor is the rect a drag starts from, in the space that key's drag is tracked in: the stored design rect for a
// theme's widgets.
// The tab strip's entry in lay.r is the live painted rect, not the transform of its stored seed, so starting from the
// seed would teleport it on the first pixel. Its base is the painted box in canvas-relative client px,
// tabStripCacheRect's exact inverse with no scale or rounding. Y is negative while docked, because the chrome band is
// above the canvas.
func (a *App) editBaseFor(key string, lay *themeLayoutCache) theme.Rect {
	if key != themeTabBarKey {
		return a.themeRects[key]
	}
	box, ok := lay.rect(key)
	if !ok {
		return a.themeRects[key]
	}
	return theme.Rect{
		X: int(box.X - lay.offX),
		Y: int(box.Y - lay.offY),
		W: int(box.W),
		H: int(box.H),
	}
}

// magnetSiblingRect is the box a design-space snapped drag may snap to: the stored rect, except for the tab strip,
// whose stored entry may be the unpainted seed, so it uses the painted rect mapped back to design units.
func (a *App) magnetSiblingRect(key string, stored theme.Rect, lay *themeLayoutCache) sdl.Rect {
	if key == themeTabBarKey {
		if box, ok := lay.rect(key); ok && lay.scaleX > 0 && lay.scaleY > 0 {
			return sdl.Rect{
				X: int32(float64(box.X-lay.offX) / lay.scaleX),
				Y: int32(float64(box.Y-lay.offY) / lay.scaleY),
				W: int32(float64(box.W) / lay.scaleX),
				H: int32(float64(box.H) / lay.scaleY),
			}
		}
	}
	return sdl.Rect{X: int32(stored.X), Y: int32(stored.Y), W: int32(stored.W), H: int32(stored.H)}
}

// clampTabStripScreen keeps the strip's stored origin inside the window, in lockstep with the cache's paint clamp so
// dragging past an edge opens no dead zone. Stops come from the chips' size; r.W/r.H are inert for this key.
func clampTabStripScreen(r theme.Rect, lay *themeLayoutCache, w, h int32) theme.Rect {
	clamp := func(v, lo, hi int) int {
		if hi < lo {
			hi = lo
		}
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	r.X = clamp(r.X, int(-lay.offX), int(w-lay.tabStripW-lay.offX))
	r.Y = clamp(r.Y, int(-lay.offY), int(h-tabBarH-lay.offY))
	return r
}

// rawChip draws a chip using a raw hit test, which the editor's input block doesn't affect.
func (a *App) rawChip(r sdl.Rect, label string) {
	c := a.ctx
	bg := ColPanel
	if pointIn(c.mouseX, c.mouseY, r) {
		bg = ColPanelHi
	}
	c.Fill(r, bg)
	c.Border(r, ColAccent)
	c.LabelClipped(r.X+6, r.Y+3, r.W-12, label, ColText)
}

// applyRectOverrides lays the active theme's saved edits over a fresh design map after a theme load.
// The tab strip stores an unused W and H in the fixed [4]int: its size is derived from its chips at every use.
func (a *App) applyRectOverrides(rects map[string]theme.Rect) map[string]theme.Rect {
	themeName, _ := a.d.Prefs.Load().Theme()
	ov := a.d.Prefs.Load().ThemeRectOverrides(themeName)
	if len(ov) == 0 {
		return rects
	}
	for k, v := range ov {
		if _, exists := rects[k]; exists && themeKeyEditable(k) {
			rects[k] = theme.Rect{X: v[0], Y: v[1], W: v[2], H: v[3]}
		}
	}
	return rects
}
