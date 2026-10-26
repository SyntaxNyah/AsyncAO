package ui

// THE CHARACTER GRID, ON AO2'S OWN ARITHMETIC (#20).
//
// charselectlayout.go established the canvas: the theme's char_select rect fitted
// into the window, with every design rect scaled by the same transform as the
// backdrop art. This file is the other half — where the character buttons actually
// land inside that canvas.
//
// AO2's grid is three lines of integer arithmetic, and they are reproduced here
// verbatim (AO2-Client src/charselect.cpp:160-162, with button_width = 60 from
// src/courtroom.h:561):
//
//	s_button_size = button_width * themeScalingFactor()
//	char_columns  = ((ui_char_buttons->width()  - s_button_size) / (spacing.x + s_button_size)) + 1
//	char_rows     = ((ui_char_buttons->height() - s_button_size) / (spacing.y + s_button_size)) + 1
//
// and put_button_in_place (:246-276) places button n at
// ((size + spacing.x) * col, (size + spacing.y) * row) RELATIVE to ui_char_buttons.
//
// WHERE A CELL ACTUALLY GOES, AND WHY IT IS NOT `origin + col*pitch`.
//
// The backdrop is one texture blitted into lay.area, so SDL stretches it at the
// exact fractional ratio area.W/designW: the slot frame AO2 drew at design x lands
// at area.X + x*area.W/designW. Placing a cell from an accumulated INTEGER pitch
// (`int32(60*cs) + int32(7*cs)`, two separate truncations, multiplied by the column
// index) tracks that only when the ratio is exactly 1. Measured against the stock
// art, the first version of this file drifted 0.00 px at 1920x1080 / 1280x720 /
// 1152x864 — and 7.79 px across / 9.77 px down on a 53 px cell at 944x644, which is
// exactly the window the import auto-snap produces for AceAttorney2x, i.e. issue
// #20's own theme in the shipped default configuration. 800x600 and 1024x600 were
// worse (12.19 / 15.10 px), and a full sweep peaked at 16.14 px.
//
// So every cell is placed by mapping its DESIGN coordinate through the canvas RECT
// once — scaleToCanvas below — never by stepping a rounded pitch. The residual is
// then a single truncation and is bounded below one pixel at every scale, which is
// what charselectgrid_test.go's sweep asserts. The device pitch survives only as
// scroll bookkeeping (one wheel notch, one page), never as a position.
//
// The COUNTS are AO2's formula in DESIGN pixels for the same reason: AO2 gets scale
// invariance free because themeScalingFactor() is an integer, so its rect and its
// button width scale together. Ours is a fraction, and the truncated-scaled version
// of that divide claims 8 columns at 800x600 where the art paints 7 — a whole column
// of characters on blank panel, which is the defect #20 reported.
//
// The formula itself is not a guess and it is not approximate. A dark-run pixel scan of
// the stock 714x668 charselect_background.png finds 7 horizontal runs starting at
// x=226 and 9 vertical runs starting at y=36, each exactly 60 px, pitch 67 — which
// is char_buttons = 226,36,518,596 with spacing 7,7 and button_width 60, to the
// pixel. It was reproduced independently on eight more corpus themes with quite
// different geometry (AAI 10x9, CCWidescreen 26x12, Mobile 5x11, AOHDMini 10x8 at
// spacing 9, AOHDUltra 21x11, 3DS Widescreen 18x12 at spacing 4, final fantasy 1
// 9x7 at spacing 20, and Uminek2x/3x which inherit the stock table and match their
// own art). No theme in the 74-theme reference corpus implies a grid this formula
// cannot produce. AsyncAO drawing its own 64+8 grid from x=pad over that art IS
// issue #20's screenshot.
//
// WHAT WE KEEP THAT AO2 DOES NOT HAVE. AO2 PAGES (char_select_left/right step whole
// pages of char_columns x char_rows buttons); AsyncAO scrolls, and its scrollbar,
// wheel and search all work. Scrolling stays, and it costs almost nothing in
// alignment: the themed grid quantises its scroll offset to whole rows OF THE DESIGN
// LATTICE (charGridPlan.snapScroll), so scrolling by m rows puts grid row r on the
// frame painted for row r-m. The backdrop itself never scrolls, so this is the only
// thing that keeps a scrolled grid on the art at all. Measured at 944x644, where the
// row pitch is a fractional 60.18 px, a resting cell is within 0.89 px of its frame
// and a scrolled one within 1.23 px — a CONSTANT, not something that grows with how
// far you have scrolled, which is exactly what a rounded device pitch could not do.

