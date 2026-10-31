package ui

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The compact bottom-right toolbox and its per-piece hide/show panel draw in normal play and in the layout editor.

// Collapsed it is a slim grip; on hover it expands left into icon chips with tooltips. It draws every frame, so chips
// are a fixed slice with constant labels, and the reveal is pure hover state that can't wake the render loop when idle.

const (
	// compactGripW/H size the collapsed grip; its height matches a chip so the expanded strip shares a baseline.
	compactGripW = int32(18)
	compactGripH = int32(22)
	// compactChipH / compactChipPad size an expanded icon chip and its inner text pad.
	compactChipH   = int32(22)
	compactChipPad = int32(8)
	// compactToolboxMargin insets the strip from the window's bottom-right corner.
	compactToolboxMargin = int32(4)
	// compactHoverPad grows the grip's hover target so the strip doesn't collapse when the cursor grazes a chip's edge.
	compactHoverPad = int32(6)
	// toolboxIconPad keeps the icon off the chip border.
	toolboxIconPad = int32(5)
	// toolboxRingAlpha is the grip's accent ring until first expanded (!ToolboxSeen); static so it never wakes the
	// loop.
	toolboxRingAlpha = uint8(90)
	// toolboxPiecesRowPitch is the per-piece checkbox row pitch.
	toolboxPiecesRowPitch = int32(26)
	// toolboxPiecesMaxH clamps the panel's height for a short window; the body scrolls inside it.
	toolboxPiecesMaxH = int32(360)
	// toolboxPiecesW is the panel's fixed width.
	toolboxPiecesW = int32(300)
	// toolboxPiecesCols is the column count of the panel's control/roster button grids.
	toolboxPiecesCols = int32(2)
)

// iconKind selects a glyph drawToolIcon composes from axis-aligned rects, since no glyph font can be relied on.
type iconKind int

const (
	iconTheater iconKind = iota // a wide stage bar over two short legs
	iconEdit                    // a diagonal-stepped 3-rect pencil
	iconEyeOff                  // a horizontal lens bar with a slash
	iconPin                     // a head rect over a vertical stem
	iconGrid                    // a 2×2 block of small rects
	// The Music panel's header row: AO2 uses Qt pixmaps here, and a font glyph can show as a missing-character box.
	iconStop        // an inset filled square (matches KFO's stop button)
	iconDice        // a bordered square with five pips — "play a random song"
	iconExpandAll   // a bordered box with a plus bar
	iconCollapseAll // a bordered box with a minus bar
	iconKebab       // three stacked squares — the overflow menu
	// iconPopOut is the sprite preview's pop-out toggle: a window outline with a smaller filled one up and to the
	// right.
	iconPopOut
)

