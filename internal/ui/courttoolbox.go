package ui

import (
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// Editor toolbox (#27 → A1 Phase 1): the ONE show/hide + editor surface is the compact
// bottom-right toolbox (grip → Theater / Edit / Hide-UI chips) plus its pinned
// per-piece hide/show panel (drawToolboxPieces). Both draw in normal play AND in the
// layout editor now — the old full-width top-band chip strip (drawClassicToolbox) that
// only existed in the classic editor, and the separate "Hide UI pieces" dialog
// (drawUICfgPanel), are both RETIRED: per-piece hiding lives entirely in the pinned
// panel, which is cleaner and reachable in and out of the editor, in both layouts.

// hideableSlot maps a hideable element id → the layout slot the editor positions it
// through. The map records which hideable elements own a movable layout slot (vs. the
// penalty bars, timers, testimony, judge, extras, the shouts/knobs inside the controls
// block, and the tabs — which the tab tray tears — that have none). For the control
// buttons the hideable id IS the slot key.
var hideableSlot = map[string]string{
	panelEmotes:       slotEmotes,
	panelOOC:          slotOOC,
	panelLog:          slotRightCol,
	"ctrl.character":  "ctrl.character",
	"ctrl.wardrobe":   "ctrl.wardrobe",
	"ctrl.restyle":    "ctrl.restyle",
	"ctrl.background": "ctrl.background",
	"ctrl.evidence":   "ctrl.evidence",
	"ctrl.mods":       "ctrl.mods",
	"ctrl.settings":   "ctrl.settings",
	"ctrl.hotkeys":    "ctrl.hotkeys",
	"ctrl.about":      "ctrl.about",
	"ctrl.login":      "ctrl.login",
}

// hideableForSlot returns the hideable element id a slot key positions, or "" when the
// slot has no hideable mapping (the viewport, the IC bar, the controls block, …). The
// inverse lookup over hideableSlot; pinned by TestHideableForSlot.
func hideableForSlot(slotKey string) string {
	for id, sk := range hideableSlot {
		if sk == slotKey {
			return id
		}
	}
	return ""
}

// --- compact hover toolbox (#27) ---------------------------------------------------
//
// Show/hide config was previously split three ways (the toolbar's UI…/Edit-Layout
// buttons, the Extras box, and the Hide-UI dialog which ALONE hosted Theater +
// the themed Edit-layout entry). This consolidates those three entry points into
// one toolbox, collapsed to small hover-revealed chips so it stays out of the way
// during normal play.
//
// So this is a compact, collapsed-by-default strip pinned to the bottom-right
// corner, shown in NORMAL play (both the classic and themed courtroom). Collapsed
// it's a slim, semi-transparent grip (a drawn hamburger primitive — no glyph
// font dependency) with a visually negligible footprint. On hover it expands
// LEFT into a row of small icon chips with tooltip labels: Pin, Theater, Edit
// layout, and Hide UI (the last opens the pinned per-piece panel below — the
// drawUICfgPanel dialog is retired, so this toolbox is now the ONLY home for
// Theater, the themed Edit-layout entry, and per-piece show/hide).
//
// Perf (it draws per-frame in normal play, under the ui AllocsPerRun gates): the
// chip set is a fixed package-level slice with CONSTANT labels, so nothing is
// allocated per frame and the TextWidth probes hit the width cache after warm-up.
// The reveal is a pure hover state (no persistent animation, no NoteAnimating
// keepalive) — the hover transition already forces the redraw via the input path,
// so it can't wake the render loop at full rate while idle.

const (
	// compactGripW/H size the collapsed grip (the slim edge tab). Deliberately
	// small so it barely touches the scene during normal play. Its height matches
	// a chip so the expanded strip aligns cleanly to the same baseline.
	compactGripW = int32(18)
	compactGripH = int32(22)
	// compactChipH / compactChipPad size an expanded icon chip and its inner text pad.
	compactChipH   = int32(22)
	compactChipPad = int32(8)
	// compactToolboxMargin insets the strip from the window's bottom-right corner.
	compactToolboxMargin = int32(4)
	// compactHoverPad grows the collapsed grip's hover target a little so the strip
	// doesn't collapse the instant the cursor grazes a chip's edge.
	compactHoverPad = int32(6)
	// toolboxIconPad insets a drawn chip icon inside its rect so the vector
	// glyph never touches the chip border (A1).
	toolboxIconPad = int32(5)
	// toolboxRingAlpha is the accent-ring alpha on the collapsed grip while the
	// user hasn't yet expanded the toolbox (!ToolboxSeen) — a faint, static
	// discoverability hint (A1). Static (no pulse) so it never wakes the render
	// loop while idle.
	toolboxRingAlpha = uint8(90)
	// toolboxPiecesRowPitch is the per-piece checkbox row pitch in the pinned
	// panel (matches the retired dialog's cfgRow).
	toolboxPiecesRowPitch = int32(26)
	// toolboxPiecesMaxH clamps the pinned per-piece scroll panel's height so it
	// never runs off a short window; the body scrolls inside it.
	toolboxPiecesMaxH = int32(360)
	// toolboxPiecesW is the fixed width of the pinned per-piece panel.
	toolboxPiecesW = int32(300)
	// toolboxPiecesCols is the column count of the control/roster button grids
	// inside the pinned panel (narrower than the old dialog's 3-wide grid).
	toolboxPiecesCols = int32(2)
)

// iconKind selects one of the hand-drawn vector glyphs drawToolIcon composes
// from axis-aligned Fill/Border rects (A1). There is NO icon primitive in the
// Ctx kit and no glyph font we can rely on (tofu risk), so every chip icon is
// built from 2–6 rectangles — the same "draw it, don't font it" precedent the
// collapsed hamburger grip set. Each kind is a couple of stack-local rects fed
// to c.Fill, so drawing one allocates nothing (Fill copies into c.cgoRect).
type iconKind int

const (
	iconTheater iconKind = iota // a wide stage bar over two short legs
	iconEdit                    // a diagonal-stepped 3-rect pencil
	iconEyeOff                  // a horizontal lens bar with a slash
	iconPin                     // a head rect over a vertical stem
	iconGrid                    // a 2×2 block of small rects
)

// drawToolIcon paints the vector glyph for k, centred inside r, in col. Pure
// c.Fill rectangles (no closures, no font, no assets) so it stays alloc-free —
// pinned by TestToolIconAllocFree. Geometry is derived from r each call with
// integer math; the small insets/thicknesses use toolboxIconPad so the icon
// never touches the chip border.
func drawToolIcon(c *Ctx, k iconKind, r sdl.Rect, col sdl.Color) {
	// The drawable box: the chip rect inset by toolboxIconPad on every side.
	ix, iy := r.X+toolboxIconPad, r.Y+toolboxIconPad
	iw, ih := r.W-2*toolboxIconPad, r.H-2*toolboxIconPad
	if iw <= 0 || ih <= 0 {
		return
	}
	switch k {
	case iconTheater:
		// A wide top bar (the stage/marquee) resting on two short legs.
		bar := sdl.Rect{X: ix, Y: iy, W: iw, H: ih / 3}
		c.Fill(bar, col)
		legW := iw / 4
		legY := iy + ih/3
		legH := ih - ih/3
		c.Fill(sdl.Rect{X: ix, Y: legY, W: legW, H: legH}, col)
		c.Fill(sdl.Rect{X: ix + iw - legW, Y: legY, W: legW, H: legH}, col)
	case iconEdit:
		// A pencil drawn as three stepped squares along the diagonal, plus a
		// small tip square at the bottom-left (the nib).
		step := iw / 4
		if step < 2 {
			step = 2
		}
		th := step
		// Body: three squares climbing from bottom-left to top-right.
		for i := int32(0); i < 3; i++ {
			bx := ix + i*step
			by := iy + ih - th - i*step
			if by < iy {
				by = iy
			}
			c.Fill(sdl.Rect{X: bx, Y: by, W: th, H: th}, col)
		}
		// Nib: a tiny square at the bottom-left corner.
		c.Fill(sdl.Rect{X: ix, Y: iy + ih - th/2, W: th, H: th / 2}, col)
	case iconEyeOff:
		// A horizontal lens bar (the "eye") with a diagonal-ish slash rendered
		// as a thin bar crossing it (hidden = eye struck through).
		lensH := ih / 3
		lensY := iy + (ih-lensH)/2
		c.Fill(sdl.Rect{X: ix, Y: lensY, W: iw, H: lensH}, col)
		// Slash: a thin full-width bar tilted by drawing two offset segments.
		slashH := ih / 5
		if slashH < 2 {
			slashH = 2
		}
		c.Fill(sdl.Rect{X: ix, Y: iy + ih - slashH, W: iw / 2, H: slashH}, col)
		c.Fill(sdl.Rect{X: ix + iw/2, Y: iy, W: iw / 2, H: slashH}, col)
	case iconPin:
		// A pushpin: a wide head rect over a narrow vertical stem.
		headH := ih / 3
		c.Fill(sdl.Rect{X: ix, Y: iy, W: iw, H: headH}, col)
		stemW := iw / 4
		if stemW < 2 {
			stemW = 2
		}
		c.Fill(sdl.Rect{X: ix + (iw-stemW)/2, Y: iy + headH, W: stemW, H: ih - headH}, col)
	case iconGrid:
		// A 2×2 block of small squares (the per-piece list).
		gap := iw / 8
		if gap < 1 {
			gap = 1
		}
		cw := (iw - gap) / 2
		ch := (ih - gap) / 2
		c.Fill(sdl.Rect{X: ix, Y: iy, W: cw, H: ch}, col)
		c.Fill(sdl.Rect{X: ix + cw + gap, Y: iy, W: cw, H: ch}, col)
		c.Fill(sdl.Rect{X: ix, Y: iy + ch + gap, W: cw, H: ch}, col)
		c.Fill(sdl.Rect{X: ix + cw + gap, Y: iy + ch + gap, W: cw, H: ch}, col)
	}
}

// compactChip is one hover-revealed icon chip: a drawn vector icon, a tooltip
// carrying the full word (the accessible name — the chip itself is icon-only),
// and the action it runs. run is a METHOD VALUE — the slice is built once
// (compactToolboxChips), so no closure is allocated per frame and the whole set
// stays inside the zero-alloc courtroom gate.
type compactChip struct {
	icon iconKind
	tip  string
	run  func(a *App)
}

// compactToolboxChips is the fixed chip set, right-to-left from the grip (A1):
// Pin (latch the flyout open), Theater, Edit layout, Hide-UI (the per-piece
// panel). Each chip draws a vector icon and carries a Tooltip with the full
// word. The slice is package-level with method values, so it never re-allocates.
var compactToolboxChips = []compactChip{
	{iconPin, "Pin the toolbox open (press again or the grip to unpin)", (*App).compactTogglePin},
	{iconTheater, "Theater mode — stage only, Esc exits", (*App).compactTheater},
	{iconEdit, "Edit layout — drag & resize every box", (*App).compactEditLayout},
	{iconEyeOff, "Hide UI pieces — per-piece show/hide list", (*App).compactHideUI},
}

func (a *App) compactTheater()    { a.setTheater(!a.theaterOn) }
func (a *App) compactEditLayout() { a.openLayoutEditor() }

// compactTogglePin latches the flyout open / closed. Unpinning also closes the
// per-piece panel so a later hover doesn't silently re-reveal it.
func (a *App) compactTogglePin() {
	a.toolboxPinned = !a.toolboxPinned
	if !a.toolboxPinned {
		a.toolboxPieces = false
	}
}

// compactHideUI opens (or toggles) the in-flyout per-piece hide/show panel —
// the replacement for the retired drawUICfgPanel dialog. Opening it implies
// pinning: the panel is gated on toolboxPinned, so without this a click from a
// hover-only flyout would set a flag that shows nothing.
func (a *App) compactHideUI() {
	a.toolboxPieces = !a.toolboxPieces
	if a.toolboxPieces {
		a.toolboxPinned = true
	}
}

// drawCompactToolbox paints the collapsed grip and, while hovered OR pinned, the
// expanded icon-chip row. In normal play it draws in-pass (classic + themed
// courtroom); while a layout editor is armed it draws POST-courtroom instead
// (app.go, fence released) and force-expands so the editor's fence can't blank its
// grip/chips (A1 Phase 1). NOT drawn in theater mode or when hidden via panelToolbox.
// A1: the grip is a press-to-pin latch (click toggles toolboxPinned), the chips draw
// vector icons, and while the user has never expanded it the collapsed grip wears a
// faint accent discoverability ring.
func (a *App) drawCompactToolbox(w, h int32) {
	if a.panelHidden(panelToolbox) {
		return
	}
	c := a.ctx
	// Expanded footprint (grip + chips to its left) so the strip stays open while
	// the cursor is anywhere over a chip, not just the grip. Factored out
	// (compactToolboxStripRect) so the editor's over-toolbox suppression rect and
	// this draw agree — the click-leak class a fence rect that disagrees with the
	// draw rect invites (mirrors toolboxPiecesRect's reason for existing). The strip
	// routes through the slotToolbox override (A1 Phase 2), so the grip is DERIVED
	// from the strip's right end rather than computed independently — a moved toolbox
	// carries its grip with it and the two can never drift apart.
	strip := a.compactToolboxStripRect(w, h)
	// The collapsed grip: the strip's right end, slim + semi-transparent.
	grip := compactToolboxGripRect(strip)
	hoverArea := sdl.Rect{X: grip.X - compactHoverPad, Y: grip.Y - compactHoverPad,
		W: grip.W + compactHoverPad, H: grip.H + 2*compactHoverPad}
	// While a layout editor is armed the toolbox draws (post-courtroom, fence
	// released) as a stable target — it force-expands so its grip/chips are always
	// reachable to reach Edit/Hide-UI/Theater without hunting for the hover sweet
	// spot over the busy editor overlay (A1 Phase 1).
	expanded := a.classicEdit || a.layoutEdit || a.toolboxPinned || c.hovering(hoverArea) || c.hovering(strip)

	if expanded && !a.d.Prefs.ToolboxSeenOn() {
		// First expand (hover or pin) latches the discoverability flag off so the
		// ring stops. Idempotent setter → no per-frame markDirty once seen.
		a.d.Prefs.SetToolboxSeen(true)
	}

	if !expanded {
		// Collapsed: a slim translucent tab with a hamburger primitive (drawn, not
		// a glyph) so it renders on any font and stays unobtrusive.
		c.Fill(grip, sdl.Color{R: 0, G: 0, B: 0, A: 120})
		barW := grip.W - 8
		for i := int32(0); i < 3; i++ {
			bar := sdl.Rect{X: grip.X + 4, Y: grip.Y + 5 + i*5, W: barW, H: 2}
			c.Fill(bar, sdl.Color{R: 200, G: 200, B: 210, A: 180})
		}
		if !a.d.Prefs.ToolboxSeenOn() {
			// Faint STATIC accent ring while never-expanded — a quiet "there's
			// something here" hint. Static (not a pulse) so it never registers an
			// animating frame and can't wake the render loop while idle. Constant
			// geometry + a package-level colour const ⇒ alloc-free (gated).
			ring := sdl.Rect{X: grip.X - 1, Y: grip.Y - 1, W: grip.W + 2, H: grip.H + 2}
			c.Border(ring, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: toolboxRingAlpha})
		}
		c.Tooltip(hoverArea, "Toolbox — hover or click for Theater, Edit layout & Hide UI")
		return
	}

	// Expanded: chips laid out right-to-left from the grip, each an icon with a
	// tooltip carrying the full word. The strip stays a sharp frame (the grip
	// square overlaps its right end, so a rounded strip would show corner nubs);
	// the individual chips below are self-contained and DO follow the shape.
	c.Fill(strip, sdl.Color{R: 0, G: 0, B: 0, A: 205})
	c.Border(strip, ColAccent)
	x := grip.X // chips fan LEFT from the grip's left edge (grip is derived from the strip)
	// The grip stays as the right-hand anchor — and now a pin toggle. Filled accent
	// (or a brighter nub while pinned) so it's clear where the strip folds back to
	// and whether it's latched.
	gripCol := ColPanelHi
	if a.toolboxPinned {
		gripCol = ColAccent
	}
	c.Fill(grip, gripCol)
	for i := int32(0); i < 3; i++ {
		bar := sdl.Rect{X: grip.X + 4, Y: grip.Y + 5 + i*5, W: grip.W - 8, H: 2}
		c.Fill(bar, ColText)
	}
	// Clicking the grip toggles the pin latch (the un-strand affordance: it also
	// closes/opens without needing a chip) — EXCEPT while a layout editor is armed,
	// where the grip is the toolbox's DRAG handle (Phase 2): the same press already
	// grabbed the toolbox for a move in the editor pass, so a pin toggle here would
	// double-fire on one click.
	editing := a.classicEdit || a.layoutEdit
	if !editing && c.hovering(grip) && c.clicked {
		a.compactTogglePin()
	}
	gripTip := "Toolbox — click to pin/unpin"
	if editing {
		gripTip = "Toolbox — drag this grip to move it"
	}
	c.Tooltip(grip, gripTip)
	for _, ch := range compactToolboxChips {
		cw := compactChipH // square icon chip
		x -= cw + 4
		chip := sdl.Rect{X: x, Y: grip.Y + (compactGripH-compactChipH)/2, W: cw, H: compactChipH}
		hover := c.hovering(chip)
		bg := ColPanel
		if hover {
			bg = ColPanelHi
		}
		// The Pin chip shows its latched state; the Hide-UI chip shows whether the
		// per-piece panel is open, so both read as toggles.
		if (ch.icon == iconPin && a.toolboxPinned) || (ch.icon == iconEyeOff && a.toolboxPieces) {
			bg = ColAccent
		}
		// Chip background follows the chrome SHAPE (A5); the vector icon glyph
		// inside stays sharp (same principle as a shaped button keeping its text).
		c.FillShaped(chip, bg)
		c.borderShaped(chip, ColAccent)
		iconCol := ColText
		if !hover {
			iconCol = ColTextDim
		}
		drawToolIcon(c, ch.icon, chip, iconCol)
		if hover && c.clicked {
			ch.run(a)
		}
		c.Tooltip(chip, ch.tip)
	}
	// The pinned per-piece panel is NOT drawn here — it draws post-courtroom in
	// app.go (drawToolboxPieces), where the pointer fence is lifted so its widgets
	// get real input. Drawing it there also keeps it reachable when the grip itself
	// is hidden via panelHidden(panelToolbox): the hotkey un-strand path.
}