import (
	"github.com/veandco/go-sdl2/sdl"
)

const (
	// charSelectHeading is the screen's title, kept apart from the server-name
	// concatenation so the cached heading (App.charSelectTitle) has a constant to
	// rebuild from.
	charSelectHeading = "Choose a character"

	// charSelectHeaderH is the height of AsyncAO's own header block on this screen,
	// measured down from the top of the content area: the heading row, then the
	// search field / Characters-Wardrobe tabs / Spectate row under it. The grid starts
	// below it. Previously the bare literal 76 (hard rule 9); it is named now because
	// the themed path also reads it — those widgets are AsyncAO's, they have no AO2
	// counterpart, and the theme's grid has to start clear of them.
	charSelectHeaderH int32 = 76

	// charCellCaptionH is the strip UNDER a CLASSIC char-select cell that carries the
	// character's name. It is part of the classic row pitch (cell + gutter + caption)
	// and has always been there; it is named now because the themed grid has no such
	// strip and the difference has to be legible (hard rule 9). AO2 has no caption
	// either: AOCharButton is a bare 60x60 icon whose name is a TOOLTIP
	// (charselect.cpp:304 setToolTip), which is what the themed cell does instead —
	// a caption would spill into the next row's slot frame at spacing 7.
	charCellCaptionH int32 = 14

	// charGridMinPitchPx floors the divisor in AO2's column/row formula, and the
	// design pitch the cell placement steps by.
	//
	// AO2 divides by (spacing + button_size) with NO guard — compare evidence.cpp:211,
	// which wraps the identical expression in qMax(1, ...) — so a theme shipping
	// char_button_spacing = -60 divides by zero inside stock AO2. In Go that is a
	// panic, i.e. a theme file that crashes the client, so the pitch is floored here.
	// One pixel is the smallest step that still advances the grid: a hostile theme
	// degrades into an unreadably dense grid instead of taking the process down.
	charGridMinPitchPx int32 = 1

	// charCellMinPx is the smallest themed cell worth drawing. Below it the screen
	// falls back to its classic full-window grid, which is the same "sloppy theme
	// data is not a widget" judgement minThemedElementPx makes for every other themed
	// element (theme_layout.go).
	//
	// The floor is a CHARACTER ICON's floor, not a rectangle's. The kit's own
	// smallest character-icon draw sites are the messaging panel's 20 px thread
	// bubbles (msgBubbleIconPx) and its 22 px DM header, so 20 px is the smallest
	// size at which AsyncAO itself is willing to claim a face is recognisable. It
	// was 8 px, which is below the scrollbar's own width: `microsoft surface 4k
	// webao` (a 2592x1719 canvas) at the 640x480 window floor scales to a 14 px cell,
	// and that KEPT the themed grid — 64 px icon textures crushed into 14 px, star
	// and caption both suppressed, under a classic header row that had correctly
	// fallen back. The pre-#20 screen gave that user 64 px captioned cells.
	charCellMinPx int32 = 20

	// charCellChromeMinPx is the smallest cell that still carries AsyncAO's
	// FIXED-SIZE corner chrome: the Wardrobe ★, the download badge, the folder tag
	// and the key badge. None of them scales with the cell — they are glyphs at the
	// client font's size in boxes of 15-20 px — so on a downscaled theme canvas each
	// stops being a badge in the corner and becomes a large fraction of the character
	// art, and its hover+click arm eats the cell's own click along with it.
	//
	// The number is the STAR's: 48 keeps it on every cell down to 0.8x canvas scale
	// (AO2's button_width is 60). The download badge, the folder tag and the key
	// badge are all within a couple of pixels of the same footprint — the badge is
	// 20x18 inset 22 px from the cell's right edge, the star 17-18 px inset 18-20 —
	// so one threshold for all four is honest, and it is what makes them appear and
	// disappear TOGETHER instead of leaving a cell with three of the four.
	//
	// Measured, which is why it is one constant and not four: at the 640x480 window
	// floor with the stock 714x668 table and the shipped chrome band the themed cell
	// is 39 px. The Characters tab suppressed its 18 px star there and drew a 20x18
	// download badge over the bottom-right quadrant, while the Wardrobe tab of the
	// SAME grid drew a 17 px star, a 15 px folder tag and a 16 px key badge —
	// three-quarters of the cell in chrome, each piece consuming the click that was
	// aimed at the character. Nothing becomes unidentifiable when they go: both cells
	// carry the character's name as a tooltip on a themed grid (AO2 does the same,
	// charselect.cpp:304), and the courtroom's own wardrobe panels draw 64 px cells,
	// so they never cross this floor at all.
	charCellChromeMinPx int32 = 48

	// charCellStarPx / charCellStarInsetPx are the Characters cell's ★ box and how far
	// it is inset from the cell's top-right corner. Named because charCellChromeMinPx's
	// justification is stated in terms of them (hard rule 9); the values are unchanged.
	charCellStarPx      int32 = 18
	charCellStarInsetPx int32 = 2

	// charSelectorHaloDesignPx is how far the hover selector overhangs the button on
	// each side, in DESIGN pixels. AO2 sizes ui_selector 62x62 against a 60x60 button
	// and moves it to (x - factor, y - factor) on enterEvent
	// (AO2-Client src/aocharbutton.cpp:10, :68-69) — a one-pixel halo, no more.
	charSelectorHaloDesignPx = 1
)

