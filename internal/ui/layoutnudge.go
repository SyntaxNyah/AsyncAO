package ui

// Arrow-key nudging in the two layout editors: a plain arrow moves one pixel, Ctrl+arrow steps to the next line of the
// editor's snap grid.
// A nudge is one pixel of the space the widget is stored in, or it wouldn't round-trip: screen px for a classic box and
// the tab strip, design px for a theme widget or free element.
// A nudge writes the same state as a drag release, so it persists, undoes and resets the same way.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// layoutNudgePx is the plain arrow key's step.
	layoutNudgePx = 1
	// editBannerHintGap is the clearance between an editor banner's help line and its leftmost chip, in screen px.
	editBannerHintGap = 10
)

// editorDrawSiteRuns reports whether an armed editor's draw reaches the screen this frame; the editor flag can outlive
// its overlay (no room, theater, gif, replay, scene maker), and then it must not eat arrow keys.
func (a *App) editorDrawSiteRuns() bool {
	return a.layoutEditorArmed() && a.screen == ScreenCourtroom &&
		a.room != nil && a.sess != nil && !a.theaterOn && !a.screenDispatchPreempted()
}

// keyboardSurfaceOverEditor reports whether another keyboard surface owns the arrows despite an armed editor: the
// command palette, which can stay open over an editor, or a focused field.
// Both are tested because they can disagree. It can't strand the nudge: arming an editor drops focus.
func (a *App) keyboardSurfaceOverEditor() bool {
	return a.paletteOpen || a.ctx.focusID != ""
}

// nudgeKeyDir maps an arrow keycode to a unit step, Y growing downward; ok=false for any other key.
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

// takeNudgeKey reads and consumes this frame's nudge: Ctrl chords arrive on c.hotkey, plain keys on c.keyPressed.
// Consumed even if the nudge is a no-op, so a key an armed editor owns can't reach a hotkey, caret or recall ring.
// Ctrl means coarser because Shift already means pixel-precise and Alt means force-move in the drag paths.
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

// gridStepTo returns the grid line strictly beyond v in direction dir, so a coarse nudge lands on a snapped drag's
// lines.
// The floor correction matters: the tab strip's stored Y can be negative, and Go's division truncates toward zero.
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

// nudgeCoord steps one coordinate. phase is the offset between the stored coordinate and the one the grid is phased
// against: zero, except the canvas origin for the tab strip, whose grid is the window's.
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

// editorNudgeKeys routes this frame's arrow key to the armed editor, reporting whether one consumed it.
// It runs pre-screen because the editors draw last, after other code has already spent c.keyPressed.
func (a *App) editorNudgeKeys(w, h int32) bool {
	if !a.editorDrawSiteRuns() || a.keyboardSurfaceOverEditor() {
		return false // no editor owns the screen: arrows belong to whoever else wants them
	}
	dx, dy, coarse, ok := takeNudgeKey(a.ctx)
	if !ok {
		return false
	}
	// Dispatch on the target's own space: the themed editor can hold a design rect or a free element.
	tgt := a.editSelection()
	switch tgt.space {
	case spaceDesign:
		a.nudgeThemedRect(tgt, dx, dy, coarse, w, h)
	case spaceElement:
		a.nudgeThemeElement(tgt, dx, dy, coarse)
	case spaceClassic:
		a.nudgeClassicSlot(tgt, dx, dy, coarse, w, h)
	}
	return true
}

// editSelection is the armed editor's target, chosen by armed flag because a stale selection can outlive a theme swap
// in the other editor's field.
func (a *App) editSelection() editTarget {
	switch {
	case a.layoutEdit:
		return a.editTgt
	case a.classicEdit:
		return a.classicTgt
	}
	return noTarget()
}

