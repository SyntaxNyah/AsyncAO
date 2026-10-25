package ui

// Live layout editor: select a themed widget with the mouse and move it
// across the screen, or grab its corner and shrink/grow it — on the fly,
// over the running courtroom. Edits are DESIGN-space overrides persisted
// per theme (prefs.ThemeRectOverrides), applied on top of the theme's
// courtroom_design.ini whenever it loads, so window resizes keep working
// and the theme's own file is never touched.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// layoutHandlePx is the resize grip (bottom-right corner), screen px.
	layoutHandlePx = 12
	// layoutMinDesignPx floors edited widgets in design space (matches
	// the layout engine's own degenerate-rect rejection with margin).
	layoutMinDesignPx = 16
	// layoutGridDesign is the snap grid in design px — edits round to it when
	// snap is on, so widgets line up cleanly.
	layoutGridDesign = 8
	// layoutBannerH is the height of the themed editor's top banner strip, in screen
	// px — the black band carrying the help line and the Done / Reset all / Snap /
	// Magnet / Profile chips. It is BOTH the paint height and the floor of the drag
	// hit test (a press inside the banner must hit a chip, never a widget under it),
	// so the two can never drift. The classic editor's twin is classicBannerH.
	layoutBannerH = 26
)

// snapDesign rounds a design-space coordinate to the nearest grid line.
func snapDesign(v int) int {
	if v < 0 {
		return 0
	}
	return (v + layoutGridDesign/2) / layoutGridDesign * layoutGridDesign
}

// snapTabStripScreen rounds the server-tab strip's stored origin so that its PAINTED
// box lands on the WINDOW's grid — the grid the user is looking at — rather than on one
// whose phase is the design canvas's origin.
func snapTabStripScreen(r theme.Rect, lay *themeLayoutCache) theme.Rect {
	r.X = snapDesign(r.X+int(lay.offX)) - int(lay.offX)
	r.Y = snapDesign(r.Y+int(lay.offY)) - int(lay.offY)
	return r
}

// themedKeyResizable reports whether a themed key honours the corner-grip resize.
func themedKeyResizable(key string) bool { return key != themeTabBarKey }

// resizeDesignRect applies a resize drag's delta to the GRIPPED EDGES, in design px —
// the design-space twin of drawClassicEditor's own per-edge arithmetic, which is where
// this shape (and the anchored-edge floor below it) comes from.
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

// snapDesignResize rounds a resize to the design grid by snapping the SIZE and holding
// the anchored edge still.
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

// layoutUndoCap bounds the editor's undo/redo stacks (rule §17.4).
const layoutUndoCap = 64

