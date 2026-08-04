package ui

import (
	"strings"
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
	// The Music panel's header row (musicheader.go). Axis-aligned like the rest:
	// AO2 uses Qt pixmaps for these, which we have no equivalent of, and a font
	// glyph is a tofu risk on any face without Geometric Shapes.
	iconStop        // an inset filled square (matches KFO's stop button)
	iconDice        // a bordered square with five pips — "play a random song"
	iconExpandAll   // a bordered box with a plus bar
	iconCollapseAll // a bordered box with a minus bar
	iconKebab       // three stacked squares — the overflow menu
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
	case iconStop:
		// One filled square, inset a little so it reads as a symbol rather than a
		// block fill of the whole chip.
		in := iw / 6
		c.Fill(sdl.Rect{X: ix + in, Y: iy + in, W: iw - 2*in, H: ih - 2*in}, col)
	case iconDice:
		// A bordered square with five pips (the "5" face) — the smallest die face
		// that still reads as a die at 22 px.
		box := sdl.Rect{X: ix, Y: iy, W: iw, H: ih}
		c.Border(box, col)
		pip := iw / 5
		if pip < 2 {
			pip = 2
		}
		cx, cy := ix+(iw-pip)/2, iy+(ih-pip)/2
		off := iw / 4
		for _, p := range [...]sdl.Point{
			{X: cx, Y: cy},
			{X: cx - off, Y: cy - off}, {X: cx + off, Y: cy - off},
			{X: cx - off, Y: cy + off}, {X: cx + off, Y: cy + off},
		} {
			c.Fill(sdl.Rect{X: p.X, Y: p.Y, W: pip, H: pip}, col)
		}
	case iconExpandAll, iconCollapseAll:
		// A bordered box carrying a minus bar, plus a vertical bar for expand —
		// deliberately the same +/- language as the per-category fold markers
		// ("[+]" / "[-]"), so the two affordances read as one idea.
		box := sdl.Rect{X: ix, Y: iy, W: iw, H: ih}
		c.Border(box, col)
		th := ih / 5
		if th < 2 {
			th = 2
		}
		barW := iw / 2
		c.Fill(sdl.Rect{X: ix + (iw-barW)/2, Y: iy + (ih-th)/2, W: barW, H: th}, col)
		if k == iconExpandAll {
			barH := ih / 2
			c.Fill(sdl.Rect{X: ix + (iw-th)/2, Y: iy + (ih-barH)/2, W: th, H: barH}, col)
		}
	case iconKebab:
		// Three stacked squares down the centre.
		sq := iw / 4
		if sq < 2 {
			sq = 2
		}
		gap := (ih - 3*sq) / 4
		if gap < 1 {
			gap = 1
		}
		x := ix + (iw-sq)/2
		for i := int32(0); i < 3; i++ {
			c.Fill(sdl.Rect{X: x, Y: iy + gap + i*(sq+gap), W: sq, H: sq}, col)
		}
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

// Dwell-tooltip ids for the toolbox. TooltipAfter keys its shared dwell timer on
// the id, so the ids must be CONSTANT strings: this strip draws inside the
// courtroom's zero-alloc gate, and building "toolbox:chip:"+n per frame would put
// a concatenation on that path. A fixed table indexed by chip position costs
// nothing and cannot drift out of sync with compactToolboxChips (a test pins the
// two lengths equal).
const (
	toolboxTipIDCollapsed = "toolbox:collapsed"
	toolboxTipIDGrip      = "toolbox:grip"
)

var toolboxChipTipIDs = [...]string{"toolbox:chip:0", "toolbox:chip:1", "toolbox:chip:2", "toolbox:chip:3"}

// toolboxChipTipID is the dwell id for chip i, falling back to the grip's id if
// the chip table ever outgrows the id table (a shared id merely makes two chips
// share one dwell timer — never a crash on the render path).
func toolboxChipTipID(i int) string {
	if i < 0 || i >= len(toolboxChipTipIDs) {
		return toolboxTipIDGrip
	}
	return toolboxChipTipIDs[i]
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

// drawCompactToolboxInPass is the IN-PASS draw site (classic screens.go / themed
// theme_layout.go). It paints exactly what fenceCompactToolbox already decided and
// published for this frame — never re-deriving the predicate — so the fence and the
// pixels can never disagree. No latch (a modal owns the screen, an editor is armed,
// theater, or the toolbox is hidden) ⇒ nothing drew, so nothing is drawn.
func (a *App) drawCompactToolboxInPass() {
	if !a.toolboxFence.set {
		return
	}
	a.drawCompactToolbox(a.toolboxFence)
}

// drawCompactToolboxOverEditor is the POST-courtroom draw site (app.go): while a
// layout editor is armed the toolbox draws there instead, over the editor overlay
// and outside the editor's modal fence, force-expanded so Edit/Hide-UI/Theater stay
// reachable. It evaluates its own latch because the in-pass fence deliberately
// stands down for that frame.
//
// It stands down in turn if the in-pass site already took a latch this frame: a
// hotkey can arm the editor from INSIDE the pass (handleHotkeys runs mid-pass), and
// drawing the toolbox twice would hit-test its chips twice on a single click —
// toggling the pin latch straight back off.
func (a *App) drawCompactToolboxOverEditor(w, h int32) {
	if a.toolboxFence.set {
		return
	}
	a.drawCompactToolbox(a.latchCompactToolbox(w, h))
}

// drawCompactToolbox paints the collapsed grip and, while hovered OR pinned, the
// expanded icon-chip row, from an already-decided latch. In normal play it draws
// in-pass (classic + themed courtroom); while a layout editor is armed it draws
// POST-courtroom instead (app.go, fence released) and force-expands so the editor's
// fence can't blank its grip/chips (A1 Phase 1). NOT drawn in theater mode or when
// hidden via panelToolbox. A1: the grip is a press-to-pin latch (click toggles
// toolboxPinned), the chips draw vector icons, and while the user has never expanded
// it the collapsed grip wears a faint accent discoverability ring.
func (a *App) drawCompactToolbox(lat compactToolboxLatch) {
	if !lat.draws {
		return
	}
	c := a.ctx
	// Geometry comes from the latch, not a fresh compactToolboxStripRect call: the
	// rect the fence published IS the rect painted here (mirrors toolboxPiecesRect's
	// reason for existing). The strip routes through the slotToolbox override (A1
	// Phase 2), and the grip is DERIVED from the strip's right end rather than
	// computed independently — a moved toolbox carries its grip with it.
	strip := lat.strip
	// The collapsed grip: the strip's right end, slim + semi-transparent.
	grip := compactToolboxGripRect(strip)
	hoverArea := compactToolboxHoverRect(grip)
	// The toolbox is an OCCLUDER: fenceCompactToolbox published this strip at the
	// top of the courtroom pass so the widgets underneath it stay inert (#26). Own
	// the pointer for our own draw or the fence would blank the grip and every chip
	// — the same self-fencing an open dropdown dodges with raw pointIn(). The mark is
	// the registry depth from just BEFORE our own publication, so an occluder that
	// published EARLIER (an app-wide menu bar's open dropdown) still blanks us; a
	// latch that published nothing carries the live depth and suspends nothing.
	prevOwner := c.pushOverlayOwner(lat.mark)
	defer c.popOverlayOwner(prevOwner)
	expanded := lat.expanded

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
		c.TooltipAfter(toolboxTipIDCollapsed, hoverArea, "Toolbox — hover or click for Theater, Edit layout & Hide UI")
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
	// NOT consumed (no `c.clicked = false`). The press is already exclusive where it
	// needs to be: the overlay fence blanks everything drawn UNDER us in this pass,
	// and the post-courtroom floating surfaces that could paint OVER us run the whole
	// courtroom pass pointer-blind while the cursor is on them (boxFencesPointer), so
	// this branch cannot even be reached from under one. Consuming would invert Z for
	// anything drawn later that ISN'T pointer-fenced — the later widget owns the
	// pixels, so it must own the click. The one genuine double-fire (a chip arming the
	// editor mid-pass, then the post-courtroom site drawing the toolbox a second time)
	// is closed structurally by drawCompactToolboxOverEditor's latch guard instead.
	if !editing && c.hovering(grip) && c.clicked {
		a.compactTogglePin()
	}
	gripTip := "Toolbox — click to pin/unpin"
	if editing {
		gripTip = "Toolbox — drag this grip to move it"
	}
	// The whole strip is the tooltip's avoid rect, and the hints are DWELL hints.
	// Both for the same reason: this strip lives in the bottom-right corner, on top
	// of whatever the theme declared there (AO2's call_mod / change_character /
	// reload_theme cluster on the reference themes), so an instant ~262x30 hint
	// thrown at the first hovered frame wallpapered the theme's own buttons as the
	// cursor swept past. TooltipAfter makes it deliberate; TooltipGroup keeps the
	// box off the strip AND off its neighbouring chips once it does show.
	c.TooltipGroup(strip)
	c.TooltipAfter(toolboxTipIDGrip, grip, gripTip)
	for i, ch := range compactToolboxChips {
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
			ch.run(a) // not consumed — see the grip branch above for why
		}
		c.TooltipAfter(toolboxChipTipID(i), chip, ch.tip)
	}
	c.TooltipGroup(sdl.Rect{}) // the strip's claim ends with its chips
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

// compactToolboxHoverRect is the collapsed grip's forgiving hover target: a
// compactHoverPad halo on the left/top/bottom so the strip doesn't fold shut the
// instant the cursor grazes a chip's edge. The RIGHT edge is unpadded (X moves left
// by the pad and W grows by exactly the same pad, so X+W is unchanged) — the strip
// expands leftwards, so that is the side the cursor travels along. Factored out so
// the reveal predicate (compactToolboxExpanded) and the collapsed tooltip agree.
func compactToolboxHoverRect(grip sdl.Rect) sdl.Rect {
	return sdl.Rect{X: grip.X - compactHoverPad, Y: grip.Y - compactHoverPad,
		W: grip.W + compactHoverPad, H: grip.H + 2*compactHoverPad}
}

// compactToolboxExpanded reports whether the toolbox paints its EXPANDED chip strip
// this frame (rather than just the collapsed grip). While a layout editor is armed
// the toolbox draws post-courtroom as a stable target and force-expands, so its
// grip/chips are always reachable for Edit/Hide-UI/Theater without hunting for the
// hover sweet spot over the busy editor overlay (A1 Phase 1).
//
// Evaluated ONCE per frame, by latchCompactToolbox at the top of the courtroom pass
// (the draw replays the answer): the fence must cover the strip on exactly the frames
// the strip is on screen, and a second evaluation at draw time could disagree — the
// cursor is re-read from the same Ctx, but classicEdit / layoutEdit can be flipped by
// handleHotkeys in between.
func (a *App) compactToolboxExpanded(strip, grip sdl.Rect) bool {
	c := a.ctx
	return a.classicEdit || a.layoutEdit || a.toolboxPinned ||
		c.hovering(compactToolboxHoverRect(grip)) || c.hovering(strip)
}

// compactToolboxLatch is the ONE answer the compact toolbox's overlay fence and its
// draw share for a frame. The fence has to be published at the TOP of the courtroom
// pass (a fence is only consulted at hit-test time, and a single-pass kit hit-tests
// as it draws) while the toolbox itself paints at the BOTTOM — so between the two
// sits handleHotkeys, which can flip classicEdit / layoutEdit / the hidden set, and
// drawCourtroomModals, which returns the pass outright. Re-deriving the predicate at
// the draw would let those answer differently on one frame, and a fence that
// disagrees with the draw eats clicks over pixels nothing painted: the mirror image
// of the leak the fence exists to fix.
type compactToolboxLatch struct {
	// set marks that the IN-PASS site owns the toolbox this frame. False means the
	// post-courtroom site (app.go, a layout editor armed) owns it instead — exactly
	// one of the two draws per frame.
	set bool
	// draws is "the toolbox paints at all": false when it is hidden via panelToolbox
	// or a modal took the screen before the draw was reached.
	draws bool
	// expanded is "the chip strip paints" (vs. the slim collapsed grip alone), which
	// is also the condition under which a fence was published.
	expanded bool
	// strip is the exact rect published and painted — one computation, so the two
	// cannot drift even if a slot override or the themed rect moves mid-frame.
	strip sdl.Rect
	// mark is the fence-registry depth from just BEFORE our own publication, handed
	// to pushOverlayOwner so the toolbox is exempt from its OWN rect but still
	// occluded by anything published earlier (overlayfence.go).
	mark int
}

// latchCompactToolbox computes that answer. Split out so the post-courtroom draw
// site can reuse the identical predicate without publishing a fence.
//
// Geometry note: the strip's position depends on a.toolboxThemeRectOn
// (compactToolboxStripRect), which drawCourtroom force-clears every frame and only
// drawCourtroomThemed re-arms — so this must run INSIDE the pass, below the theme
// dispatch, never from an App-level pre-pass where the flag is stale.
func (a *App) latchCompactToolbox(w, h int32) compactToolboxLatch {
	lat := compactToolboxLatch{set: true, mark: a.ctx.overlayFenceMark()}
	if a.panelHidden(panelToolbox) {
		return lat // drawCompactToolbox early-returns; nothing is painted, so nothing is fenced
	}
	lat.draws = true
	lat.strip = a.compactToolboxStripRect(w, h)
	lat.expanded = a.compactToolboxExpanded(lat.strip, compactToolboxGripRect(lat.strip))
	return lat
}

// fenceCompactToolbox latches this frame's answer and publishes the toolbox's
// painted footprint as an overlay fence (overlayfence.go) so the widgets it covers
// stay inert underneath it — issue #26: the strip parks over an AO2 theme's own
// control buttons, and a press on a chip used to fire "Call Mod" underneath as well,
// because the theme button draws EARLIER in the same single pass and had no way to
// know.
//
// Two call sites, both INSIDE the courtroom pass: after the theme dispatch for the
// classic path, and immediately after the asyncao_toolbox arming for the themed one.
//
// Publishes NOTHING unless the expanded strip really paints:
//   - a layout editor is armed: the in-pass draw is skipped (the toolbox draws
//     post-courtroom instead) and the editor's own modalOn already fences everything.
//     The latch stays UNSET so the post-courtroom site knows it owns the draw.
//   - a return-to-top modal is open: drawCourtroomModals ends the pass before the
//     toolbox draw is ever reached, so a fence here would blank a ~122×22 band of
//     the modal over pixels nothing painted.
//   - hidden via panelToolbox: drawCompactToolbox early-returns, nothing is drawn.
//   - collapsed: only the slim grip paints. Fencing the whole strip there would eat
//     clicks over empty pixels, and it would be pointless anyway — the pointer being
//     anywhere on the grip is itself what expands the strip on the very same frame.
func (a *App) fenceCompactToolbox(w, h int32) {
	c := a.ctx
	a.toolboxFence = compactToolboxLatch{mark: c.overlayFenceMark()}
	if a.classicEdit || a.layoutEdit {
		return
	}
	if a.courtroomModalUp() {
		// set, but draws=false: the in-pass site is still the owner (the editor is
		// not armed), it simply never gets there.
		a.toolboxFence.set = true
		return
	}
	a.toolboxFence = a.latchCompactToolbox(w, h)
	if !a.toolboxFence.expanded {
		return
	}
	c.fenceOverlay(a.toolboxFence.strip)
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
// toolboxPiecesMinW / toolboxPiecesMinH bound a dragged-and-resized panel. Wide
// enough for the filter box and the Close button — the no-strand lifeline —
// side by side, and tall enough for the header, one row and the footer.
const (
	toolboxPiecesMinW = 200
	toolboxPiecesMinH = toolboxPiecesHeaderH + toolboxPiecesFooterH + toolboxPiecesRowPitch
)

// toolboxPiecesRect is where the Hide-UI-pieces panel sits: wherever the user
// last dragged it, or its old bottom-right home the first time.
//
// It was fixed in that corner with no way to move it — dragging did nothing, and
// the layout editor did not offer it either — so a panel that landed awkwardly,
// or underneath something else, could not be shifted at all. It is a floatWin
// now, which brings the drag, the cross-session position and the de-overlap
// cascade with it (panelSlotTable, floatbox.go).
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
	if panelH < toolboxPiecesMinH {
		panelH = toolboxPiecesMinH
	}
	pw := toolboxPiecesW
	if pw > w-2*compactToolboxMargin {
		pw = w - 2*compactToolboxMargin
	}
	// Never placed and nothing saved: keep the original corner, so an existing
	// user's panel does not move on them just because it became draggable.
	if !a.piecesWin.placed {
		if r, ok := a.seedPanelFromSlot(&a.piecesWin, slotPanelPieces, pw, panelH, toolboxPiecesMinW, toolboxPiecesMinH, w, h); ok {
			return r
		}
		x := w - pw - compactToolboxMargin
		y := h - compactGripH - compactToolboxMargin - toolboxPiecesTopGap - panelH
		if y < compactToolboxMargin {
			y = compactToolboxMargin
		}
		return sdl.Rect{X: x, Y: y, W: pw, H: panelH}
	}
	return a.piecesWin.rect(pw, panelH, toolboxPiecesMinW, toolboxPiecesMinH, w, h)
}

const (
	// toolboxPiecesHeaderH is the fixed title strip at the pieces panel top. It
	// hosts the title AND the Phase-3 filter box (both outside the scroll body, so
	// the filter never scrolls away).
	toolboxPiecesHeaderH = int32(56)
	// toolboxPiecesFooterH is the fixed footer (Close button) at the bottom.
	toolboxPiecesFooterH = int32(34)
	// toolboxPiecesTopGap separates the pieces panel from the toolbox strip.
	toolboxPiecesTopGap = int32(6)
	// toolboxFilterH / toolboxFilterY size and place the per-piece filter box within
	// the header strip (Phase 3 "easier to navigate"). Named per rule 9. The box sits
	// under the title, inset by pad on each side; its width is derived from the panel
	// at draw time (panel.W - 2*pad).
	toolboxFilterH = int32(22)
	toolboxFilterY = int32(28)
)

// toolboxPieceSearch maps each hideable id → its LOWERED searchable text, built
// ONCE at package init from the three registries (panels carry both their long
// label and short chip text; buttons/roster carry their label). Precomputing here
// keeps the per-piece filter match in drawToolboxPieces allocation-free — it
// compares two already-lowered strings via strings.Contains, never lowering a
// label per row per frame (the panel draws every frame in normal play; rule 8 /
// the alloc gates). The query itself is lowered once, only when it changes.
var toolboxPieceSearch = buildToolboxPieceSearch()

func buildToolboxPieceSearch() map[string]string {
	m := make(map[string]string, len(hideablePanels)+len(hideableButtons)+len(hideableRosterButtons))
	for _, p := range hideablePanels {
		m[p.id] = strings.ToLower(p.label + " " + p.short)
	}
	for _, b := range hideableButtons {
		m[b.id] = strings.ToLower(b.label)
	}
	for _, b := range hideableRosterButtons {
		m[b.id] = strings.ToLower(b.label)
	}
	return m
}

// toolboxPieceMatches reports whether the piece id passes the active filter. An
// empty query matches everything (the filter is inert until typed into). Both
// operands are pre-lowered, so strings.Contains allocates nothing.
func toolboxPieceMatches(id, queryLower string) bool {
	if queryLower == "" {
		return true
	}
	return strings.Contains(toolboxPieceSearch[id], queryLower)
}

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

// mouseLifelines are the chrome routes a pure-mouse user has back to the client's
// own controls. Hiding the LAST surviving one is refused (A6, #21).
//
// The set is three, not two, and the third is why: ctrlSettingsSlot is drawn only
// by drawICControls, which runs on the CLASSIC path — a themed courtroom returns
// long before it. So for a themed user that lifeline never exists to be hidden,
// the old two-way guard could never fire, and hiding the toolbox and then the menu
// bar left no mouse chrome at all. The themed ★ Extras chip row that used to be
// the fallback was deleted in this arc (rule (c): it painted over the canvas).
var mouseLifelines = [...]string{panelToolbox, ctrlSettingsSlot, panelMenuBar}

// toggleChromePanel flips one chrome piece from the keyboard and says which way
// it went. The feedback is not decoration: these binds exist precisely for when
// you cannot see or reach the panel that owns the switch, so pressing one and
// getting no response at all would be its own dead end.
//
// Routed through setPanelHiddenGuarded rather than setPanelHidden, so the
// keyboard is held to the same no-strand rule as the panel. A hotkey that can
// strand you is worse than no hotkey: the person who pressed it is by definition
// the one who could not find the way back.
func (a *App) toggleChromePanel(id, label string) {
	hide := !a.panelHidden(id)
	a.setPanelHiddenGuarded(id, hide)
	if a.panelHidden(id) != hide {
		return // refused by the guard, which has set its own explanatory toast
	}
	a.warnLine = label + " shown"
	if hide {
		a.warnLine = label + " hidden"
	}
	a.warnAt = time.Now()
}

// setPanelHiddenGuarded is drawToolboxPieces' setPanelHidden with the no-strand
// guard: hiding the last mouse lifeline is refused with a toast explaining why,
// instead of applied-then-silently-undone. Every other toggle passes straight
// through. Wholesale hidden-set writes (profile apply, prefs import/reset)
// normalize the same invariant in seedHiddenFromPrefs instead.
func (a *App) setPanelHiddenGuarded(id string, hide bool) {
	if hide && a.lastMouseLifeline(id) {
		a.warnLine = "Kept: that's the last mouse route back to the client's controls. (Ctrl+F reopens this panel.)"
		a.warnAt = time.Now()
		return
	}
	a.setPanelHidden(id, hide)
}

// lastMouseLifeline reports whether hiding id would leave every mouse lifeline
// hidden. A id that is not a lifeline is never refused.
func (a *App) lastMouseLifeline(id string) bool {
	isLifeline := false
	for _, l := range mouseLifelines {
		if l == id {
			isLifeline = true
			break
		}
	}
	if !isLifeline {
		return false
	}
	for _, l := range mouseLifelines {
		if l != id && !a.panelHidden(l) {
			return false // something else still gets them back
		}
	}
	return true
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

	// The TITLE strip is the drag handle — the band above the filter box, not the
	// whole header, so grabbing it can never swallow a click meant for the filter
	// field. (The Close button lives in the footer, so it is clear either way.)
	// piecesPressed is this panel's OWN press edge: it draws over other surfaces
	// and so cannot share the frame's click.
	piecesPressed := c.mouseDown && !a.piecesPrevDown
	a.piecesPrevDown = c.mouseDown
	wasManip := a.piecesWin.dragging
	a.floatWinDrag(&a.piecesWin, sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W, H: toolboxFilterY}, &piecesPressed)
	if wasManip && !a.piecesWin.dragging {
		a.persistPanelSlot(slotPanelPieces, panel, w, h) // remember where, across sessions
	}
	if !c.mouseDown && wasManip {
		c.clicked = false // a finished drag is not a click on whatever is underneath
	}

	// Filter box (Phase 3): narrows the per-piece list across the same three-section
	// grouping. It lives in the header strip, OUTSIDE the scroll body, so it never
	// scrolls away and never displaces the Close button (the no-strand lifeline). The
	// raw text drives the TextField; the lowered form is cached and only re-derived
	// when the text changes, so per-row matching below stays allocation-free.
	filterRect := sdl.Rect{X: panel.X + pad, Y: panel.Y + toolboxFilterY, W: panel.W - 2*pad, H: toolboxFilterH}
	if next, _ := c.TextField("toolboxfilter", filterRect, a.toolboxFilter, "Filter pieces…"); next != a.toolboxFilter {
		a.toolboxFilter = next
		a.toolboxFilterLower = strings.ToLower(strings.TrimSpace(next)) // lowered ONCE per edit, not per frame
	}
	q := a.toolboxFilterLower

	// The control-button grid only applies to the new-default toolbar; the
	// legacy/themed row draws fixed inline buttons that ignore the hidden set, so
	// a chip there would be a dead toggle (mirrors the retired dialog's guard).
	showBtnGrid := !a.d.Prefs.LegacyDevThemeOn()

	body := sdl.Rect{X: panel.X, Y: panel.Y + toolboxPiecesHeaderH,
		W: panel.W, H: panel.H - toolboxPiecesHeaderH - toolboxPiecesFooterH}
	// Content height reflects the FILTERED rows so the scrollbar tracks what's
	// actually drawn (an unfiltered height would leave dead scroll space). Panel
	// SIZE stays on the unfiltered content (toolboxPiecesRect) so the box doesn't
	// resize as you type.
	contentH := a.toolboxPiecesFilteredContentH(q, showBtnGrid)
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
		if !toolboxPieceMatches(p.id, q) {
			continue
		}
		hidden := a.panelHidden(p.id)
		if next := c.Checkbox(panel.X+pad, y, "Hide "+p.short, hidden); next != hidden {
			a.setPanelHiddenGuarded(p.id, next)
		}
		y += toolboxPiecesRowPitch
	}
	if showBtnGrid {
		// The section heading only draws when the section has a visible row (so a
		// filter that excludes the whole section doesn't leave a stranded heading).
		if toolboxButtonsHaveMatch(q) {
			c.Label(panel.X+pad, y+4, "Control buttons (tick to hide):", ColTextDim)
			y += toolboxPiecesRowPitch
			// vis is the visible-row index so the 2-col grid stays gapless under a
			// filter (registry index would leave holes for filtered-out rows).
			vis := int32(0)
			for _, b := range hideableButtons {
				if !toolboxPieceMatches(b.id, q) {
					continue
				}
				cx := panel.X + pad + vis%toolboxPiecesCols*colW
				cy := y + vis/toolboxPiecesCols*toolboxPiecesRowPitch
				hidden := a.panelHidden(b.id)
				if next := c.Checkbox(cx, cy, b.label, hidden); next != hidden {
					a.setPanelHiddenGuarded(b.id, next)
				}
				vis++
			}
			y += toolboxGridRows(vis) * toolboxPiecesRowPitch
		}
	}
	if toolboxRosterHaveMatch(q) {
		c.Label(panel.X+pad, y+4, "Players-list row actions (tick to hide):", ColTextDim)
		y += toolboxPiecesRowPitch
		vis := int32(0)
		for _, b := range hideableRosterButtons {
			if !toolboxPieceMatches(b.id, q) {
				continue
			}
			cx := panel.X + pad + vis%toolboxPiecesCols*colW
			cy := y + vis/toolboxPiecesCols*toolboxPiecesRowPitch
			hidden := a.panelHidden(b.id)
			if next := c.Checkbox(cx, cy, b.label, hidden); next != hidden {
				a.setPanelHiddenGuarded(b.id, next)
			}
			vis++
		}
	}
	c.popClip(clipPrev, clipHad)

	// Fixed footer: a Close button (always reachable even when the grip is hidden,
	// so a hotkey-opened panel is never stranded — the un-strand path). Drawn
	// OUTSIDE the scroll body and OUTSIDE the filter, so no query can strand it.
	footerY := panel.Y + panel.H - btnH - 8
	if c.Button(sdl.Rect{X: panel.X + panel.W - 84 - pad, Y: footerY, W: 84, H: btnH}, "Close") {
		a.toolboxPieces = false
	}
}