// cellFitsCornerChrome reports whether a grid cell is big enough to carry AsyncAO's
// fixed-size corner chrome (charCellChromeMinPx).
//
// ONE predicate, called by BOTH cell painters — drawCharCell for the Characters tab
// and drawIniswapCell for the Wardrobe tab. They are the same grid on the same plan
// since #20, and they diverged here: the star was gated on one and not the other, and
// the download badge on neither. A shared predicate is what stops that recurring, and
// it is what charselectgrid_test.go asserts against both painters.
//
// BOTH axes, not just the width: a theme is free to declare a char_buttons rect whose
// scale leaves a cell wide enough and short enough that a 18-20 px badge still swallows
// it vertically.
func cellFitsCornerChrome(cell sdl.Rect) bool {
	return cell.W >= charCellChromeMinPx && cell.H >= charCellChromeMinPx
}

// themeStemCharTaken / themeStemCharSelector are the two char-select overlay images
// AO2's AOCharButton ships as real theme art (aocharbutton.cpp:17 and :23). Both are
// optional: 40 of the 74 corpus themes ship char_taken and 38 ship char_selector, and
// where a theme ships neither the cell keeps AsyncAO's own treatment rather than
// inventing art. Stems (not design keys): they name T1 theme:// textures, and the
// spelling matches the file on disk.
const (
	themeStemCharTaken    = "char_taken"
	themeStemCharSelector = "char_selector"
)

