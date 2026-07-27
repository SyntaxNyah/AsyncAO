package ui

// Arrow-key nudging in the two layout editors.
//
// The report was about PRECISION, not about a missing feature: dropping a widget on an
// exact pixel with the mouse is a fight, and both editors snap to an 8 px grid by
// default, so the pointer alone cannot express "one pixel left". The keyboard can.
//
//   - a PLAIN arrow moves the selected widget exactly ONE pixel;
//   - CTRL + arrow steps to the next line of the editor's own snap grid, so a coarse
//     nudge lands on the same lattice a snapped drag does.
//
// ONE PIXEL OF WHAT. Each editor tracks its widgets in a different space, and a nudge
// has to be one pixel of the space the widget is STORED in, or it would not round-trip:
//
//   - a classic slot lives in screen px (persisted as a window fraction) — one SCREEN px;
//   - a theme's own widget lives in the theme's design canvas — one DESIGN px, which is
//     what its stored rect, its drag (cursor delta ÷ layout scale) and its grid all use;
//   - the server-tab strip is the exception the themed editor already carries: it is
//     intrinsically sized CLIENT chrome whose stored X/Y are canvas-relative CLIENT px
//     (see themeTabBarKey), so one pixel means one SCREEN px at every layout scale.
//     Its whole gesture — base (editBaseFor), delta, grid (snapTabStripScreen), magnet
//     and clamp (clampTabStripScreen) — is already screen-space; the nudge simply joins
//     it rather than re-deriving any of it.
//
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
//
// Deliberately NOT just layoutEditorArmed(): an editor flag outlives the frames where
// its overlay does not run. drawCourtroom bails to the lobby with no room or session,
// returns early in theater mode, and App.Frame's gif / replay / scene-maker modes
// preempt the whole screen dispatch — and on any of those frames the editor is not what
// the user is looking at, so it must not be silently eating their arrow keys.
//
// The server-tab strip's paint-site predicate needs exactly this question answered
// (tabStripPaintsUnderEditor, chrometop.go), which is why it delegates here instead of
// carrying a second copy of the conjunction.
func (a *App) editorDrawSiteRuns() bool {
	return a.layoutEditorArmed() && a.screen == ScreenCourtroom &&
		a.room != nil && a.sess != nil && !a.theaterOn && !a.screenDispatchPreempted()
}