// drawToolIcon paints the glyph for k inside r using only c.Fill rects, so it stays allocation-free.
func drawToolIcon(c *Ctx, k iconKind, r sdl.Rect, col sdl.Color) {
	// The chip rect inset by toolboxIconPad on every side.
	ix, iy := r.X+toolboxIconPad, r.Y+toolboxIconPad
	iw, ih := r.W-2*toolboxIconPad, r.H-2*toolboxIconPad
	if iw <= 0 || ih <= 0 {
		return
	}
	switch k {
	case iconTheater:
		// A wide top bar resting on two short legs.
		bar := sdl.Rect{X: ix, Y: iy, W: iw, H: ih / 3}
		c.Fill(bar, col)
		legW := iw / 4
		legY := iy + ih/3
		legH := ih - ih/3
		c.Fill(sdl.Rect{X: ix, Y: legY, W: legW, H: legH}, col)
		c.Fill(sdl.Rect{X: ix + iw - legW, Y: legY, W: legW, H: legH}, col)
	case iconEdit:
		// A pencil: three stepped squares along the diagonal plus a nib square.
		step := iw / 4
		if step < 2 {
			step = 2
		}
		th := step
		for i := int32(0); i < 3; i++ {
			bx := ix + i*step
			by := iy + ih - th - i*step
			if by < iy {
				by = iy
			}
			c.Fill(sdl.Rect{X: bx, Y: by, W: th, H: th}, col)
		}
		c.Fill(sdl.Rect{X: ix, Y: iy + ih - th/2, W: th, H: th / 2}, col)
	case iconEyeOff:
		// A lens bar with a thin bar crossing it: an eye struck through.
		lensH := ih / 3
		lensY := iy + (ih-lensH)/2
		c.Fill(sdl.Rect{X: ix, Y: lensY, W: iw, H: lensH}, col)
		// The slash is two offset segments.
		slashH := ih / 5
		if slashH < 2 {
			slashH = 2
		}
		c.Fill(sdl.Rect{X: ix, Y: iy + ih - slashH, W: iw / 2, H: slashH}, col)
		c.Fill(sdl.Rect{X: ix + iw/2, Y: iy, W: iw / 2, H: slashH}, col)
	case iconPin:
		// A pushpin: a wide head over a narrow stem.
		headH := ih / 3
		c.Fill(sdl.Rect{X: ix, Y: iy, W: iw, H: headH}, col)
		stemW := iw / 4
		if stemW < 2 {
			stemW = 2
		}
		c.Fill(sdl.Rect{X: ix + (iw-stemW)/2, Y: iy + headH, W: stemW, H: ih - headH}, col)
	case iconGrid:
		// A 2x2 block of small squares.
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
		// One filled square, inset so it reads as a symbol.
		in := iw / 6
		c.Fill(sdl.Rect{X: ix + in, Y: iy + in, W: iw - 2*in, H: ih - 2*in}, col)
	case iconDice:
		// A bordered square with five pips, the smallest die face readable at 22 px.
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
		// A bordered box with a minus bar, plus a vertical bar for expand: the same language as the "[+]" / "[-]"
		// markers.
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
	case iconPopOut:
		small := iw * 3 / 5
		if small < 2 {
			small = 2
		}
		smallH := ih * 3 / 5
		if smallH < 2 {
			smallH = 2
		}
		c.Border(sdl.Rect{X: ix, Y: iy + ih - smallH, W: small, H: smallH}, col)
		// The popped-out window is filled so the two read as distinct without a third colour.
		c.Fill(sdl.Rect{X: ix + iw - small, Y: iy, W: small, H: smallH}, col)
	}
}

// compactChip is one icon chip: its tooltip carries the full word, and run is a method value so nothing allocates.
type compactChip struct {
	icon iconKind
	tip  string
	run  func(a *App)
}

// Dwell-tooltip ids must be constant strings: TooltipAfter keys its timer on the id, and concatenating per frame
// would allocate on this path. A test holds this table to compactToolboxChips' length.
const (
	toolboxTipIDCollapsed = "toolbox:collapsed"
	toolboxTipIDGrip      = "toolbox:grip"
)

var toolboxChipTipIDs = [...]string{"toolbox:chip:0", "toolbox:chip:1", "toolbox:chip:2", "toolbox:chip:3"}

// toolboxChipTipID falls back to the grip's id past the table's end; a shared id only shares a dwell timer.
func toolboxChipTipID(i int) string {
	if i < 0 || i >= len(toolboxChipTipIDs) {
		return toolboxTipIDGrip
	}
	return toolboxChipTipIDs[i]
}

// compactToolboxChips is the fixed chip set, right to left from the grip: Pin, Theater, Edit layout, Hide-UI.
var compactToolboxChips = []compactChip{
	{iconPin, "Pin the toolbox open (press again or the grip to unpin)", (*App).compactTogglePin},
	{iconTheater, "Theater mode — stage only, Esc exits", (*App).compactTheater},
	{iconEdit, "Edit layout — drag & resize every box", (*App).compactEditLayout},
	{iconEyeOff, "Hide UI pieces — per-piece show/hide list", (*App).compactHideUI},
}

func (a *App) compactTheater()    { a.setTheater(!a.theaterOn) }
func (a *App) compactEditLayout() { a.openLayoutEditor() }

// compactTogglePin pins the flyout; unpinning also closes the per-piece panel so a later hover doesn't reveal it.
func (a *App) compactTogglePin() {
	a.toolboxPinned = !a.toolboxPinned
	if !a.toolboxPinned {
		a.toolboxPieces = false
	}
}