// charGridPlan is the ONE set of cell metrics BOTH tabs of the character-select
// screen lay out on — the Characters grid and the Wardrobe grid, which is the same
// screen (drawWardrobeGrid is called from drawCharSelect) and shared iconCell/iconGap
// with it before this existed. Two independently-derived grids on one screen desync
// on the first resize, so there is exactly one builder and exactly one placement
// method, and both tabs go through them.
//
// A plain value, no pointer: it is passed per cell and must never reach the heap.
type charGridPlan struct {
	// view is the scrolling viewport in window-absolute pixels — the region the grid
	// may paint and hit-test in, pushed as the clip for the cell loop. Classic: the
	// full window width below the header row. Themed: char_buttons intersected with
	// the canvas clip, then advanced to the first WHOLE lattice row below the client
	// header (see charSelectGridPlan).
	view sdl.Rect
	// originX/originY is cell (0,0)'s top-left before the scroll offset. Themed, this
	// is char_buttons' own origin (possibly advanced by whole rows), NEVER a clamped
	// or shifted one — the stock char_buttons overhangs its canvas by 30 px BY DESIGN
	// and shifting it back would walk every column off the art.
	originX, originY int32
	// cell is the button edge: iconCell classic, button_width x cellScale themed.
	cell int32
	// pitchX/pitchY are the cell-to-cell steps, i.e. AO2's (size + spacing). Classic
	// pitchY additionally carries the name caption strip, and classic PLACES cells at
	// this pitch. Themed, it is bookkeeping only — one wheel notch and one page step —
	// because a rounded pitch multiplied by a column index walks off the artwork (see
	// the file header); themed placement goes through the design lattice below.
	pitchX, pitchY int32
	// cols is how many cells fit per row — AO2's char_columns, evaluated in DESIGN
	// pixels on the themed path so the count cannot change with the window size.
	cols int32
	// trackX is the scrollbar lane's left edge. Kept in the plan so both tabs put the
	// bar in the same place.
	trackX int32
	// caption draws the character's name under the cell (classic only; themed cells
	// get AO2's tooltip instead, there being no room between rows).
	caption bool
	// themed selects AO2 behaviour: theme overlay art, row-quantised scrolling, no
	// caption. False means the pre-#20 screen, unchanged.
	themed bool
	// haloPx is the hover selector's per-side overhang at this scale.
	haloPx int32

	// --- THE DESIGN LATTICE (themed only). Every themed cell position is
	// `canvas.<origin> + scaleToCanvas(design coordinate)`, which is where the
	// stretched backdrop puts the slot frame painted for that cell.
	//
	// canvas is lay.area: the rectangle SDL blits the backdrop into.
	canvas sdl.Rect
	// designW/designH are the canvas in design pixels (char_select's w/h).
	designW, designH int32
	// designX/designY are char_buttons' own origin in design pixels. designY is
	// already advanced past any rows skipped under the client header row, so it is
	// always a whole number of design rows below char_buttons' declared top.
	designX, designY int32
	// designPitchX/designPitchY are AO2's (button_width + char_button_spacing) in
	// design pixels, floored at charGridMinPitchPx.
	designPitchX, designPitchY int32
}

// scaleToCanvas maps a DESIGN-space coordinate onto the canvas rect the backdrop art
// is actually blitted into — the one mapping that keeps a cell on the slot frame
// behind it.
//
// Exact integer arithmetic, in int64 so a hostile theme's 100000-px canvas cannot
// overflow the multiply. It is deliberately NOT `float64(design) * scale`: the canvas
// rect is itself a truncation of design*scale, so the art's real ratio is
// area.W/designW, and multiplying by the pre-truncation scale re-introduces a drift
// that grows with the coordinate.
func scaleToCanvas(design, canvasExtent, designExtent int32) int32 {
	if designExtent < 1 {
		return design
	}
	return int32(int64(design) * int64(canvasExtent) / int64(designExtent))
}

// cellAt is put_button_in_place (AO2-Client charselect.cpp:246-276): the n-th
// MATCHING character's cell, window-absolute, at the given scroll offset. n counts
// only the cells that pass the search filter, exactly as AO2 indexes into
// ui_char_button_list_filtered rather than the full roster.
//
// This is the single placement function for both tabs. It is a value method on a
// value receiver with no pointers in it, so it neither allocates nor escapes.
//
// The two arms are the two lattices. Classic is the pre-#20 screen's own arithmetic,
// byte for byte: there is no artwork to line up with, so an integer pitch is exactly
// right. Themed maps the cell's DESIGN coordinate through the canvas rect instead,
// because that is where the stretched backdrop paints its slot frame — see the file
// header for the pitch-accumulation drift this replaces.
func (g charGridPlan) cellAt(n, scroll int32) sdl.Rect {
	col, row := n%g.cols, n/g.cols
	if !g.themed {
		return sdl.Rect{
			X: g.originX + col*g.pitchX,
			Y: g.originY + row*g.pitchY - scroll,
			W: g.cell,
			H: g.cell,
		}
	}
	return sdl.Rect{
		X: g.canvas.X + scaleToCanvas(g.designX+col*g.designPitchX, g.canvas.W, g.designW),
		Y: g.canvas.Y + scaleToCanvas(g.designY+row*g.designPitchY, g.canvas.H, g.designH) - scroll,
		W: g.cell,
		H: g.cell,
	}
}