// keyboardSurfaceOverEditor reports whether some OTHER keyboard surface owns this
// frame's arrow keys even though an editor is armed.
//
// An armed editor is NOT exclusive, which is the whole reason this predicate exists.
// handleHotkeys runs unconditionally from both courtroom passes and editorUndoChord
// only diverts Ctrl+Z/Y, so Ctrl+Space still opens the command palette mid-edit;
// closeEditorBlockingOverlays leaves it open (correctly — the palette is not a
// return-to-top modal, it draws AFTER the pass, on top of the editor, so it strands
// nothing) and it is not in courtroomModals. It steers its row selection with ↑/↓
// (palette.go) and its focused search field steers a caret with ←/→ (ui.go), and a
// nudge claiming the key PRE-SCREEN silently kills both.
//
// Two questions, not one, because they can disagree: clicking the palette's own
// chrome blurs the search field while the row selection stays live, and a field can
// be focused with no palette at all. focusID != "" is the house test every other
// plain-key claim already uses to yield to a typing user (handleJukeboxKeys,
// handleMacroKeys, handleICPhraseKeys, the style-preset and showname binds).
//
// It cannot strand the nudge behind a field the user cannot see: arming either
// editor drops the focus (closeEditorBlockingOverlays), so the only way back into
// this branch is a surface the user opened AFTER arming — one they can close.
func (a *App) keyboardSurfaceOverEditor() bool {
	return a.paletteOpen || a.ctx.focusID != ""
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
//
// The split is the house key-routing rule, not a choice made here: HandleEvent puts
// every non-clipboard Ctrl chord on c.hotkey and returns, and plain keys on
// c.keyPressed. So the fine step and the coarse step arrive on different fields by
// construction, which is also why Ctrl is the right modifier for "coarser" — Shift
// already means "pixel-precise, bypass the grid and the magnet" in both editors' drag
// paths, and making it mean the opposite here would invert that in the same two
// functions. Alt already means "force a move instead of a resize" in the classic editor.
//
// Consumes whichever channel it read even when the nudge then turns out to be a no-op
// (nothing selected, or the widget is already against a stop). That is the same promise
// editorUndoChord makes for Ctrl+Z/Y: while an editor is armed, a key it owns can never
// also reach a user-rebound hotkey, a focused field's caret, the IC/OOC recall rings or
// any of the other plain-arrow consumers that draw BEFORE the editors' overlays do.
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
//
// The floor correction matters: the server-tab strip's stored Y is legitimately negative
// while it is docked in the chrome band above the canvas, and Go's integer division
// truncates toward zero, which would step the wrong way there.
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
//
// phase is the distance between the STORED coordinate and the coordinate the editor's
// grid is phased against. It is zero for everything that is stored, drawn and snapped in
// one space, and the canvas origin for the server-tab strip, whose stored value is
// canvas-relative while its grid is the WINDOW's (snapTabStripScreen carries the whole
// argument for why that key's grid lives in window phase).
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
//
// It runs PRE-SCREEN, beside the other app-wide key claims, for the same reason
// editorUndoChord had to be hoisted out of the editors' draw bodies: both editors draw
// LAST in their pass, long after the focused text field, the IC/OOC recall rings, the
// pair-ghost offset nudge and the emote-preview cycler have all had a look at
// c.keyPressed. An in-draw check would have been handed a key that was already spent.
func (a *App) editorNudgeKeys(w, h int32) bool {
	if !a.editorDrawSiteRuns() || a.keyboardSurfaceOverEditor() {
		return false // no editor owns the screen: arrows belong to whoever else wants them
	}
	dx, dy, coarse, ok := takeNudgeKey(a.ctx)
	if !ok {
		return false
	}
	switch {
	case a.layoutEdit:
		a.nudgeThemedRect(dx, dy, coarse, w, h)
	case a.classicEdit:
		a.nudgeClassicSlot(dx, dy, coarse, w, h)
	}
	return true
}

// nudgeThemedRect moves the THEMED editor's selected widget.
//
// a.editKey is the selection: the editor arms it on the press that grabs a box and
// leaves it set after the release (it is the key drawn in ColDanger with the readout at
// the bottom of the screen), so the gesture the user already knows — click the box, then
// arrow it — needs nothing new. A drag in flight keeps the mouse in charge.
func (a *App) nudgeThemedRect(dx, dy int, coarse bool, w, h int32) {
	key := a.editKey
	if key == "" || layoutEditSkip[key] || a.editDrag != 0 {
		return
	}
	themeName, _ := a.d.Prefs.Theme()
	if themeName == "" {
		return // the editor disarms itself with no theme; nothing to persist against
	}
	lay := &a.themeLay
	if !lay.valid {
		return
	}
	// lay.rect, NOT a bare lay.r probe: rect also rejects a degenerate (W<=0 || H<=0)
	// entry, which is the SAME question editBaseFor asks two lines below. A bare probe
	// would let a degenerate key through, editBaseFor would then fall back to
	// a.themeRects[key] — for the tab strip, the inert synthesized seed — and the nudge
	// would move and PERSIST that seed, marking the strip placed at a coordinate nothing
	// ever painted. That is exactly the phantom the drag's no-op discard exists to stop.
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
		if court, ok := a.themeRectsOrig["courtroom"]; ok {
			r = clampDesignRectToCanvas(r, court)
		}
	}
	if r == base {
		return // hard against a stop: no move, and so no undo entry to trip over later
	}
	a.pushLayoutUndo()
	a.themeRects[key] = r
	// The same write the drag's release makes. For the strip this override IS its
	// placement flag (tabStripPlacementRecorded), so a nudged strip is "placed" exactly
	// as a dragged one is — it leaves the chrome band and stays where it was put.
	a.d.Prefs.SetThemeRectOverride(themeName, key, [4]int{r.X, r.Y, r.W, r.H})
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
func (a *App) nudgeClassicSlot(dx, dy int, coarse bool, w, h int32) {
	key := a.classicEditKey
	if key == "" || a.classicEditDrag != 0 {
		return
	}
	base, ok := a.classicSlotCurrentRect(key, w, h)
	if !ok {
		return
	}
	// The classic editor's grid step is user-tunable (the banner's Grid chip,
	// config.LayoutGridSize), so the coarse nudge reads it rather than the themed
	// editor's fixed one — otherwise Ctrl+arrow would land off the lattice the Snap
	// chip is drawing the widget onto.
	grid := int(a.d.Prefs.LayoutGridSize())
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

// classicSlotCurrentRect is the selected slot's rect right now.
//
// An existing override is resolved live (anchoredRect — window pins included) rather
// than read out of the slot registry, because the registry is one frame behind here: the
// nudge runs pre-screen, and the courtroom pass that rebuilds the registry has not run
// yet. Only a slot with NO override needs the registry, and then only for the rect the
// layout computed for it — which is exactly what a first nudge has to start from.
func (a *App) classicSlotCurrentRect(key string, w, h int32) (sdl.Rect, bool) {
	if ov, has := a.classicOv[key]; has {
		return a.anchoredRect(key, ov, w, h), true
	}
	info, reg := a.slotReg[key]
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