// compactHideUI toggles the per-piece panel. Opening also pins, because the panel only shows while pinned.
func (a *App) compactHideUI() {
	a.toolboxPieces = !a.toolboxPieces
	if a.toolboxPieces {
		a.toolboxPinned = true
	}
}

// drawCompactToolboxInPass paints what fenceCompactToolbox decided this frame, so blocking and pixels can't disagree.
func (a *App) drawCompactToolboxInPass() {
	if !a.toolboxFence.set {
		return
	}
	a.drawCompactToolbox(a.toolboxFence)
}

// drawCompactToolboxOverEditor draws post-courtroom over an armed editor, outside its modal blocking, force-expanded.
// It stands down if the in-pass site already drew: a hotkey can arm the editor mid-pass (handleHotkeys), and drawing
// twice would hit-test the chips twice on one click, toggling the pin straight back.
func (a *App) drawCompactToolboxOverEditor(w, h int32) {
	if a.toolboxFence.set {
		return
	}
	a.drawCompactToolbox(a.latchCompactToolbox(w, h))
}

// drawCompactToolbox paints the grip and, while hovered or pinned, the chips, from an already-decided answer. Not drawn
// in theater mode or when hidden via panelToolbox. While a layout editor is armed it draws post-courtroom.
func (a *App) drawCompactToolbox(lat compactToolboxLatch) {
	if !lat.draws {
		return
	}
	c := a.ctx
	// From lat, not a fresh compactToolboxStripRect call: the blocked rect is the painted rect. A moved toolbox carries
	// its grip, which derives from the strip.
	strip := lat.strip
	grip := compactToolboxGripRect(strip)
	hoverArea := compactToolboxHoverRect(grip)
	// The strip is published as an overlay covering the widgets beneath it, so own the pointer or it would blank our
	// own widgets. lat.mark predates our publication, so an earlier overlay (the menu bar's dropdown) still blanks us.
	prevOwner := c.pushOverlayOwner(lat.mark)
	defer c.popOverlayOwner(prevOwner)
	expanded := lat.expanded

	if expanded && !a.d.Prefs.Load().ToolboxSeen {
		// First expand stops the ring. The setter is idempotent, so no per-frame markDirty once seen.
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ToolboxSeen = true })
	}

	if !expanded {
		// A drawn hamburger rather than a glyph, so it renders on any font.
		c.Fill(grip, sdl.Color{R: 0, G: 0, B: 0, A: 120})
		barW := grip.W - 8
		for i := int32(0); i < 3; i++ {
			bar := sdl.Rect{X: grip.X + 4, Y: grip.Y + 5 + i*5, W: barW, H: 2}
			c.Fill(bar, sdl.Color{R: 200, G: 200, B: 210, A: 180})
		}
		if !a.d.Prefs.Load().ToolboxSeen {
			// Static, not a pulse, so it never registers an animating frame and wakes the loop.
			ring := sdl.Rect{X: grip.X - 1, Y: grip.Y - 1, W: grip.W + 2, H: grip.H + 2}
			c.Border(ring, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: toolboxRingAlpha})
		}
		c.TooltipAfter(toolboxTipIDCollapsed, hoverArea, "Toolbox — hover or click for Theater, Edit layout & Hide UI")
		return
	}

	// The strip stays sharp-cornered because the grip overlaps its right end and a rounded strip would show corner
	// nubs.
	c.Fill(strip, sdl.Color{R: 0, G: 0, B: 0, A: 205})
	c.Border(strip, ColAccent)
	x := grip.X // chips fan LEFT from the grip's left edge (grip is derived from the strip)
	// Accent colours show where the strip folds back to and whether it is pinned.
	gripCol := ColPanelHi
	if a.toolboxPinned {
		gripCol = ColAccent
	}
	c.Fill(grip, gripCol)
	for i := int32(0); i < 3; i++ {
		bar := sdl.Rect{X: grip.X + 4, Y: grip.Y + 5 + i*5, W: grip.W - 8, H: 2}
		c.Fill(bar, ColText)
	}
	// Clicking the grip toggles the pin, except while a layout editor is armed: there it is the drag handle, and the
	// same press already grabbed the toolbox, so a toggle would double-fire.
	editing := a.classicEdit || a.layoutEdit
	// Not consumed: overlay blocking already keeps widgets drawn under us from the press, and consuming would invert Z
	// for anything drawn later. The one double-fire is closed by drawCompactToolboxOverEditor's check.
	if !editing && c.hovering(grip) && c.clicked {
		a.compactTogglePin()
	}
	gripTip := "Toolbox — click to pin/unpin"
	if editing {
		gripTip = "Toolbox — drag this grip to move it"
	}
	// The strip sits on the theme's bottom-right buttons, so an instant hint would cover them as the cursor sweeps
	// past: TooltipAfter makes it deliberate, and TooltipGroup keeps the box off the strip and its neighbouring chips.
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
		// The Pin and Hide-UI chips show their state, so both read as toggles.
		if (ch.icon == iconPin && a.toolboxPinned) || (ch.icon == iconEyeOff && a.toolboxPieces) {
			bg = ColAccent
		}
		// The background follows the chrome shape; the icon stays sharp, like a shaped button's text.
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
	// The per-piece panel draws post-courtroom (drawToolboxPieces), where pointer blocking is lifted. That also keeps
	// it reachable by hotkey when the grip is hidden via panelToolbox.
}

