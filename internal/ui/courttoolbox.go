package ui

import "github.com/veandco/go-sdl2/sdl"

// Editor toolbox (#27): a single strip in the layout editor listing EVERY hideable UI
// element as a chip — chrome panels (shouts, knobs, emotes, the tabs, penalty bars,
// timers, judge controls, …) and the customizable control buttons. A bright/accent
// chip is SHOWN, a dim chip is HIDDEN; clicking toggles it. This pulls the old "Hide
// UI pieces" checkbox dialog into the editor itself, so arranging and hiding pieces
// live in one place (the playtest ask: "one toolbox, not a separate scuffed menu").
//
// Slicing (advisor): this is Slice 1 — the unified toggle toolbox. Drag-a-chip-onto-
// the-stage (show + place) and drag-a-piece-off (hide) come next, reusing the editor's
// existing overTray drag-release seam. The old separate "Hide UI pieces" dialog
// (drawUICfgPanel) is RETIRED (A1): its per-piece checkbox list now lives in the
// pinned pieces panel (drawToolboxPieces), and Theater + the themed Edit-layout entry
// moved onto the compact bottom-right toolbox chips (which both layouts share).

const (
	// toolboxChipH / toolboxRowPitch size the wrapped chip grid.
	toolboxChipH    = 22
	toolboxRowPitch = toolboxChipH + 6
	// toolboxTop is the strip's top, just under the tear-off tab tray (which sits at
	// classicBannerH+8 with a 40 px strip).
	toolboxTop = int32(classicBannerH + 8 + 40 + 4)
	// toolboxDragThresh is the manhattan pixel distance a press must travel before it's
	// a chip DRAG (place on the stage) rather than a click (toggle) — #27 slice 2b.
	toolboxDragThresh = 4
)

// toolboxItem is one chip: a hideable id and its short label.
type toolboxItem struct{ id, label string }

// hideableSlot maps a hideable element id → the layout slot the editor positions it
// through. Only elements WITH a slot are drag show/hide targets (#27 slice 2): you can
// drag them between the stage and the toolbox. The rest (penalty bars, timers,
// testimony, judge, extras, the shouts/knobs that live inside the controls block, and
// the tabs — which the tab tray tears) stay click-toggle only. For the control buttons
// the hideable id IS the slot key.
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
// slot has no hideable mapping (the viewport, the IC bar, the controls block, …). Used
// on drag-release to turn "dropped a piece on the toolbox" into a hide.
func hideableForSlot(slotKey string) string {
	for id, sk := range hideableSlot {
		if sk == slotKey {
			return id
		}
	}
	return ""
}

// classicToolboxItems gathers the chips for this frame: every chrome panel, plus the
// control buttons when the new-default toolbar is active (the legacy/themed row draws
// fixed inline buttons that ignore the hidden set, so a chip there would be a dead
// toggle — mirrors drawUICfgPanel's showBtnGrid guard).
func (a *App) classicToolboxItems() []toolboxItem {
	items := make([]toolboxItem, 0, len(hideablePanels)+len(hideableButtons))
	for _, p := range hideablePanels {
		items = append(items, toolboxItem{p.id, p.short})
	}
	if !a.d.Prefs.LegacyDevThemeOn() {
		for _, b := range hideableButtons {
			items = append(items, toolboxItem{b.id, b.label})
		}
	}
	return items
}

