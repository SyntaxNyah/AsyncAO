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
// existing overTray drag-release seam. The dialog stays for now: the THEMED-layout
// editor (startLayoutEdit) shares the same hidden set, and the dialog also hosts
// Theater mode + the themed Edit-layout entry, so it can't retire until the toolbox
// covers those too.

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
	"ctrl.editlayout": "ctrl.editlayout",
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
// LEFT into a row of small icon chips with tooltip labels: Theater, Edit layout,
// and Hide UI (the last opens the existing checkbox dialog, so the per-piece
// list stays reachable). Theater + the themed Edit-layout entry now live here
// too, so the Hide-UI dialog is no longer their only home.
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
)

// compactChip is one hover-revealed icon chip: its label, an accessibility
// tooltip, and the action it runs. run is a method value — the slice is built
// once (compactToolboxChips), so no closure is allocated per frame.
type compactChip struct {
	label, tip string
	run        func(a *App)
}

// compactToolboxChips is the fixed chip set, right-to-left from the grip. Kept
// tiny on purpose (#27): Theater, Edit layout, Hide-UI. Constant labels ⇒ the
// per-frame TextWidth probes are cache hits and the slice never re-allocates.
// Labels are plain ASCII short words (no decorative glyphs) so they render on
// any chrome font — the "icon" feel comes from the compact chip form + the drawn
// hamburger grip, not a font glyph that could tofu.
var compactToolboxChips = []compactChip{
	{"Theater", "Theater mode — stage only, Esc exits", (*App).compactTheater},
	{"Edit", "Edit layout — drag & resize every box", (*App).compactEditLayout},
	{"Hide UI", "Hide UI pieces — per-piece show/hide list", (*App).compactHideUI},
}

func (a *App) compactTheater()    { a.setTheater(!a.theaterOn) }
func (a *App) compactEditLayout() { a.openLayoutEditor() }
func (a *App) compactHideUI()     { a.showUICfg = true }

// drawCompactToolbox paints the collapsed grip and, while hovered, the expanded
// icon-chip row. Called from the normal-play courtroom (classic + themed); NOT
// drawn in theater mode, while editing a layout, or when hidden via panelToolbox.
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
	// hover test covers the whole strip.
	stripW := compactGripW
	for _, ch := range compactToolboxChips {
		stripW += c.TextWidth(ch.label) + 2*compactChipPad + 4
	}
	strip := sdl.Rect{X: w - stripW - compactToolboxMargin, Y: grip.Y, W: stripW, H: compactGripH}
	hoverArea := sdl.Rect{X: grip.X - compactHoverPad, Y: grip.Y - compactHoverPad,
		W: grip.W + compactHoverPad, H: grip.H + 2*compactHoverPad}
	expanded := c.hovering(hoverArea) || c.hovering(strip)

	if !expanded {
		// Collapsed: a slim translucent tab with a hamburger primitive (drawn, not
		// a glyph) so it renders on any font and stays unobtrusive.
		c.Fill(grip, sdl.Color{R: 0, G: 0, B: 0, A: 120})
		barW := grip.W - 8
		for i := int32(0); i < 3; i++ {
			bar := sdl.Rect{X: grip.X + 4, Y: grip.Y + 5 + i*5, W: barW, H: 2}
			c.Fill(bar, sdl.Color{R: 200, G: 200, B: 210, A: 180})
		}
		c.Tooltip(hoverArea, "Toolbox — hover for Theater, Edit layout & Hide UI")
		return
	}

	// Expanded: chips laid out right-to-left from the grip, each with a tooltip.
	c.Fill(strip, sdl.Color{R: 0, G: 0, B: 0, A: 205})
	c.Border(strip, ColAccent)
	x := w - compactGripW - compactToolboxMargin
	// The grip stays as the right-hand anchor (a filled accent nub) so it's clear
	// where the strip folds back to.
	c.Fill(grip, ColPanelHi)
	for i := int32(0); i < 3; i++ {
		bar := sdl.Rect{X: grip.X + 4, Y: grip.Y + 5 + i*5, W: grip.W - 8, H: 2}
		c.Fill(bar, ColText)
	}
	for _, ch := range compactToolboxChips {
		cw := c.TextWidth(ch.label) + 2*compactChipPad
		x -= cw + 4
		chip := sdl.Rect{X: x, Y: grip.Y + (compactGripH-compactChipH)/2, W: cw, H: compactChipH}
		if c.Button(chip, ch.label) {
			ch.run(a)
		}
		c.Tooltip(chip, ch.tip)
	}
}