// compactToolboxDefaultWidth is the expanded strip's intrinsic width: the grip plus one spaced square chip per chip.
func compactToolboxDefaultWidth() int32 {
	stripW := compactGripW
	for range compactToolboxChips {
		stripW += compactChipH + 4
	}
	return stripW
}

// compactToolboxDefaultRect is the slotToolbox default: bottom-right, inset by compactToolboxMargin.
func (a *App) compactToolboxDefaultRect(w, h int32) sdl.Rect {
	stripW := compactToolboxDefaultWidth()
	gripY := h - compactGripH - compactToolboxMargin
	return sdl.Rect{X: w - stripW - compactToolboxMargin, Y: gripY, W: stripW, H: compactGripH}
}

// compactToolboxGripRect is the grip at the strip's right end, the drag handle in the editor (like a title bar).
func compactToolboxGripRect(strip sdl.Rect) sdl.Rect {
	return sdl.Rect{X: strip.X + strip.W - compactGripW, Y: strip.Y, W: compactGripW, H: compactGripH}
}

// compactToolboxHoverRect pads the grip's hover target on the left, top and bottom but not the right: X moves left by
// the pad and W grows by it, so X+W is unchanged. The reveal test and the collapsed tooltip share it.
func compactToolboxHoverRect(grip sdl.Rect) sdl.Rect {
	return sdl.Rect{X: grip.X - compactHoverPad, Y: grip.Y - compactHoverPad,
		W: grip.W + compactHoverPad, H: grip.H + 2*compactHoverPad}
}

// compactToolboxExpanded reports whether the chip strip paints; an armed layout editor forces it. Evaluated once, by
// latchCompactToolbox: at draw time handleHotkeys may have flipped classicEdit / layoutEdit, and the answers would
// differ.
func (a *App) compactToolboxExpanded(strip, grip sdl.Rect) bool {
	c := a.ctx
	return a.classicEdit || a.layoutEdit || a.toolboxPinned ||
		c.hovering(compactToolboxHoverRect(grip)) || c.hovering(strip)
}

// compactToolboxLatch is the one answer blocking and draw share for a frame. Blocking is published at the top of the
// pass and painting happens at the bottom, and handleHotkeys and drawCourtroomModals sit between; disagreeing would eat
// clicks over pixels nothing painted.
type compactToolboxLatch struct {
	// set means the in-pass site owns the toolbox this frame; false means the post-courtroom site does.
	set bool
	// draws is false when hidden via panelToolbox or a modal took the screen before the draw.
	draws bool
	// expanded means the chip strip paints, which is also when blocking was published.
	expanded bool
	// strip is the one rect published and painted.
	strip sdl.Rect
	// mark is the overlay registry depth before our publication, so we are exempt from our own rect but not earlier
	// ones.
	mark int
}