// drawClassicToolbox paints the show/hide chip strip and toggles a piece on click.
// Returns whether the cursor is over the strip, so the editor suppresses a slot-move
// under it (like the tab tray's overTray). Edit-only.
func (a *App) drawClassicToolbox(w, h int32, pressed bool) bool {
	c := a.ctx
	items := a.classicToolboxItems()

	// First pass: wrap the chips to count rows, so the backing strip is sized to fit.
	rows := int32(1)
	x := int32(pad)
	for _, it := range items {
		cw := c.TextWidth(it.label) + 18
		if x > pad && x+cw > w-pad {
			x = pad
			rows++
		}
		x += cw + 6
	}
	stripH := 18 + rows*toolboxRowPitch + 4
	strip := sdl.Rect{X: 0, Y: toolboxTop - 4, W: w, H: stripH}
	a.classicChromeBot = strip.Y + strip.H // the lowest editor chrome strip: slot tags clamp below it
	over := pointIn(c.mouseX, c.mouseY, strip)
	// "Drop here to hide" affordance: while dragging a slotted piece, the strip arms
	// (greenish) and the heading invites the release (slice 2a drag-to-hide).
	dropArmed := a.classicEditDrag != 0 && hideableForSlot(a.classicEditKey) != ""
	stripCol := sdl.Color{R: 0, G: 0, B: 0, A: 205}
	if dropArmed && over {
		stripCol = sdl.Color{R: 25, G: 70, B: 30, A: 225}
	}
	c.Fill(strip, stripCol)
	heading := "Show / hide UI pieces (click a chip, or drag one onto the stage to place it):"
	if dropArmed {
		heading = "Release here to HIDE " + classicSlotLabel(a.classicEditKey)
	}
	c.Label(pad, toolboxTop-2, heading, ColTierYellow)

	// Resolve a chip drag that ended this frame, then track an in-progress one (#27
	// slice 2b): a release without moving toggles the piece (slice 1); dragging a HIDDEN
	// chip out onto the stage shows it.
	if a.toolboxDragID != "" && !c.mouseDown {
		a.resolveToolboxDrag(over)
		a.toolboxDragID, a.toolboxDragMoved = "", false
	}
	if a.toolboxDragID != "" && c.mouseDown {
		dx, dy := c.mouseX-a.toolboxDragStart[0], c.mouseY-a.toolboxDragStart[1]
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx+dy > toolboxDragThresh {
			a.toolboxDragMoved = true
		}
	}

	// Second pass: draw the chips + arm a drag on press over one.
	x = int32(pad)
	y := toolboxTop + 16
	dragLabel := ""
	for _, it := range items {
		cw := c.TextWidth(it.label) + 18
		if x > pad && x+cw > w-pad {
			x = pad
			y += toolboxRowPitch
		}
		chip := sdl.Rect{X: x, Y: y, W: cw, H: toolboxChipH}
		hidden := a.panelHidden(it.id)
		bg := ColPanel // hidden → dim
		if !hidden {
			bg = ColAccent // shown → filled accent
		}
		if a.toolboxDragID == it.id || pointIn(c.mouseX, c.mouseY, chip) {
			bg = ColPanelHi // hover / grabbed
		}
		c.Fill(chip, bg)
		c.Border(chip, ColAccent)
		txtCol := ColText
		if hidden {
			txtCol = ColTextDim
		}
		c.LabelClipped(chip.X+6, chip.Y+3, chip.W-12, it.label, txtCol)
		if pressed && a.toolboxDragID == "" && pointIn(c.mouseX, c.mouseY, chip) {
			a.toolboxDragID = it.id
			a.toolboxDragStart = [2]int32{c.mouseX, c.mouseY}
			a.toolboxDragMoved = false
		}
		if it.id == a.toolboxDragID {
			dragLabel = it.label
		}
		x += cw + 6
	}

	// Ghost chip trailing the cursor while a chip is dragged out, so you can see what
	// you're placing on the stage.
	if a.toolboxDragID != "" && a.toolboxDragMoved && dragLabel != "" {
		gw := c.TextWidth(dragLabel) + 18
		ghost := sdl.Rect{X: c.mouseX - gw/2, Y: c.mouseY - toolboxChipH/2, W: gw, H: toolboxChipH}
		c.Fill(ghost, ColAccent)
		c.Border(ghost, ColTierYellow)
		c.LabelClipped(ghost.X+6, ghost.Y+3, ghost.W-12, dragLabel, ColText)
	}
	return over
}

// resolveToolboxDrag finishes a chip drag (#27 slice 2b): a click (no move) toggles the
// piece; dragging a HIDDEN chip out onto the stage (released off the toolbox) shows it.
// Dragging a shown chip, or dropping back on the toolbox, is a no-op.
func (a *App) resolveToolboxDrag(overToolbox bool) {
	id := a.toolboxDragID
	hidden := a.panelHidden(id)
	switch {
	case !a.toolboxDragMoved:
		a.setPanelHidden(id, !hidden)
	case hidden && !overToolbox:
		a.setPanelHidden(id, false)
		a.pushDebug("layout: dragged a piece onto the stage → shown")
	}
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
// expanded icon-chip row. Called from the normal-play courtroom (classic +
// themed); NOT drawn in theater mode, while editing a layout, or when hidden via
// panelToolbox. A1: the grip is now a press-to-pin latch (click toggles
// toolboxPinned), the chips draw vector icons, and while the user has never
// expanded it the collapsed grip wears a faint accent discoverability ring.
func (a *App) drawCompactToolbox(w, h int32) {
	if a.panelHidden(panelToolbox) {
		return
	}
	c := a.ctx
	// The collapsed grip: bottom-right corner, slim + semi-transparent.
	grip := sdl.Rect{
		X: w - compactGripW - compactToolboxMargin,
		Y: h - compactGripH - compactToolboxMargin,
		W: compactGripW, H: compactGripH,
	}
	// Expanded footprint (grip + chips to its left) so the strip stays open while
	// the cursor is anywhere over a chip, not just the grip. Measured first so the
	// hover test covers the whole strip. Chips are square (icon-only), so each is
	// as wide as it is tall.
	stripW := compactGripW
	for range compactToolboxChips {
		stripW += compactChipH + 4
	}
	strip := sdl.Rect{X: w - stripW - compactToolboxMargin, Y: grip.Y, W: stripW, H: compactGripH}
	hoverArea := sdl.Rect{X: grip.X - compactHoverPad, Y: grip.Y - compactHoverPad,
		W: grip.W + compactHoverPad, H: grip.H + 2*compactHoverPad}
	expanded := a.toolboxPinned || c.hovering(hoverArea) || c.hovering(strip)

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
	// tooltip carrying the full word.
	c.Fill(strip, sdl.Color{R: 0, G: 0, B: 0, A: 205})
	c.Border(strip, ColAccent)
	x := w - compactGripW - compactToolboxMargin
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
	// closes/opens without needing a chip).
	if c.hovering(grip) && c.clicked {
		a.compactTogglePin()
	}
	c.Tooltip(grip, "Toolbox — click to pin/unpin")
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
		c.Fill(chip, bg)
		c.Border(chip, ColAccent)
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
			a.setPanelHidden(p.id, next)
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
				a.setPanelHidden(b.id, next)
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
			a.setPanelHidden(b.id, next)
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