// compactToolboxDefaultWidth is the expanded strip's intrinsic width: the grip plus
// one square chip (compactChipH wide, +4 px spacing) per toolbox chip. Named so both
// the default-rect geometry and any width reasoning share the one derivation.
func compactToolboxDefaultWidth() int32 {
	stripW := compactGripW
	for range compactToolboxChips {
		stripW += compactChipH + 4
	}
	return stripW
}

// compactToolboxDefaultRect is the strip's HISTORICAL position: pinned to the
// bottom-right corner, inset by compactToolboxMargin. This is the slotToolbox
// DEFAULT (A1 Phase 2) — an untouched install (no override) draws here, pixel-
// identical to before movability landed. Pure geometry, alloc-free.
func (a *App) compactToolboxDefaultRect(w, h int32) sdl.Rect {
	stripW := compactToolboxDefaultWidth()
	gripY := h - compactGripH - compactToolboxMargin
	return sdl.Rect{X: w - stripW - compactToolboxMargin, Y: gripY, W: stripW, H: compactGripH}
}

// compactToolboxGripRect is the grip sub-rect at the strip's right end — the
// hamburger handle. It's the toolbox's DRAG handle in the editor (like a floatWin's
// title bar): pressing it grabs the toolbox to move it, while the chips to its left
// stay live buttons. Derived from the strip so a moved toolbox carries its grip.
func compactToolboxGripRect(strip sdl.Rect) sdl.Rect {
	return sdl.Rect{X: strip.X + strip.W - compactGripW, Y: strip.Y, W: compactGripW, H: compactGripH}
}