// latchCompactToolbox computes that answer. It must run inside the pass, below the theme dispatch, because
// a.toolboxThemeRectOn is cleared every frame by drawCourtroom and re-armed only by drawCourtroomThemed.
func (a *App) latchCompactToolbox(w, h int32) compactToolboxLatch {
	lat := compactToolboxLatch{set: true, mark: a.ctx.overlayFenceMark()}
	if a.panelHidden(panelToolbox) {
		return lat // drawCompactToolbox early-returns; nothing is painted, so nothing is fenced
	}
	// In normal play the commands live on the Extras menu, the palette and the hotkeys; the strip draws only under an
	// armed layout editor, where the menu bar stands down.
	if !a.classicEdit && !a.layoutEdit {
		return lat
	}
	lat.draws = true
	lat.strip = a.compactToolboxStripRect(w, h)
	lat.expanded = a.compactToolboxExpanded(lat.strip, compactToolboxGripRect(lat.strip))
	return lat
}

// fenceCompactToolbox records this frame's answer and publishes the painted strip as an overlay, so a press on a chip
// doesn't also fire the theme button under it, which drew earlier in the same pass. Called inside the courtroom pass.
// Publishes nothing unless the expanded strip paints: with an armed editor (the answer stays unset, so the
// post-courtroom site draws), a return-to-top modal open, hidden, or collapsed, it would eat clicks over empty pixels.
func (a *App) fenceCompactToolbox(w, h int32) {
	c := a.ctx
	a.toolboxFence = compactToolboxLatch{mark: c.overlayFenceMark()}
	if a.classicEdit || a.layoutEdit {
		return
	}
	if a.courtroomModalUp() {
		// set with draws=false: the in-pass site still owns it but never gets there.
		a.toolboxFence.set = true
		return
	}
	a.toolboxFence = a.latchCompactToolbox(w, h)
	if !a.toolboxFence.expanded {
		return
	}
	c.fenceOverlay(a.toolboxFence.strip)
}

// compactToolboxStripRect is the strip's footprint (grip + chips), routed through the slotToolbox override.
// The editor's over-toolbox test shares it; it stays allocation-free.
func (a *App) compactToolboxStripRect(w, h int32) sdl.Rect {
	def := a.compactToolboxDefaultRect(w, h)
	// A theme's "asyncao_toolbox" rect wins over the classic slot, as asyncao_ic_fx does over slotICFx. Keep W/H;
	// clamp.
	if a.toolboxThemeRectOn {
		cur := def
		cur.X = value.Clamp(a.toolboxThemeRect.X, compactToolboxMargin, w-def.W-compactToolboxMargin)
		cur.Y = value.Clamp(a.toolboxThemeRect.Y, compactToolboxMargin, h-def.H-compactToolboxMargin)
		return cur
	}
	cur := def
	if ov, ok := a.classicOv[slotToolbox]; ok {
		cur = a.anchoredRect(slotToolbox, ov, w, h) // fracToRect unless the slot is window-pinned
		// Move-only: the override's fraction would scale W/H on a resized window, so keep only X/Y and the intrinsic
		// size. Clamped so a moved toolbox can't end up off a smaller window, where it could not be grabbed.
		cur.W, cur.H = def.W, def.H
		cur.X = value.Clamp(cur.X, compactToolboxMargin, w-def.W-compactToolboxMargin)
		cur.Y = value.Clamp(cur.Y, compactToolboxMargin, h-def.H-compactToolboxMargin)
	}
	// Registered inline, not via slotRect, so the editor's drag base gets the normalized W/H.
	if a.classicEdit {
		a.regSlot(slotToolbox, cur, def)
	}
	return cur
}

