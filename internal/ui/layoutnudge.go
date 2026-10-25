package ui

// Arrow-key nudging in the two layout editors.
// Everything a nudge writes goes through the SAME state a mouse drag's release writes,
// so a nudged widget persists, undoes with the editor's existing Ctrl+Z, and is put back
// by right-click reset and "Reset all" with no extra bookkeeping.

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// layoutNudgePx is the plain arrow key's step. ONE is the entire point of the
	// request — it is the precision the pointer cannot give — so it is named here
	// rather than spelled as a bare literal at three call sites (hard rule 9).
	layoutNudgePx = 1
	// editBannerHintGap is the clearance between an editor banner's help line and the
	// leftmost chip of its button row, in screen px. Shared by both banners so the two
	// cannot drift; it is what keeps the (now longer) help text from running under
	// Snap / Reset all / Done on a small window.
	editBannerHintGap = 10
)

// editorDrawSiteRuns reports whether an armed layout editor's own draw actually REACHES
// the screen this frame.
// The server-tab strip's paint-site predicate needs exactly this question answered
// (tabStripPaintsUnderEditor, chrometop.go), which is why it delegates here instead of
// carrying a second copy of the conjunction.
func (a *App) editorDrawSiteRuns() bool {
	return a.layoutEditorArmed() && a.screen == ScreenCourtroom &&
		a.link.room != nil && a.link.sess != nil && !a.theaterOn && !a.screenDispatchPreempted()
}

// keyboardSurfaceOverEditor reports whether some OTHER keyboard surface owns this
// frame's arrow keys even though an editor is armed.
// Two questions, not one, because they can disagree: clicking the palette's own
// chrome blurs the search field while the row selection stays live, and a field can
// be focused with no palette at all. focusID != "" is the house test every other
// It cannot strand the nudge behind a field the user cannot see: arming either
// editor drops the focus (closeEditorBlockingOverlays), so the only way back into
// this branch is a surface the user opened AFTER arming — one they can close.
func (palette1 *paletteState) keyboardSurfaceOverEditor(e *env) bool {
	return palette1.paletteOpen || e.ctx.focusID != ""
}

// nudgeKeyDir maps an arrow keycode to a unit step in screen/design orientation (Y grows
// downward, matching every rect in the client). ok=false for every other key.
func nudgeKeyDir(key sdl.Keycode) (dx, dy int, ok bool) {
	switch key {
	case sdl.K_LEFT:
		return -1, 0, true
	case sdl.K_RIGHT:
		return 1, 0, true
	case sdl.K_UP:
		return 0, -1, true
	case sdl.K_DOWN:
		return 0, 1, true
	}
	return 0, 0, false
}

// takeNudgeKey reads this frame's nudge off the TWO channels an armed editor owns and
// CONSUMES it.
// Consumes whichever channel it read even when the nudge then turns out to be a no-op
// (nothing selected, or the widget is already against a stop). That is the same promise
// editorUndoChord makes for Ctrl+Z/Y: while an editor is armed, a key it owns can never
func takeNudgeKey(c *Ctx) (dx, dy int, coarse, ok bool) {
	if dx, dy, ok = nudgeKeyDir(c.hotkey); ok {
		c.hotkey = 0
		return dx, dy, true, true
	}
	if dx, dy, ok = nudgeKeyDir(c.keyPressed); ok {
		c.keyPressed = 0
		return dx, dy, false, true
	}
	return 0, 0, false, false
}

// gridStepTo returns the grid line strictly beyond v in direction dir — "move to the
// next line", not "move by one line's worth". That is what makes the coarse nudge land
// on the SAME lattice a snapped drag lands on (snapDesign / classicSnapTo round to it)
// from any starting offset, including a widget the theme authored off-grid.
func gridStepTo(v, dir, g int) int {
	if g <= 0 {
		g = layoutGridDesign
	}
	q := v / g
	if v%g != 0 && v < 0 {
		q--
	}
	if dir > 0 {
		return (q + 1) * g
	}
	if v%g != 0 {
		return q * g // v sits between two lines: the floor is already strictly below it
	}
	return (q - 1) * g
}