// compactToolboxStripRect is the expanded strip's footprint (grip + the icon chips
// to its left). Factored out (A1 Phase 1) so the editor's over-toolbox suppression
// rect matches the drawn strip exactly — the same draw-vs-fence agreement
// toolboxPiecesRect keeps. Chips are square (icon-only), so each is as wide as it is
// tall. A1 Phase 2: routed through slotRect(slotToolbox, …) so an Edit-Layout
// override relocates the whole toolbox; an absent override returns the bottom-right
// default unchanged. slotRect reads a.classicOv lock-free and only touches the slot
// registry while editing, so this stays alloc-free on the settled courtroom gate
// (TestDrawCourtroomZeroAlloc).
func (a *App) compactToolboxStripRect(w, h int32) sdl.Rect {
	def := a.compactToolboxDefaultRect(w, h)
	// Themed twin: a theme whose design INI ships "asyncao_toolbox" positions the
	// strip at that (themed-editor-draggable) rect, taking precedence over the
	// classic slot — exactly as the FX button's asyncao_ic_fx rect wins over its
	// classic slotICFx. Keep the intrinsic (move-only) W/H; clamp on-window.
	if a.toolboxThemeRectOn {
		cur := def
		cur.X = clampI32(a.toolboxThemeRect.X, compactToolboxMargin, w-def.W-compactToolboxMargin)
		cur.Y = clampI32(a.toolboxThemeRect.Y, compactToolboxMargin, h-def.H-compactToolboxMargin)
		return cur
	}
	cur := def
	if ov, ok := a.classicOv[slotToolbox]; ok {
		cur = a.anchoredRect(slotToolbox, ov, w, h) // fracToRect unless the slot is window-pinned
		// The strip is MOVE-only (slotResizeEdges): its W/H are chip-count-driven,
		// not user-resizable. The override persists position as a window FRACTION,
		// which would scale W/H on a resized window and smear the strip — so keep only
		// the override's X/Y and always restore the intrinsic size. Clamp X/Y so a
		// moved toolbox can't sail off a now-smaller window (ungrabbable).
		cur.W, cur.H = def.W, def.H
		cur.X = clampI32(cur.X, compactToolboxMargin, w-def.W-compactToolboxMargin)
		cur.Y = clampI32(cur.Y, compactToolboxMargin, h-def.H-compactToolboxMargin)
	}
	// Register with the editor while editing (so it hands the toolbox move handles),
	// exactly as slotRect does — but with the NORMALIZED rect, so the editor's drag
	// base and this draw agree on the strip's true W/H. Done inline (not via slotRect)
	// precisely because slotRect can't apply the move-only W/H normalization above.
	if a.classicEdit {
		a.regSlot(slotToolbox, cur, def)
	}
	return cur
}