// editOverToolbox reports the cursor over the strip or the open pieces panel, so the editors leave that press to it.
func (a *App) editOverToolbox(w, h int32) bool {
	c := a.ctx
	// The chips stay live buttons, so suppress a slot move over them. The grip is excluded: it is the drag handle, and
	// a press there must reach the editor. With the themed rect on, the themed editor owns the whole strip as a normal
	// box.
	if !a.toolboxThemeRectOn && !a.panelHidden(panelToolbox) {
		strip := a.compactToolboxStripRect(w, h)
		grip := compactToolboxGripRect(strip)
		if pointIn(c.mouseX, c.mouseY, strip) && !pointIn(c.mouseX, c.mouseY, grip) {
			return true // over a chip, not the grip
		}
	}
	// The pieces panel takes its own input post-courtroom, so a press under it must not grab a box.
	if a.toolboxPinned && a.toolboxPieces && pointIn(c.mouseX, c.mouseY, a.toolboxPiecesRect(w, h)) {
		return true
	}
	return false
}

// toolboxPiecesMinW / toolboxPiecesMinH bound a resized panel: filter box and Close side by side; header, row, footer.
const (
	toolboxPiecesMinW = 200
	toolboxPiecesMinH = toolboxPiecesHeaderH + toolboxPiecesFooterH + toolboxPiecesRowPitch
)

// toolboxPiecesRect is wherever the user last dragged the panel (a floatWin), or bottom-right the first time.
// boxFencesPointer and the draw share it, as a mismatch leaks a click through the panel.
func (a *App) toolboxPiecesRect(w, h int32) sdl.Rect {
	panelH := a.toolboxPiecesContentH() + toolboxPiecesHeaderH + toolboxPiecesFooterH
	if panelH > toolboxPiecesMaxH {
		panelH = toolboxPiecesMaxH
	}
	// Leave the toolbox strip and a small margin below.
	maxH := h - (compactGripH + 2*compactToolboxMargin) - toolboxPiecesTopGap
	panelH = value.Clamp(panelH, toolboxPiecesMinH, maxH)
	pw := toolboxPiecesW
	if pw > w-2*compactToolboxMargin {
		pw = w - 2*compactToolboxMargin
	}
	// Never placed and nothing saved: keep the original corner.
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
	// toolboxPiecesHeaderH holds the title and filter box, outside the scroll body so the filter never scrolls away.
	toolboxPiecesHeaderH = int32(56)
	// toolboxPiecesFooterH is the fixed footer (Close button).
	toolboxPiecesFooterH = int32(34)
	// toolboxPiecesTopGap separates the panel from the toolbox strip.
	toolboxPiecesTopGap = int32(6)
	// toolboxFilterH / toolboxFilterY size and place the filter box in the header; its width is panel.W - 2*pad.
	toolboxFilterH = int32(22)
	toolboxFilterY = int32(28)
)

// toolboxPieceSearch maps each hideable id to its lowered searchable text, built once, so per-row matching allocates
// nothing. The query is lowered only when it changes.
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

// toolboxPieceMatches: an empty query matches everything; both operands are already lowered.
func toolboxPieceMatches(id, queryLower string) bool {
	if queryLower == "" {
		return true
	}
	return strings.Contains(toolboxPieceSearch[id], queryLower)
}

// toolboxPiecesContentH is the unfiltered scroll height: chrome list, control-button grid (new default only), roster.
func (a *App) toolboxPiecesContentH() int32 {
	rows := int32(len(hideablePanels))
	if !a.d.Prefs.Load().LegacyDevTheme {
		btnRows := (int32(len(hideableButtons)) + toolboxPiecesCols - 1) / toolboxPiecesCols
		rows += 1 + btnRows // +1 sub-heading row
	}
	rosterRows := (int32(len(hideableRosterButtons)) + toolboxPiecesCols - 1) / toolboxPiecesCols
	rows += 1 + rosterRows // +1 sub-heading row
	return rows*toolboxPiecesRowPitch + 8
}

// mouseLifelines are a mouse user's routes back to the client's controls; hiding the last one is refused. Three,
// because ctrlSettingsSlot draws only on the classic path, so a themed user has just the other two.
var mouseLifelines = [...]string{panelToolbox, ctrlSettingsSlot, panelMenuBar}

// toggleChromePanel flips a piece from the keyboard and says which way: these binds exist for when you can't see the
// panel. Goes through setPanelHiddenGuarded because a hotkey that strands you is worse than none.
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