// rowOffset is how far row `row` sits below row 0 on this plan's lattice — the ONE
// scroll distance that steps the grid by whole rows.
//
// Themed, consecutive rows are NOT a constant number of device pixels apart: the
// artwork's own frames alternate between floor and ceil of the fractional row pitch,
// and the cells follow them. So a scroll offset that is a multiple of some rounded
// pitch drifts off the lattice as it accumulates; an offset taken from here does not.
func (g charGridPlan) rowOffset(row int32) int32 {
	if !g.themed {
		return row * g.pitchY
	}
	return scaleToCanvas(g.designY+row*g.designPitchY, g.canvas.H, g.designH) -
		scaleToCanvas(g.designY, g.canvas.H, g.designH)
}

// contentHeight is the scrollable height of n cells on this plan — what the
// scrollbar measures its thumb against.
func (g charGridPlan) contentHeight(n int32) int32 {
	return g.rowOffset(g.rows(n))
}

// toDesignY converts a device-pixel vertical distance back into design pixels, the
// space where the row lattice IS uniform.
func (g charGridPlan) toDesignY(px int32) int32 {
	if g.canvas.H < 1 {
		return px
	}
	return int32(int64(px) * int64(g.designH) / int64(g.canvas.H))
}

// visible reports whether a cell touches the viewport — the cull that keeps a
// 4000-character roster to one screenful of draws and hit-tests per frame. Rows that
// do not touch it are neither drawn NOR hovered, which is what stops a cell scrolled
// under the header bar from being clickable up there.
func (g charGridPlan) visible(cell sdl.Rect) bool {
	return cell.Y+cell.H > g.view.Y && cell.Y < g.view.Y+g.view.H
}

// rows is how many grid rows n cells occupy.
func (g charGridPlan) rows(n int32) int32 {
	if g.cols < 1 {
		return n
	}
	return (n + g.cols - 1) / g.cols
}