// editOverToolbox reports whether the cursor sits over the compact toolbox strip
// (or, while open, the pinned per-piece panel) during a layout edit — so the
// classic/themed editors suppress a slot-move/right-click there and the toolbox's
// own grip/chips win the press instead. Replaces drawClassicToolbox's old
// `overToolbox` return: the top-band strip is gone, the toolbox is the bottom-right
// grip both editors now show. Pure hit-test, alloc-free.
func (a *App) editOverToolbox(w, h int32) bool {
	c := a.ctx
	// The CHIPS (strip minus the grip) stay live buttons in the editor (Phase 1:
	// Theater / Edit / Hide-UI), so suppress a slot-move/right-click over them — the
	// chip press wins. The GRIP is deliberately EXCLUDED: it's the toolbox's drag
	// handle (Phase 2), so a press there must reach the editor and grab the toolbox to
	// move it, exactly like a floatWin title bar. When the themed twin is active the
	// toolbox IS an editable themed key (asyncao_toolbox) the themed editor drags as a
	// normal box, so don't suppress the chips region either — let the themed editor own
	// the whole strip. The classic editor drags via the slotToolbox registration
	// (compactToolboxStripRect → regSlot) whenever the press lands on the grip.
	if !a.toolboxThemeRectOn && !a.panelHidden(panelToolbox) {
		strip := a.compactToolboxStripRect(w, h)
		grip := compactToolboxGripRect(strip)
		if pointIn(c.mouseX, c.mouseY, strip) && !pointIn(c.mouseX, c.mouseY, grip) {
			return true // over a chip, not the grip
		}
	}
	// The pinned pieces panel is drawn post-courtroom and takes its own input there,
	// but it overlaps the bottom-right where slots can park — fence a slot-move under
	// it too so an editor press can't grab a box beneath the open panel.
	if a.toolboxPinned && a.toolboxPieces && pointIn(c.mouseX, c.mouseY, a.toolboxPiecesRect(w, h)) {
		return true
	}
	return false
}