// nudgeCoord steps ONE coordinate of the selected widget.
func nudgeCoord(v, dir int, coarse bool, phase, grid int) int {
	switch {
	case dir == 0:
		return v
	case !coarse:
		return v + dir*layoutNudgePx
	default:
		return gridStepTo(v+phase, dir, grid) - phase
	}
}

// editorNudgeKeys routes this frame's arrow key to the armed layout editor and reports
// whether it belonged to one (and was therefore consumed).
func (a *App) editorNudgeKeys(w, h int32) bool {
	if !a.editorDrawSiteRuns() || a.palette.keyboardSurfaceOverEditor(&a.env) {
		return false // no editor owns the screen: arrows belong to whoever else wants them
	}
	dx, dy, coarse, ok := takeNudgeKey(a.ctx)
	if !ok {
		return false
	}
	// ONE DISPATCH, ON THE TARGET'S OWN SPACE (edittarget.go, W6). It used to be a
	// switch over which EDITOR was armed, which answered the same question only while
	// each editor could hold exactly one kind of thing. The themed editor can hold two
	// from W7 — a design rect or a free element — and the space says which without
	// anyone inspecting the payload.
	tgt := a.editSelection()
	switch tgt.space {
	case spaceDesign:
		a.nudgeThemedRect(tgt, dx, dy, coarse, w, h)
	case spaceElement:
		a.nudgeThemeElement(tgt, dx, dy, coarse)
	case spaceClassic:
		a.classic.nudgeClassicSlot(&a.env, tgt, dx, dy, coarse, w, h)
	}
	return true
}

// editSelection is the armed editor's current target.
func (a *App) editSelection() editTarget {
	switch {
	case a.layoutEdit:
		return a.editTgt
	case a.classic.classicEdit:
		return a.classic.classicTgt
	}
	return noTarget()
}

// nudgeThemedRect moves the THEMED editor's selected DESIGN rect.
func (a *App) nudgeThemedRect(tgt editTarget, dx, dy int, coarse bool, w, h int32) {
	key := tgt.designKey()
	if key == "" || !themeKeyEditable(key) || a.editDrag != 0 {
		return
	}
	themeName, _ := a.d.Prefs.Load().Theme()
	if themeName == "" {
		return // the editor disarms itself with no theme; nothing to persist against
	}
	lay := &a.skin.themeLay
	if !lay.valid {
		return
	}
	// lay.rect, NOT a bare lay.r probe: rect also rejects a degenerate (W<=0 || H<=0)
	if _, drawn := lay.rect(key); !drawn {
		return // hidden, omitted or clamped out of the canvas this frame: no box to move
	}
	// editBaseFor, not a.themeRects[key]: for a theme's own widgets the two are the same
	// rect, and for the server-tab strip only this one is the preimage of the box the
	// user is actually looking at (an unparked strip's stored entry is the inert
	// synthesized seed). Reusing the drag's own base is also what guarantees a nudge and
	// a drag can never start from different places.
	base := a.editBaseFor(key, lay)
	r := base
	if key == themeTabBarKey {
		r.X = nudgeCoord(r.X, dx, coarse, int(lay.offX), layoutGridDesign)
		r.Y = nudgeCoord(r.Y, dy, coarse, int(lay.offY), layoutGridDesign)
		r = clampTabStripScreen(r, lay, w, h)
	} else {
		r.X = nudgeCoord(r.X, dx, coarse, 0, layoutGridDesign)
		r.Y = nudgeCoord(r.Y, dy, coarse, 0, layoutGridDesign)
		if court, ok := a.skin.themeRectsOrig["courtroom"]; ok {
			r = clampDesignRectToCanvas(r, court)
		}
	}
	if r == base {
		return // hard against a stop: no move, and so no undo entry to trip over later
	}
	a.pushLayoutUndo()
	a.skin.themeRects[key] = r
	// The same write the drag's release makes. For the strip this override IS its
	// placement flag (tabStripPlacementRecorded), so a nudged strip is "placed" exactly
	// as a dragged one is — it leaves the chrome band and stays where it was put.
	a.d.Prefs.SetThemeRectOverride(themeName, key, [4]int{r.X, r.Y, r.W, r.H})
	a.invalidateThemeCanvases()
}