// charSelectGridPlan resolves the cell metrics for a char-select frame: the classic
// full-window grid, or — under a theme canvas that declares a usable char_buttons —
// AO2's grid inside it.
//
// gridTop is the bottom of the client header row (search field, tabs, Spectate). It
// matters on the themed path too: those widgets are AsyncAO's, they have no AO2
// equivalent, and they are drawn BEFORE the grid, so the grid must start below them.
// Rather than clip mid-cell, the viewport advances to the first WHOLE row of
// char_buttons' own lattice at or below the header — the rows that would have been
// half-hidden are simply not used, and every row that IS drawn still sits exactly on
// the slot frame painted behind it. (In a window tall enough for the canvas to centre
// clear of the header — 1080p and up at the stock 714x668 — nothing is skipped at
// all.)
//
// Any themed condition it cannot satisfy falls back to the classic plan rather than
// drawing a broken grid: no canvas, no char_buttons rect, a cell scaled below
// charCellMinPx, or a viewport with no room for one cell.
func (a *App) charSelectGridPlan(w, h, gridTop int32, lay *charSelectLayoutCache) charGridPlan {
	// The classic grid, byte-identical to the pre-#20 screen: cells of iconCell on an
	// iconCell+iconGap pitch from x=pad, rows additionally spaced by the caption
	// strip, scrollbar in the window's right gutter.
	classic := charGridPlan{
		view:    sdl.Rect{X: 0, Y: gridTop, W: w, H: h - gridTop - pad},
		originX: pad,
		originY: gridTop,
		cell:    iconCell,
		pitchX:  iconCell + iconGap,
		pitchY:  iconCell + iconGap + charCellCaptionH,
		cols:    1,
		trackX:  w - pad - scrollBarW,
		caption: true,
	}
	if cols := (w - 2*pad - scrollBarW - scrollBarGap) / (iconCell + iconGap); cols > 1 {
		classic.cols = cols
	}
	if !lay.valid {
		return classic
	}
	// The scaled rect is the GATE only: rect() is where the 11037 hide convention and
	// the minThemedElementPx floor are enforced, and lay.buttonsDesign is populated in
	// lockstep with it. The geometry below is derived from the design rect instead —
	// see the file header for why a pre-scaled rect cannot carry a grid.
	if _, ok := lay.rect(charSelectButtonsKey); !ok {
		return classic
	}
	// ONE uniform factor for the cell and its gutter, because AO2 multiplies
	// button_width and char_button_spacing by the single themeScalingFactor(): a cell
	// stretched per-axis would stop being square and would overflow char_buttons on
	// the tighter axis, silently losing a column.
	cs := lay.cellScale()
	cell := int32(float64(charSelectButtonPx) * cs)
	if cell < charCellMinPx {
		return classic
	}
	pitchX, pitchY := cell+lay.spacingX, cell+lay.spacingY
	if pitchX < charGridMinPitchPx {
		pitchX = charGridMinPitchPx
	}
	if pitchY < charGridMinPitchPx {
		pitchY = charGridMinPitchPx
	}
	// The DESIGN lattice: AO2's (button_width + char_button_spacing), unscaled. This
	// is what cells are placed on and what the counts are computed from; the device
	// pitch above is only the wheel/page step.
	dPitchX, dPitchY := int32(charSelectButtonPx)+lay.spacingDesignX, int32(charSelectButtonPx)+lay.spacingDesignY
	if dPitchX < charGridMinPitchPx {
		dPitchX = charGridMinPitchPx
	}
	if dPitchY < charGridMinPitchPx {
		dPitchY = charGridMinPitchPx
	}
	bd := lay.buttonsDesign
	// AO2-Client charselect.cpp:160, verbatim — and in DESIGN pixels, so the answer is
	// the same at every window size, exactly as AO2's integer themeScalingFactor makes
	// it. A cell wider than char_buttons makes the numerator negative; Go truncates
	// toward zero, so that yields 1 — the same single column C++ integer division
	// gives — and the floor below makes it explicit.
	cols := ((bd.W - charSelectButtonPx) / dPitchX) + 1
	if cols < 1 {
		cols = 1
	}
	// Paint/hit region: char_buttons (Qt clips AOCharButtons to their ui_char_buttons
	// parent) intersected with the canvas clip (Qt clips the canvas at the fixed
	// window edge, which is what keeps the stock table's 30 px overhang honest).
	//
	// Derived from the DESIGN rect through the canvas, not from the pre-scaled twin in
	// lay.r, so the viewport's edges sit on the same lattice the cells do — a viewport
	// a pixel off its own first column would clip that column's edge away.
	grid := sdl.Rect{
		X: lay.area.X + scaleToCanvas(bd.X, lay.area.W, lay.designW),
		Y: lay.area.Y + scaleToCanvas(bd.Y, lay.area.H, lay.designH),
		W: scaleToCanvas(bd.X+bd.W, lay.area.W, lay.designW) - scaleToCanvas(bd.X, lay.area.W, lay.designW),
		H: scaleToCanvas(bd.Y+bd.H, lay.area.H, lay.designH) - scaleToCanvas(bd.Y, lay.area.H, lay.designH),
	}
	view := intersectRect(grid, lay.clip)
	// Then down to the first whole lattice row clear of the client header row. The
	// search runs in DESIGN pixels — the only space where "a whole row" is a fixed
	// number, since on screen the artwork's rows are 67 x area.H/668 apart — and the
	// shortfall is rounded UP into design space first, which is what guarantees the
	// row it picks really is at or below firstUsableY once mapped back.
	//
	// firstUsableY is never above the grid's own top (view.Y is already the
	// intersection with the clip, and gridTop only pushes down), so the advance is
	// never negative and the height below only ever shrinks.
	firstUsableY := view.Y
	if gridTop > firstUsableY {
		firstUsableY = gridTop
	}
	designY := bd.Y
	if firstUsableY > grid.Y && lay.area.H > 0 {
		need := ceilDivInt32(int64(firstUsableY-lay.area.Y)*int64(lay.designH), int64(lay.area.H))
		if extra := need - bd.Y; extra > 0 {
			designY = bd.Y + ((extra+dPitchY-1)/dPitchY)*dPitchY
		}
	}
	originY := lay.area.Y + scaleToCanvas(designY, lay.area.H, lay.designH)
	view.H -= originY - view.Y
	view.Y = originY
	if view.W < cell || view.H < cell {
		return classic // no room for a single cell: the themed grid has nothing to say
	}
	// Scrollbar lane: in the letterbox bar immediately right of the canvas when the
	// fit mode leaves one (Native's usual case), otherwise the window's right gutter,
	// never left of the grid's own right edge.
	trackX := lay.area.X + lay.area.W + scrollBarGap
	if maxX := w - pad - scrollBarW; trackX > maxX {
		trackX = maxX
	}
	if minX := view.X + view.W - scrollBarW; trackX < minX {
		trackX = minX
	}
	halo := int32(charSelectorHaloDesignPx * cs)
	if halo < 1 {
		halo = 1 // a halo rounded away is no halo; AO2's is always at least one pixel
	}
	return charGridPlan{
		view: view,
		// originX/originY are cell (0,0) — mapped through the canvas like every other
		// cell, so `origin` and `cellAt(0,0)` can never disagree.
		originX:      grid.X,
		originY:      originY,
		cell:         cell,
		pitchX:       pitchX,
		pitchY:       pitchY,
		cols:         cols,
		trackX:       trackX,
		themed:       true,
		haloPx:       halo,
		canvas:       lay.area,
		designW:      lay.designW,
		designH:      lay.designH,
		designX:      bd.X,
		designY:      designY,
		designPitchX: dPitchX,
		designPitchY: dPitchY,
	}
}