// toolboxPiecesRect is the pinned per-piece panel's screen rect — anchored to
// the bottom-right above the toolbox strip. Factored out so boxFencesPointer and
// the draw agree on frame one (the click-leak class the recon flagged: a fence
// rect that disagrees with the draw rect leaks a click through the panel).
func (a *App) toolboxPiecesRect(w, h int32) sdl.Rect {
	panelH := a.toolboxPiecesContentH() + toolboxPiecesHeaderH + toolboxPiecesFooterH
	if panelH > toolboxPiecesMaxH {
		panelH = toolboxPiecesMaxH
	}
	// Clamp to the window (leave the toolbox strip + a small margin below).
	maxH := h - (compactGripH + 2*compactToolboxMargin) - toolboxPiecesTopGap
	if panelH > maxH {
		panelH = maxH
	}
	if panelH < toolboxPiecesHeaderH+toolboxPiecesFooterH+toolboxPiecesRowPitch {
		panelH = toolboxPiecesHeaderH + toolboxPiecesFooterH + toolboxPiecesRowPitch
	}
	pw := toolboxPiecesW
	if pw > w-2*compactToolboxMargin {
		pw = w - 2*compactToolboxMargin
	}
	x := w - pw - compactToolboxMargin
	y := h - compactGripH - compactToolboxMargin - toolboxPiecesTopGap - panelH
	if y < compactToolboxMargin {
		y = compactToolboxMargin
	}
	return sdl.Rect{X: x, Y: y, W: pw, H: panelH}
}