func cloneRects(m map[string]theme.Rect) map[string]theme.Rect {
	cp := make(map[string]theme.Rect, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// layoutSnapshot is one undo/redo step.
// It carries the server-tab strip's PLACED flag beside the rects because that flag is
// not recoverable from them: a drag that lands on the seed's own coordinates is still
// a placement (the placement is read from the persisted override, not the geometry), so
// restoring rects alone would silently un-park the strip and snap it back into the
// chrome band — the same defect on the undo path.
type layoutSnapshot struct {
	rects  map[string]theme.Rect
	parked bool
}

// layoutSnapshotNow captures the current editable state.
func (a *App) layoutSnapshotNow() layoutSnapshot {
	return layoutSnapshot{rects: cloneRects(a.skin.themeRects), parked: a.tabStripPlacementRecorded()}
}

// pushLayoutUndo snapshots the current rects BEFORE an edit and forks history
// (a fresh edit drops the redo stack).
func (a *App) pushLayoutUndo() {
	a.editUndo = append(a.editUndo, a.layoutSnapshotNow())
	if len(a.editUndo) > layoutUndoCap {
		a.editUndo = a.editUndo[1:]
	}
	a.editRedo = a.editRedo[:0]
}

// layoutEditUndo / layoutEditRedo swap the live rect map with a history
// snapshot (and re-sync the persisted overrides via restoreLayout). Driven by
// editorUndoChord — Ctrl chords arrive on the hotkey channel, never on
// keyPressed, so the draw loop can't see them.
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

// restoreLayout applies a snapshot to the live rects AND re-syncs the persisted
// overrides, so undo survives a theme reload (a key back at its original rect
// clears its override; otherwise it's re-written).
func (a *App) restoreLayout(themeName string, snap layoutSnapshot) {
	for k := range a.skin.themeRects {
		r, ok := snap.rects[k]
		if !ok {
			continue
		}
		a.skin.themeRects[k] = r
		if k == themeTabBarKey && a.tabBarSeeded {
			continue // placement is explicit, not geometric — restored below
		}
		if r == a.skin.themeRectsOrig[k] {
			a.d.Prefs.ClearThemeRectOverride(themeName, k)
		} else {
			a.d.Prefs.SetThemeRectOverride(themeName, k, [4]int{r.X, r.Y, r.W, r.H})
		}
	}
	// The strip's override IS its placement flag (tabStripThemeParked), so the
	// rect-equals-original rule above cannot decide it: a placement that happens to sit
	// on the seed's coordinates would be dropped, and a strip whose seed the user never
	// touched would be "placed" the moment any other widget moved. Restore the flag the
	// snapshot recorded instead. Only meaningful when the key was SYNTHESIZED — a theme
	// that declares it is parked unconditionally.
	if r, present := a.skin.themeRects[themeTabBarKey]; present && a.tabBarSeeded {
		if snap.parked {
			a.d.Prefs.SetThemeRectOverride(themeName, themeTabBarKey, [4]int{r.X, r.Y, r.W, r.H})
		} else {
			a.d.Prefs.ClearThemeRectOverride(themeName, themeTabBarKey)
		}
	}
	a.invalidateThemeCanvases()
}

// startLayoutEdit arms the editor (UI... panel; themed layout only).
// Open modals close — they'd be fenced shut and the editor overlay only
// draws when the themed path runs to its end.
func (a *App) startLayoutEdit() {
	a.layoutEdit = true
	// A1 Phase 1: the compact toolbox (grip + per-piece hide/show panel) now SURVIVES
	// into the themed editor too — previously the themed editor had NO hide/show list
	// at all. Leave toolboxPinned/toolboxPieces as the user set them; they draw
	// post-courtroom with the fence released.
	a.closeEditorBlockingOverlays()
	a.editTgt = noTarget()
	a.editDrag = 0
	a.editEdges = 0
	a.layoutSnap = true        // tidy placement by default; toggle off in the editor
	a.layoutProfileCursor = -1 // no saved profile applied via the banner chip yet this edit
	// layoutMagnetOff is NOT reset here (see startClassicEdit): the sibling magnet
	// applies in normal play too, so its zero value (magnet on) must persist.
	a.editUndo, a.editRedo = nil, nil
}

// closeEditorBlockingOverlays shuts everything that would either strand a layout
// editor or float, inert, over it. Shared by BOTH editors so their arm paths cannot
// drift from each other or from the modal table.
func (a *App) closeEditorBlockingOverlays() {
	a.closeCourtroomModals()
	a.evid.showEvid, a.courtxtra.showModcall, a.pairbox.showPair = false, false, false
	a.modUI.showModDash, a.banBox.kind, a.modUI.showCMPanel = false, 0, false
	a.debugPanel.open, a.lookUI.showFxPicker = false, false
	a.closeMenuBar()
	a.ctx.focusID = ""
}

// stopLayoutEdit disarms and releases the input fence.
func (a *App) stopLayoutEdit() {
	a.layoutEdit = false
	a.editTgt = noTarget()
	a.editDrag = 0
	a.editEdges = 0
	a.ctx.modalOn = false
}

// openLayoutEditor launches the live layout editor from a menu entry (the discoverable front door).
func (a *App) openLayoutEditor() {
	// start{Layout,Classic}Edit close the pinned toolbox panel themselves (A1).
	if a.theaterOn {
		a.setTheater(false)
	}
	if a.skin.themeLay.valid && a.d.Prefs.Load().ThemeLayoutOn {
		a.startLayoutEdit()
		return
	}
	a.startClassicEdit()
}

// layoutEditFence claims the pointer for the editor BEFORE the themed
// widgets draw (they see hovering()==false everywhere and stay inert).
func (a *App) layoutEditFence() {
	if a.layoutEdit {
		a.ctx.modalOn = true // hovering() blanks everywhere; the editor uses raw pointIn
	}
}

// pointIn is the editor's raw hit test (hovering() is fenced on purpose).
func pointIn(x, y int32, r sdl.Rect) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// drawLayoutEditor paints the overlay and owns every interaction. Called
// LAST from the themed courtroom draw, with its layout cache.
func (a *App) drawLayoutEditor(w, h int32, lay *themeLayoutCache) {
	c := a.ctx
	themeName, _ := a.d.Prefs.Load().Theme()
	if themeName == "" || lay.scaleX <= 0 || lay.scaleY <= 0 {
		a.stopLayoutEdit()
		return
	}

	// Banner + chrome (raw-hit buttons — the fence blocks kit ones). The nudge sits
	// straight after "drag = move" because it is the same verb done precisely, and
	// because the line clips from the right on a narrow window (below) — the tail is
	// what disappears first, so the newest entry must not live there.
	banner := "LAYOUT EDIT — drag = move, arrows = nudge 1 px (Ctrl = grid), corner grip = resize, Tab = cycle, R = rotate, right-click = reset, Ctrl+Z/Y = undo, Esc = exit"
	a.toolbox.noteEditorBanner() // the top band is ours this frame, so the menu bar stands down (menubar.go)
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: layoutBannerH}, sdl.Color{R: 0, G: 0, B: 0, A: 210})
	doneBtn := sdl.Rect{X: w - 70 - pad, Y: 2, W: 70, H: 22}
	resetBtn := sdl.Rect{X: doneBtn.X - 96, Y: 2, W: 90, H: 22}
	snapBtn := sdl.Rect{X: resetBtn.X - 106, Y: 2, W: 100, H: 22}
	// Phase 3 chips beside Snap: the persistent Magnet toggle and the saved-profile
	// cycler (applyProfile). Mirror the classic editor banner exactly.
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
	// The help line clips before the LEFTMOST chip actually drawn (the profile chip when
	// one exists, else magnet) — the classic banner has always done this, and this one
	// has to now that it carries the nudge as well: unclipped, the text ran straight
	// under the Snap / Reset all / Done row on a small window.
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

	// Undo / redo (Ctrl+Z / Ctrl+Y) fire from editorUndoChord (handleHotkeys):
	// Ctrl chords ride c.hotkey, never c.keyPressed, so an in-draw keyPressed
	// check here was dead code — the chord had already been routed away.

	if c.escPressed || (c.clicked && pointIn(c.mouseX, c.mouseY, doneBtn)) {
		a.stopLayoutEdit()
		return
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, resetBtn) {
		a.pushLayoutUndo()
		a.d.Prefs.ClearThemeRectOverride(themeName, "")
		// A4: "Reset all" wipes the tilt overrides too — the classic editor's
		// Reset-all clears classicRot the same way, and a piece that snapped
		// home but stayed rotated would contradict the reset message below.
		a.d.Prefs.ClearThemeRectRotation(themeName, "")
		for k, r := range a.skin.themeRectsOrig {
			a.skin.themeRects[k] = r
		}
		a.invalidateThemeCanvases()
		a.debug.push("layout edit: all overrides reset for " + themeName)
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
	// The stack of boxes under the cursor, SMALLEST area first (stable). Tab cycles which one is
	// armed for a move, so a big box hidden under a small one is still reachable. The index resets
	// whenever the stack under the cursor changes.
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
	// The selected design key, resolved once through the target's own space check
	// (edittarget.go): an element target — which only W7's editor can produce — is
	// structurally invisible to every arm of this overlay, which is exactly what "W6
	// ships no new UI" has to mean in code.
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

	// R rotates the hovered widget's texture-backed art (A4): coarse 0/90/180/270,
	// Shift+R a fine 15° step, offered only on keys that actually rotate
	// (themedKeyRotatable — a flat-drawn widget reports "n/a"). Non-undoable like
	// the classic anchor/rotation. Consumed so a char keybind on R can't fire.
	if a.editDrag == 0 && hoverKey != "" && c.keyPressed == sdl.K_r {
		a.cycleThemeRotation(themeName, hoverKey, sdl.GetModState()&sdl.KMOD_SHIFT != 0)
		c.keyPressed = 0
	}

	// The compact toolbox (bottom-right grip → Theater / Edit / Hide-UI) draws in the
	// themed editor too now (post-courtroom, app.go), so a press over its strip or the
	// open pieces panel must suppress a widget move/reset — else the click would also
	// grab whatever themed box sits under the bottom-right corner (A1 Phase 1). The
	// classic editor guards the same way via editOverToolbox.
	overToolbox := a.editOverToolbox(w, h)

	// Begin a drag on press. RESIZE takes priority and reaches the LARGEST box whose grip is
	// under the cursor — so a big box's grip can't be blocked by a small box sitting on its corner.
	// Otherwise MOVE the armed box (hoverKey, Tab-cyclable).
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
		if orig, ok := a.skin.themeRectsOrig[hoverKey]; ok {
			a.pushLayoutUndo()
			a.skin.themeRects[hoverKey] = orig
			a.d.Prefs.ClearThemeRectOverride(themeName, hoverKey)
			// A4: a per-piece reset clears the piece's tilt with its rect,
			// mirroring clearClassicSlot's classicRot delete.
			a.d.Prefs.ClearThemeRectRotation(themeName, hoverKey)
			a.invalidateThemeCanvases()
		}
	}

	// Live drag: a theme's own widgets take screen deltas mapped back to design space
	// through the layout scale; the cache invalidates per move (a ~40-rect rebuild).
	// The server-tab strip takes the RAW cursor delta instead, because its drag is
	// tracked in SCREEN space end to end: the base is its painted box (editBaseFor),
	// the delta is the cursor's own, the snap/magnet/clamp below all work on painted
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
		// Shift = fully pixel-precise: it bypasses the grid AND the magnet together,
		// exactly like the classic editor (classiclayout.go gates both on one check).
		if a.layoutSnap && !magnetBypassed() { // round to the grid so widgets line up cleanly
			if a.editDrag == 1 {
				// TWO GRIDS, the same split the magnet below uses and for the same
				// reason: a theme's widgets round in DESIGN space, the server-tab strip
				// rounds so its PAINTED box lands on the window's grid (see
				// snapTabStripScreen). Both steps of a snapped strip drag therefore run
				// in the space its base, its delta and its clamp already live in.
				if screenDrag {
					r = snapTabStripScreen(r, lay)
				} else {
					r.X = snapDesign(r.X)
					r.Y = snapDesign(r.Y)
				}
			} else {
				r = snapDesignResize(r, a.editEdges)
			}
			// Piece-to-piece magnet (M3): grid first (above), then snap the dragged
			// rect's edges/centre flush to the OTHER widgets and to the extent's
			// edges/centre. Shift already bypassed the whole snap block above.
			switch court, ok := a.skin.themeRectsOrig["courtroom"]; {
			case screenDrag:
				a.themeAlignScratch = a.themeAlignScratch[:0]
				for k, sr := range lay.r {
					if k == editKey || !themeKeyEditable(k) {
						continue
					}
					a.themeAlignScratch = append(a.themeAlignScratch, sr)
				}
				// The painted size, never r.W/r.H — those two slots are inert for this
				// key (applyRectOverrides) and a magnet fed the seed's width would snap
				// the wrong edge flush.
				dr := sdl.Rect{X: int32(r.X) + lay.offX, Y: int32(r.Y) + lay.offY, W: lay.tabStripW, H: tabBarH}
				dr, a.alignGuides = alignRect(dr, a.themeAlignScratch, w, h, true, 0, a.alignGuides)
				r.X, r.Y = int(dr.X-lay.offX), int(dr.Y-lay.offY)
			case ok:
				a.themeAlignScratch = a.themeAlignScratch[:0]
				for k, tr := range a.skin.themeRects {
					if k == editKey || !themeKeyEditable(k) {
						continue
					}
					a.themeAlignScratch = append(a.themeAlignScratch, a.magnetSiblingRect(k, tr, lay))
				}
				dr := sdl.Rect{X: int32(r.X), Y: int32(r.Y), W: int32(r.W), H: int32(r.H)}
				// EVERY HANDLE FEEDS THE MAGNET (design §W6). This mask used to be the
				// constant edgeR|edgeB, which was true while the bottom-right corner was
				// the only grip and became a silent hole the moment it stopped being: a
				// left-edge drag would have rounded to the grid and then aligned to
				// nothing, so six of the eight handles could not be snapped flush to
				// anything. It is the gripped edges now, and alignRect already knows how
				// to move each of the four (growL/growR/growT/growB).
				var edges uint8
				if a.editDrag != 1 {
					edges = a.editEdges
				}
				dr, a.alignGuides = alignRect(dr, a.themeAlignScratch, int32(court.W), int32(court.H), a.editDrag == 1, edges, a.alignGuides)
				r.X, r.Y, r.W, r.H = int(dr.X), int(dr.Y), int(dr.W), int(dr.H)
			}
		}
		// Keep it on the stage (the engine's clamp would rescue it, but
		// editing should feel solid, not rubber-bandy).
		if screenDrag {
			r = clampTabStripScreen(r, lay, w, h)
		} else if court, ok := a.skin.themeRectsOrig["courtroom"]; ok {
			r = clampDesignRectToCanvas(r, court) // shared with the arrow-key nudge (layoutnudge.go)
		}
		a.skin.themeRects[editKey] = r
		a.invalidateThemeCanvases()
	}
	// Release persists the edit.
	if a.editDrag != 0 && !c.mouseDown {
		if editKey != "" {
			r := a.skin.themeRects[editKey]
			if r == a.editBase { // a click with no move: discard the begin snapshot
				if n := len(a.editUndo); n > 0 {
					// The tab strip's drag base is the preimage of its PAINTED box, not
					// its stored rect (editBaseFor), so even a drag that went nowhere
					// left a different value in themeRects. Put the pre-press one back,
					// or a plain click would leave a phantom edit behind for the magnet's
					// sibling list and the next reset to trip over.
					if editKey == themeTabBarKey {
						if pre, ok := a.editUndo[n-1].rects[editKey]; ok {
							a.skin.themeRects[editKey] = pre
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
		// The release is a MUTATION of the layout, so it invalidates like every other
		// one. Two of its effects are invisible to the cache's other key fields: the
		// tab strip's in-flight drag arm drops (tabStripThemeParked goes back to what
		// the persisted override says) and a discarded no-op drag restores the stored
		// rect. tabStripParked is in the key now, so this is the belt beside that
		// braces — it makes the heal IMMEDIATE instead of waiting for whatever probes
		// the key next.
		a.invalidateThemeCanvases()
	}

	// Overlay: every editable rect outlined + named; selection pops.
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
		// A move-only key paints no grip at all, exactly like the classic editor's
		// drawSlotHandles: an affordance that does nothing is worse than none.
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
	// Piece-to-piece magnet guides (M3): the positions the drag just snapped flush to,
	// drawn in the space the magnet that produced them ran in. A design-space drag's
	// guides map through the layout scale (the classic editor draws raw px; the themed
	// path MUST scale or the hairlines land wrong); the server-tab strip's magnet runs
	// on painted pixels, so its guides ARE screen px and scaling them again would put
	// the hairline nowhere near the edge the strip snapped to.
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
		r := a.skin.themeRects[editKey]
		// The strip is stored in canvas-relative CLIENT px, not design px
		// (tabStripCacheRect), so the readout must not claim otherwise — the two agree
		// only at scale 1.
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
	// Rot readout (A4): a passive chip beside the banner's Snap chip, shown only
	// when the hovered/selected widget carries a nonzero angle. Painted at the end
	// (after hoverKey resolves) so it reflects the piece under the cursor; the
	// banner geometry (snapBtn) is still in scope. R rotates, Shift+R fine-steps.
	rotKey := hoverKey
	if editKey != "" {
		rotKey = editKey
	}
	if rotKey != "" && a.skin.themeLay.ang != nil {
		if label := rotationChipLabel(a.skin.themeLay.ang[rotKey]); label != "" {
			// Sits left of the Phase-3 chips (profile when present, else magnet) so it
			// never overpaints them.
			rotRightX := magnetBtn.X
			if a.hasLayoutProfiles() {
				rotRightX = profileBtn.X
			}
			a.rawChip(sdl.Rect{X: rotRightX - 84, Y: 2, W: 78, H: 22}, label)
		}
	}
}

// editBaseFor is the rect a drag starts from, in whatever space that key's drag is
// tracked in.
func (a *App) editBaseFor(key string, lay *themeLayoutCache) theme.Rect {
	if key != themeTabBarKey {
		return a.skin.themeRects[key]
	}
	box, ok := lay.rect(key)
	if !ok {
		return a.skin.themeRects[key]
	}
	return theme.Rect{
		X: int(box.X - lay.offX),
		Y: int(box.Y - lay.offY),
		W: int(box.W),
		H: int(box.H),
	}
}

// magnetSiblingRect is one entry of the DESIGN-space magnet's sibling list — the box
// some OTHER key's snapped drag may snap flush to.
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

// clampTabStripScreen keeps the tab strip's stored origin inside the window.
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

// rawChip draws a button-look chip the fence can't block (raw hit test).
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

// applyRectOverrides lays the persisted edits for the active theme over
// a fresh design map (pollThemeApply calls this after every theme load).
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