// setPanelHiddenGuarded refuses to hide the last mouse lifeline, with a toast. Wholesale hidden-set writes normalize
// the same rule in seedHiddenFromPrefs.
func (a *App) setPanelHiddenGuarded(id string, hide bool) {
	if hide && a.lastMouseLifeline(id) {
		a.warnLine = "Kept: that's the last mouse route back to the client's controls. (Ctrl+F reopens this panel.)"
		a.warnAt = time.Now()
		return
	}
	a.setPanelHidden(id, hide)
}

// lastMouseLifeline reports whether hiding id would hide every lifeline; a non-lifeline is never refused.
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

// drawToolboxPieces paints the per-piece panel, conditional on toolboxPinned && toolboxPieces only, not
// panelHidden(panelToolbox), so a user who hid the toolbox can still reach it by hotkey.
func (a *App) drawToolboxPieces(w, h int32) {
	if !a.toolboxPinned || !a.toolboxPieces {
		return
	}
	// Must match boxFencesPointer, which returns early on !extrasSurfaceLive, or a blocking modal would leak a click
	// here.
	if !a.extrasSurfaceLive() {
		return
	}
	// The palette draws on top but neither is modal, and this panel polls input first and would eat clicks under the
	// palette's rows. boxFencesPointer reads the same flag.
	if a.paletteOpen {
		return
	}
	c := a.ctx
	panel := a.toolboxPiecesRect(w, h)
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+6, "Hide UI pieces", ColText)

	// The title strip above the filter box is the drag handle, so it can't swallow a filter click. piecesPressed is
	// this panel's own press edge: it draws over other surfaces and can't share the frame's click.
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

	// In the header, outside the scroll body, so it can't displace Close. The lowered text is re-derived only on a
	// change.
	filterRect := sdl.Rect{X: panel.X + pad, Y: panel.Y + toolboxFilterY, W: panel.W - 2*pad, H: toolboxFilterH}
	if next, _ := c.TextField("toolboxfilter", filterRect, a.toolboxFilter, "Filter pieces…"); next != a.toolboxFilter {
		a.toolboxFilter = next
		a.toolboxFilterLower = strings.ToLower(strings.TrimSpace(next)) // lowered ONCE per edit, not per frame
	}
	q := a.toolboxFilterLower

	// The legacy/themed row draws fixed inline buttons that ignore the hidden set, so a chip there would do nothing.
	showBtnGrid := !a.d.Prefs.Load().LegacyDevTheme

	body := sdl.Rect{X: panel.X, Y: panel.Y + toolboxPiecesHeaderH,
		W: panel.W, H: panel.H - toolboxPiecesHeaderH - toolboxPiecesFooterH}
	// Height tracks the filtered rows so no dead scroll space; panel size uses the unfiltered content so it doesn't
	// resize.
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
	// Clipped and input-aware so a label tail can't leak a click past the body edge or over the scrollbar.
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
		// Heading only with a visible row, so a filter leaves no stranded heading.
		if toolboxButtonsHaveMatch(q) {
			c.Label(panel.X+pad, y+4, "Control buttons (tick to hide):", ColTextDim)
			y += toolboxPiecesRowPitch
			// vis is the visible-row index, keeping the grid gapless under a filter.
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

	// Close is outside the scroll body and the filter, so a hotkey-opened panel is never stranded.
	footerY := panel.Y + panel.H - btnH - 8
	if c.Button(sdl.Rect{X: panel.X + panel.W - 84 - pad, Y: footerY, W: 84, H: btnH}, "Close") {
		a.toolboxPieces = false
	}
}

// toolboxGridRows is the ceil division shared by the draw and the filtered height; screens.go has its own gridRows.
func toolboxGridRows(n int32) int32 {
	return (n + toolboxPiecesCols - 1) / toolboxPiecesCols
}

// toolboxButtonsHaveMatch / toolboxRosterHaveMatch suppress a heading whose section is filtered out. The registries
// are anonymous structs, so these iterate the concrete slices.
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

// toolboxPanelsVisible / toolboxButtonsVisible / toolboxRosterVisible count the rows each section draws under the
// filter.
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

// toolboxPiecesFilteredContentH is toolboxPiecesContentH over the filtered counts, so the scrollbar matches the rows.
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