const (
	// toolboxPiecesHeaderH is the fixed title strip at the pieces panel top.
	toolboxPiecesHeaderH = int32(30)
	// toolboxPiecesFooterH is the fixed footer (Close button) at the bottom.
	toolboxPiecesFooterH = int32(34)
	// toolboxPiecesTopGap separates the pieces panel from the toolbox strip.
	toolboxPiecesTopGap = int32(6)
)

// toolboxPiecesContentH is the full scroll-region height of the per-piece panel:
// the chrome list, the control-button grid (new-default toolbar only), and the
// roster grid. Mirrors the retired drawUICfgPanel's contentH arithmetic.
func (a *App) toolboxPiecesContentH() int32 {
	rows := int32(len(hideablePanels))
	if !a.d.Prefs.LegacyDevThemeOn() {
		btnRows := (int32(len(hideableButtons)) + toolboxPiecesCols - 1) / toolboxPiecesCols
		rows += 1 + btnRows // +1 sub-heading row
	}
	rosterRows := (int32(len(hideableRosterButtons)) + toolboxPiecesCols - 1) / toolboxPiecesCols
	rows += 1 + rosterRows // +1 sub-heading row
	return rows*toolboxPiecesRowPitch + 8
}

// setPanelHiddenGuarded is drawToolboxPieces' setPanelHidden with the
// no-strand guard (A6): hiding the SECOND of the two mouse lifelines — the
// toolbox grip (panelToolbox) and the toolbar Settings button
// (ctrlSettingsSlot), each a mouse route back to chrome recovery — is refused
// with a toast explaining why, instead of applied-then-silently-undone. Every
// other toggle passes straight through. Wholesale hidden-set writes (profile
// apply, prefs import/reset) normalize the same invariant in
// seedHiddenFromPrefs instead.
func (a *App) setPanelHiddenGuarded(id string, hide bool) {
	if hide {
		other := ""
		switch id {
		case panelToolbox:
			other = ctrlSettingsSlot
		case ctrlSettingsSlot:
			other = panelToolbox
		}
		if other != "" && a.panelHidden(other) {
			a.warnLine = "Kept: hiding both the toolbox and the Settings button would leave no mouse way back. (Ctrl+F reopens this panel.)"
			a.warnAt = time.Now()
			return
		}
	}
	a.setPanelHidden(id, hide)
}

