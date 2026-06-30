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
func (a *App) drawClassicToolbox(w, h int32) bool {
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
	over := pointIn(c.mouseX, c.mouseY, strip)
	// "Drop here to hide" affordance: while dragging a slotted piece, the strip arms
	// (greenish) and the heading invites the release (#27 slice 2 drag-to-hide).
	dropArmed := a.classicEditDrag != 0 && hideableForSlot(a.classicEditKey) != ""
	stripCol := sdl.Color{R: 0, G: 0, B: 0, A: 205}
	if dropArmed && over {
		stripCol = sdl.Color{R: 25, G: 70, B: 30, A: 225}
	}
	c.Fill(strip, stripCol)
	heading := "Show / hide UI pieces (click a chip — dim = hidden):"
	if dropArmed {
		heading = "Release here to HIDE " + classicSlotLabel(a.classicEditKey)
	}
	c.Label(pad, toolboxTop-2, heading, ColTierYellow)

	// Second pass: draw + handle clicks.
	x = int32(pad)
	y := toolboxTop + 16
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
		if pointIn(c.mouseX, c.mouseY, chip) {
			bg = ColPanelHi // hover
		}
		c.Fill(chip, bg)
		c.Border(chip, ColAccent)
		txtCol := ColText
		if hidden {
			txtCol = ColTextDim
		}
		c.LabelClipped(chip.X+6, chip.Y+3, chip.W-12, it.label, txtCol)
		if c.clicked && pointIn(c.mouseX, c.mouseY, chip) {
			a.setPanelHidden(it.id, !hidden)
		}
		x += cw + 6
	}
	return over
}