// toolboxGridRows returns the number of rows a gapless toolboxPiecesCols-wide grid
// needs for n visible cells (ceil division). Shared by the draw and the filtered
// content-height math so they can't disagree. Named with the toolbox prefix to
// avoid colliding with screens.go's emote-grid gridRows(h, cellH).
func toolboxGridRows(n int32) int32 {
	return (n + toolboxPiecesCols - 1) / toolboxPiecesCols
}

// toolboxButtonsHaveMatch / toolboxRosterHaveMatch report whether the control /
// roster registry has any entry passing the filter — used to suppress a section
// heading whose whole section is filtered out. The registries are anonymous
// structs (no methods), so these iterate the concrete slices directly rather than
// through a generic constraint. Empty query ⇒ always true (filter inert).
func toolboxButtonsHaveMatch(queryLower string) bool {
	if queryLower == "" {
		return true
	}
	for _, b := range hideableButtons {
		if toolboxPieceMatches(b.id, queryLower) {
			return true
		}
	}
	return false
}

func toolboxRosterHaveMatch(queryLower string) bool {
	if queryLower == "" {
		return true
	}
	for _, b := range hideableRosterButtons {
		if toolboxPieceMatches(b.id, queryLower) {
			return true
		}
	}
	return false
}

// toolboxPanelsVisible / toolboxButtonsVisible / toolboxRosterVisible count the
// rows each section draws under the active filter — the filtered content-height
// math reuses these so the scrollbar tracks exactly what's drawn.
func toolboxPanelsVisible(queryLower string) int32 {
	if queryLower == "" {
		return int32(len(hideablePanels))
	}
	n := int32(0)
	for _, p := range hideablePanels {
		if toolboxPieceMatches(p.id, queryLower) {
			n++
		}
	}
	return n
}