// nudgeThemeElement moves a FREE ELEMENT — the third space, and the whole reason the
// nudge dispatches on a target instead of on which editor is armed (W6).
func (a *App) nudgeThemeElement(tgt editTarget, dx, dy int, coarse bool) {
	idx, ok := tgt.elemIdx()
	sc := a.elemBake.themeSidecar
	if !ok || sc == nil || idx < 0 || idx >= len(sc.Elements) {
		return
	}
	if a.editDrag != 0 {
		return // a drag in flight keeps the mouse in charge, exactly as on the design arm
	}
	el := &sc.Elements[idx]
	if el.Locked {
		return // the editor-only lock (theme.Element.Locked): no drag, no nudge
	}
	r := el.Rect
	r.X = nudgeCoord(r.X, dx, coarse, 0, layoutGridDesign)
	r.Y = nudgeCoord(r.Y, dy, coarse, 0, layoutGridDesign)
	if r == el.Rect {
		return // nothing moved, so nothing to re-bake
	}
	el.Rect = r
	a.invalidateThemeCanvases()
}

// clampDesignRectToCanvas keeps a themed widget on the stage, in DESIGN px. Shared with
// drawLayoutEditor's drag so a nudge cannot stop anywhere a drag would not.
func clampDesignRectToCanvas(r theme.Rect, court theme.Rect) theme.Rect {
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	if r.X+r.W > court.W {
		r.X = court.W - r.W
	}
	if r.Y+r.H > court.H {
		r.Y = court.H - r.H
	}
	return r
}

// nudgeClassicSlot moves the CLASSIC (default-layout) editor's selected slot, in screen
// px, through the same override → fraction → prefs path a drag release takes.
func (classic1 *classicState) nudgeClassicSlot(e *env, tgt editTarget, dx, dy int, coarse bool, w, h int32) {
	key := tgt.classicKey()
	if key == "" || classic1.classicEditDrag != 0 {
		return
	}
	base, ok := classic1.classicSlotCurrentRect(key, w, h)
	if !ok {
		return
	}
	// The classic editor's grid step is user-tunable (the banner's Grid chip,
	// config.Settings.LayoutGridSize), so the coarse nudge reads it rather than the themed
	// editor's fixed one — otherwise Ctrl+arrow would land off the lattice the Snap
	// chip is drawing the widget onto.
	grid := int(e.d.Prefs.Load().LayoutGridSize())
	r := base
	r.X = int32(nudgeCoord(int(r.X), dx, coarse, 0, grid))
	r.Y = int32(nudgeCoord(int(r.Y), dy, coarse, 0, grid))
	r = clampSlotToWindow(r, w, h)
	if r == base {
		return
	}
	classic1.pushClassicUndo()
	if classic1.classicOv == nil {
		classic1.classicOv = make(map[string][4]float64, classicSlotRegCap)
	}
	ov := rectToFrac(r, w, h)
	classic1.classicOv[key] = ov
	classic1.syncAnchorWindow(key, w, h) // the override now describes THIS window size
	e.d.Prefs.SetClassicSlot(key, ov)
	if m := classic1.slotAnchorMode(key); m != "" {
		e.d.Prefs.SetClassicAnchor(key, config.ClassicAnchor{Mode: m, WinW: int(w), WinH: int(h)})
	}
}

// classicSlotCurrentRect is the selected slot's rect right now.
func (classic1 *classicState) classicSlotCurrentRect(key string, w, h int32) (sdl.Rect, bool) {
	if ov, has := classic1.classicOv[key]; has {
		return classic1.anchoredRect(key, ov, w, h), true
	}
	info, reg := classic1.slotReg[key]
	return info.cur, reg
}

// clampSlotToWindow keeps a classic slot on screen. Shared with drawClassicEditor's drag
// so the two stop in the same places — including the deliberate order, which leaves a box
// WIDER than the window flush with its right edge rather than its left.
func clampSlotToWindow(r sdl.Rect, w, h int32) sdl.Rect {
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	if r.X+r.W > w {
		r.X = w - r.W
	}
	if r.Y+r.H > h {
		r.Y = h - r.H
	}
	return r
}