// drawToolboxPieces paints the pinned per-piece hide/show panel (A1) — the
// replacement for the retired drawUICfgPanel dialog. It reuses the exact same
// registries (hideablePanels / hideableButtons / hideableRosterButtons) and
// setPanelHidden, so every toggle behaves identically. Gated ONLY on
// toolboxPinned && toolboxPieces — NOT on panelHidden(panelToolbox) — so the
// hotkey (hotkeyUIChrome) can open it even when the grip is hidden (the
// un-strand path: a user who hid the toolbox can still reach per-piece hiding).
func (a *App) drawToolboxPieces(w, h int32) {
	if !a.toolboxPinned || !a.toolboxPieces {
		return
	}
	// Agree with the fence: boxFencesPointer early-returns on !extrasSurfaceLive
	// (a blocking popup / dead surface), so the draw must suppress there too, or a
	// blocking modal would show the panel un-fenced and leak a click behind it (the
	// click-leak class). This also hides the panel behind a blocking court popup and
	// lets it reappear when that closes — the same yield the Extras box does.
	if !a.extrasSurfaceLive() {
		return
	}
	// The palette (Ctrl+Space) draws ON TOP of this panel (app.go draw order)
	// but neither is modal — on a narrow window the two overlap, and this
	// panel, drawn (and input-polled) FIRST, would eat clicks under the
	// palette's rows: a Z/input inversion. Stand down while the palette is up
	// (its fence in boxFencesPointer reads the same flag, so draw and fence
	// stay in lockstep — the ddOpen dropdown precedent).
	if a.paletteOpen {
		return
	}
	c := a.ctx
	panel := a.toolboxPiecesRect(w, h)
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+6, "Hide UI pieces", ColText)

	// The control-button grid only applies to the new-default toolbar; the
	// legacy/themed row draws fixed inline buttons that ignore the hidden set, so
	// a chip there would be a dead toggle (mirrors the retired dialog's guard).
	showBtnGrid := !a.d.Prefs.LegacyDevThemeOn()

	body := sdl.Rect{X: panel.X, Y: panel.Y + toolboxPiecesHeaderH,
		W: panel.W, H: panel.H - toolboxPiecesHeaderH - toolboxPiecesFooterH}
	contentH := a.toolboxPiecesContentH()
	needsBar := contentH > body.H
	barReserve := int32(0)
	if needsBar {
		barReserve = scrollBarW + scrollBarGap
	}
	if !c.ctrlHeld {
		a.toolboxPiecesScroll -= c.WheelIn(body) * scrollStepPx
	}
	if needsBar {
		track := sdl.Rect{X: body.X + body.W - scrollBarW - pad, Y: body.Y, W: scrollBarW, H: body.H}
		a.toolboxPiecesScroll = c.VScrollbar("toolboxpieces", track, a.toolboxPiecesScroll, contentH, body.H)
	} else {
		a.toolboxPiecesScroll = 0
	}
	// Clipped, input-aware (pushClip honours hovering) so a label tail can't leak
	// a click past the body edge or over the scrollbar lane.
	clipBody := body
	if needsBar {
		clipBody.W -= barReserve
	}
	clipPrev, clipHad := c.pushClip(clipBody)
	rowW := panel.W - 2*pad - barReserve
	colW := rowW / toolboxPiecesCols
	y := body.Y - a.toolboxPiecesScroll
	for _, p := range hideablePanels {
		hidden := a.panelHidden(p.id)
		if next := c.Checkbox(panel.X+pad, y, "Hide "+p.short, hidden); next != hidden {
			a.setPanelHiddenGuarded(p.id, next)
		}
		y += toolboxPiecesRowPitch
	}
	if showBtnGrid {
		c.Label(panel.X+pad, y+4, "Control buttons (tick to hide):", ColTextDim)
		y += toolboxPiecesRowPitch
		for i, b := range hideableButtons {
			cx := panel.X + pad + int32(i)%toolboxPiecesCols*colW
			cy := y + int32(i)/toolboxPiecesCols*toolboxPiecesRowPitch
			hidden := a.panelHidden(b.id)
			if next := c.Checkbox(cx, cy, b.label, hidden); next != hidden {
				a.setPanelHiddenGuarded(b.id, next)
			}
		}
		y += (int32(len(hideableButtons)) + toolboxPiecesCols - 1) / toolboxPiecesCols * toolboxPiecesRowPitch
	}
	c.Label(panel.X+pad, y+4, "Players-list row actions (tick to hide):", ColTextDim)
	y += toolboxPiecesRowPitch
	for i, b := range hideableRosterButtons {
		cx := panel.X + pad + int32(i)%toolboxPiecesCols*colW
		cy := y + int32(i)/toolboxPiecesCols*toolboxPiecesRowPitch
		hidden := a.panelHidden(b.id)
		if next := c.Checkbox(cx, cy, b.label, hidden); next != hidden {
			a.setPanelHiddenGuarded(b.id, next)
		}
	}
	c.popClip(clipPrev, clipHad)

	// Fixed footer: a Close button (always reachable even when the grip is hidden,
	// so a hotkey-opened panel is never stranded — the un-strand path).
	footerY := panel.Y + panel.H - btnH - 8
	if c.Button(sdl.Rect{X: panel.X + panel.W - 84 - pad, Y: footerY, W: 84, H: btnH}, "Close") {
		a.toolboxPieces = false
	}
}