// nudgeThemedRect moves the themed editor's selected design rect; a drag in flight keeps the mouse in charge.
func (a *App) nudgeThemedRect(tgt editTarget, dx, dy int, coarse bool, w, h int32) {
	key := tgt.designKey()
	if key == "" || !themeKeyEditable(key) || a.editDrag != 0 {
		return
	}
	themeName, _ := a.d.Prefs.Load().Theme()
	if themeName == "" {
		return // the editor disarms itself with no theme; nothing to persist against
	}
	lay := &a.themeLay
	if !lay.valid {
		return
	}
	// lay.rect, not a bare lay.r probe: it rejects a degenerate entry, which editBaseFor would replace with the inert
	// seed, so the nudge would persist a placement at a coordinate nothing painted.
	if _, drawn := lay.rect(key); !drawn {
		return // hidden, omitted or clamped out of the canvas this frame: no box to move
	}
	// editBaseFor, not a.themeRects[key]: for the tab strip only this is the preimage of the visible box.
	base := a.editBaseFor(key, lay)
	r := base
	if key == themeTabBarKey {
		r.X = nudgeCoord(r.X, dx, coarse, int(lay.offX), layoutGridDesign)
		r.Y = nudgeCoord(r.Y, dy, coarse, int(lay.offY), layoutGridDesign)
		r = clampTabStripScreen(r, lay, w, h)
	} else {
		r.X = nudgeCoord(r.X, dx, coarse, 0, layoutGridDesign)
		r.Y = nudgeCoord(r.Y, dy, coarse, 0, layoutGridDesign)
		if court, ok := a.themeRectsOrig["courtroom"]; ok {
			r = clampDesignRectToCanvas(r, court)
		}
	}
	if r == base {
		return // hard against a stop: no move, and so no undo entry to trip over later
	}
	a.pushLayoutUndo()
	a.themeRects[key] = r
	// The same write a drag release makes; for the strip this override is its placement flag.
	a.d.Prefs.SetThemeRectOverride(themeName, key, [4]int{r.X, r.Y, r.W, r.H})
	a.invalidateThemeCanvases()
}

// nudgeThemeElement moves a free element one unit of its own declared Space, with no canvas clamp: an anchored
// element's rect is a signed delta on its anchor and a SpaceWindow element has no canvas.
// It mutates the parsed model directly and isn't persisted, so a background theme apply can discard it; this legacy
// editor can't save anyway. Don't add a second element mutator like this, and don't suppress applies, which re-bake the
// geometry.
func (a *App) nudgeThemeElement(tgt editTarget, dx, dy int, coarse bool) {
	idx, ok := tgt.elemIdx()
	sc := a.themeSidecar
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

// clampDesignRectToCanvas keeps a themed widget on the stage, in design px, for both nudge and drag.
// It moves the box and never shrinks it, because a move must not change authored width: an over-wide box ends with its
// right edge flush, overhanging the left.
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

// nudgeClassicSlot moves the classic editor's selected box in screen px, through the same path as a drag release.
func (a *App) nudgeClassicSlot(tgt editTarget, dx, dy int, coarse bool, w, h int32) {
	key := tgt.classicKey()
	if key == "" || a.classicEditDrag != 0 {
		return
	}
	base, ok := a.classicSlotCurrentRect(key, w, h)
	if !ok {
		return
	}
	// LayoutGridSize is user-tunable; use it, or Ctrl+arrow would land off the lines Snap draws onto.
	grid := int(a.d.Prefs.Load().LayoutGridSize())
	r := base
	r.X = int32(nudgeCoord(int(r.X), dx, coarse, 0, grid))
	r.Y = int32(nudgeCoord(int(r.Y), dy, coarse, 0, grid))
	r = clampSlotToWindow(r, w, h)
	if r == base {
		return
	}
	a.pushClassicUndo()
	if a.classicOv == nil {
		a.classicOv = make(map[string][4]float64, classicSlotRegCap)
	}
	ov := rectToFrac(r, w, h)
	a.classicOv[key] = ov
	a.syncAnchorWindow(key, w, h) // the override now describes THIS window size
	a.d.Prefs.SetClassicSlot(key, ov)
	if m := a.slotAnchorMode(key); m != "" {
		a.d.Prefs.SetClassicAnchor(key, config.ClassicAnchor{Mode: m, WinW: int(w), WinH: int(h)})
	}
}

// classicSlotCurrentRect is the selected box's rect now; an override is resolved live because the registry is a frame
// behind before the courtroom pass, and only a box with no override needs the registry.
func (a *App) classicSlotCurrentRect(key string, w, h int32) (sdl.Rect, bool) {
	if ov, has := a.classicOv[key]; has {
		return a.anchoredRect(key, ov, w, h), true
	}
	info, reg := a.slotReg[key]
	return info.cur, reg
}

// clampSlotToWindow keeps a classic box on screen, shared with the drag; an over-wide box ends flush with its right
// edge.
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