// ceilDivInt32 is ⌈a/b⌉ for non-negative a and positive b, narrowed to int32. Used
// where a device distance has to be converted into design pixels WITHOUT ever landing
// short — rounding down there would pick a lattice row that is still under the client
// header row it was supposed to clear.
func ceilDivInt32(a, b int64) int32 {
	if b < 1 {
		return int32(a)
	}
	if a < 0 {
		return int32(a / b)
	}
	return int32((a + b - 1) / b)
}

// intersectRect is a ∩ b in plain integer arithmetic. sdl.Rect.Intersect takes BOTH
// operands by pointer, so calling it with a local would move that local to the heap
// on a path the char-select screen rebuilds every time the window changes — the same
// reason charSelectLayoutIn spells its clip out by hand.
func intersectRect(a, b sdl.Rect) sdl.Rect {
	x0, y0 := a.X, a.Y
	if b.X > x0 {
		x0 = b.X
	}
	if b.Y > y0 {
		y0 = b.Y
	}
	x1, y1 := a.X+a.W, a.Y+a.H
	if bx := b.X + b.W; bx < x1 {
		x1 = bx
	}
	if by := b.Y + b.H; by < y1 {
		y1 = by
	}
	if x1 < x0 {
		x1 = x0
	}
	if y1 < y0 {
		y1 = y0
	}
	return sdl.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// charGridScroll runs the shared scroll bookkeeping for one char-select tab: the
// wheel band over the grid, the scrollbar in the plan's lane, and — on the themed
// path — quantisation of the result to whole rows.
//
// WHY QUANTISE. AO2 never has this problem because it pages: a page always starts on
// char_buttons' origin, so every button always sits on the slot frame painted behind
// it. We scroll, and a pixel-scrolled grid would drift off those frames the moment
// the wheel moved — re-creating issue #20's symptom for anyone who scrolls, having
// just fixed it for everyone who does not. Snapping the offset to a multiple of the
// row pitch keeps the lattice exact at every scroll position and makes one wheel
// notch step whole rows, which is also how AO2's arrows feel.
//
// maxRow rounds UP so the last row can always be brought fully into view: snapping it
// down would leave the final row permanently half-clipped, which is worse than
// scrolling a fraction of a row past the content.
func (a *App) charGridScroll(w int32, id string, g charGridPlan, scroll, matches int32) int32 {
	c := a.ctx
	contentH := g.contentHeight(matches)
	// The wheel band. Classic: the whole window width below the header row — the grid
	// is the only thing down there, so the generous band is a kindness. Themed: the
	// grid's OWN viewport, because the char_list name column shares that Y range and
	// WheelIn is single-consumer — a full-width band would swallow every notch aimed
	// at the column (the grid draws first) and the names would never scroll.
	band := sdl.Rect{X: 0, Y: g.view.Y, W: w, H: g.view.H}
	// The wheel STEP. Classic: the kit's pixel notch. Themed: one whole ROW, because
	// the offset is row-snapped below and scrollStepPx (32 px) is less than half the
	// stock 67 px row pitch — a single notch would round straight back to where it
	// started and the themed grid would refuse to scroll at all. One notch, one row is
	// also the closest thing our scrolling grid has to AO2's page arrows.
	step := int32(scrollStepPx)
	if g.themed {
		band = g.view
		step = g.pitchY
	}
	scroll -= c.WheelIn(band) * step
	scroll = c.VScrollbar(id, sdl.Rect{X: g.trackX, Y: g.view.Y, W: scrollBarW, H: g.view.H}, scroll, contentH, g.view.H)
	return g.snapScroll(scroll, contentH)
}

// snapScroll quantises a scroll offset onto this plan's row lattice. The classic
// plan scrolls by pixels and is returned untouched; the themed one snaps.
//
// The snap runs in DESIGN pixels, where the lattice is exactly uniform, and the
// chosen ROW is then mapped back through the canvas — so the offset lands on the
// artwork rather than on a multiple of a rounded device pitch, which would walk off
// it again as it accumulated. A theme whose lattice is degenerate (hostile spacing
// that floors the design pitch away) falls back to the uniform arithmetic.
func (g charGridPlan) snapScroll(scroll, contentH int32) int32 {
	if !g.themed {
		return scroll
	}
	if g.designPitchY < 1 || g.canvas.H < 1 || g.designH < 1 {
		return snapScrollToRows(scroll, contentH, g.view.H, g.pitchY)
	}
	maxRow := int32(0)
	if over := contentH - g.view.H; over > 0 {
		// Into design pixels rounding UP, then into whole rows rounding UP: the last
		// row must be reachable in full, exactly as snapScrollToRows rounds its own
		// maximum up rather than leaving that row permanently half-clipped.
		d := ceilDivInt32(int64(over)*int64(g.designH), int64(g.canvas.H))
		maxRow = (d + g.designPitchY - 1) / g.designPitchY
	}
	row := (g.toDesignY(scroll) + g.designPitchY/2) / g.designPitchY
	if row < 0 {
		row = 0
	}
	if row > maxRow {
		row = maxRow
	}
	return g.rowOffset(row)
}

// snapScrollToRows rounds a scroll offset to the nearest whole row and clamps it to
// the range that keeps content on screen. Split out from charGridScroll so the
// arithmetic — the thing that keeps a scrolled themed grid on the artwork's lattice —
// is testable without a renderer.
func snapScrollToRows(scroll, contentH, viewH, pitchY int32) int32 {
	if pitchY < 1 {
		return scroll
	}
	maxRow := int32(0)
	if over := contentH - viewH; over > 0 {
		maxRow = (over + pitchY - 1) / pitchY
	}
	row := (scroll + pitchY/2) / pitchY
	if row < 0 {
		row = 0
	}
	if row > maxRow {
		row = maxRow
	}
	return row * pitchY
}

// drawCharOverlayArt blits a char-select overlay stem into dst and reports whether
// the active theme actually ships it. Missing art is the common case (roughly half
// the reference corpus ships each of the two), and the caller falls back to whatever
// AsyncAO already drew rather than to invented art.
func (a *App) drawCharOverlayArt(stem string, dst sdl.Rect) bool {
	page, ok := a.themePage(stem)
	if !ok {
		return false
	}
	c := a.ctx
	// Shared scratch, not &dst: escape analysis is static, so a cgo pointer arg taken
	// from a parameter heap-allocates on EVERY call — and this one runs per visible
	// taken cell per frame. SDL copies the rect during the call (ui.go cgoRect
	// contract) and never retains the pointer.
	c.cgoRect = dst
	_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
	return true
}

// drawCharSelectorHalo paints the theme's char_selector around the hovered cell.
//
// AO2 makes the selector a SIBLING of the buttons, not a child, and raise()s it on
// enterEvent (aocharbutton.cpp:20, :68-71) so it draws above every button including
// its neighbours. Immediate mode has no z-order, so the equivalent is to paint it
// after the whole cell loop — which is where this is called from. A theme that ships
// no char_selector gets nothing, which is the pre-#20 behaviour.
func (a *App) drawCharSelectorHalo(cell sdl.Rect, halo int32) {
	a.drawCharOverlayArt(themeStemCharSelector, charSelectorRect(cell, halo))
}

// charSelectorRect is the selector's geometry: the cell grown by halo on every side,
// which at design scale is AO2's 62x62 selector over a 60x60 button, moved to
// (x - 1, y - 1) (AO2-Client aocharbutton.cpp:10, :68-69).
func charSelectorRect(cell sdl.Rect, halo int32) sdl.Rect {
	return sdl.Rect{
		X: cell.X - halo,
		Y: cell.Y - halo,
		W: cell.W + 2*halo,
		H: cell.H + 2*halo,
	}
}