func toolboxButtonsVisible(queryLower string) int32 {
	if queryLower == "" {
		return int32(len(hideableButtons))
	}
	n := int32(0)
	for _, b := range hideableButtons {
		if toolboxPieceMatches(b.id, queryLower) {
			n++
		}
	}
	return n
}

func toolboxRosterVisible(queryLower string) int32 {
	if queryLower == "" {
		return int32(len(hideableRosterButtons))
	}
	n := int32(0)
	for _, b := range hideableRosterButtons {
		if toolboxPieceMatches(b.id, queryLower) {
			n++
		}
	}
	return n
}

// toolboxPiecesFilteredContentH is the scroll-region height under the active
// filter: visible panel rows + (if the section has any match) its heading row +
// its gapless grid rows, for the control and roster sections. Mirrors
// toolboxPiecesContentH's arithmetic but over the filtered counts, so the
// scrollbar range matches the drawn rows exactly.
func (a *App) toolboxPiecesFilteredContentH(queryLower string, showBtnGrid bool) int32 {
	rows := toolboxPanelsVisible(queryLower)
	if showBtnGrid && toolboxButtonsHaveMatch(queryLower) {
		rows += 1 + toolboxGridRows(toolboxButtonsVisible(queryLower)) // +1 heading row
	}
	if toolboxRosterHaveMatch(queryLower) {
		rows += 1 + toolboxGridRows(toolboxRosterVisible(queryLower)) // +1 heading row
	}
	return rows*toolboxPiecesRowPitch + 8
}
