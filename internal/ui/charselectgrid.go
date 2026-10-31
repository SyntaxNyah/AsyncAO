package ui

// The character grid uses AO2's arithmetic (charselect.cpp:160-162, button_width = 60 from courtroom.h:561):
// s_button_size = button_width * themeScalingFactor()
// char_columns  = ((ui_char_buttons->width()  - s_button_size) / (spacing.x + s_button_size)) + 1
// char_rows     = ((ui_char_buttons->height() - s_button_size) / (spacing.y + s_button_size)) + 1
// Each cell maps its design coordinate through the canvas rect once (scaleToCanvas), never by stepping a rounded pitch:
// the backdrop stretches at area.W/designW, and an accumulated integer pitch drifts off the art at fractional scales.
// Counts use design pixels too, since a truncated scaled divide can claim a column the art doesn't paint.
// AO2 pages; we scroll, snapping to whole rows of the design lattice so a scrolled grid stays on the art.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// charSelectHeading is kept apart from the server name so the cached heading has a constant to rebuild from.
	charSelectHeading = "Choose a character"

	// charSelectHeaderH is the height of AsyncAO's own header block; the themed grid must start clear of these widgets
	// too.
	charSelectHeaderH int32 = 76

	// charCellCaptionH is the name strip under a classic cell. AO2's name is a tooltip instead (charselect.cpp:304
	// setToolTip), as is the themed cell's: a caption would spill into the next row's frame at spacing 7.
	charCellCaptionH int32 = 14

	// charGridMinPitchPx floors the divisor in AO2's column/row formula. AO2 does not guard it (compare
	// evidence.cpp:211), so char_button_spacing = -60 divides by zero there; in Go a theme file must not panic the
	// client.
	charGridMinPitchPx int32 = 1

	// charCellMinPx is the smallest themed cell worth drawing; below it the screen uses its classic grid. 20 px matches
	// the smallest character icons we draw (msgBubbleIconPx), the size at which a face is still recognisable.
	charCellMinPx int32 = 20

	// charCellChromeMinPx is the smallest cell that keeps the fixed-size corner chrome: star, download badge, folder
	// tag, key badge. They don't scale, so on small cells they cover the art and their click areas eat the cell's own
	// click; one threshold makes all four appear together. 48 keeps the star down to 0.8x (button_width 60). Names stay
	// as tooltips (charselect.cpp:304).
	charCellChromeMinPx int32 = 48

	// charCellStarPx / charCellStarInsetPx are the Characters cell's star box and its inset from the top-right corner.
	charCellStarPx      int32 = 18
	charCellStarInsetPx int32 = 2

	// charSelectorHaloDesignPx is the hover selector's overhang per side in design pixels: AO2 sizes ui_selector 62x62
	// against a 60x60 button (aocharbutton.cpp:10, :68-69).
	charSelectorHaloDesignPx = 1
)

// cellFitsCornerChrome reports whether a cell is big enough for the corner chrome (charCellChromeMinPx), on both axes.
// It is the one predicate shared by drawCharCell and drawIniswapCell.
func cellFitsCornerChrome(cell sdl.Rect) bool {
	return cell.W >= charCellChromeMinPx && cell.H >= charCellChromeMinPx
}

// themeStemCharTaken / themeStemCharSelector are AOCharButton's optional overlay images (aocharbutton.cpp:17 and :23).
// A theme shipping neither keeps AsyncAO's own treatment. Stems name T1 theme:// textures, spelled as the file on disk.
const (
	themeStemCharTaken    = "char_taken"
	themeStemCharSelector = "char_selector"
)

// charGridPlan is the one set of cell metrics both tabs (Characters and Wardrobe) lay out on, so they cannot desync.
// A plain value: it is passed per cell and must never reach the heap.
type charGridPlan struct {
	// view is the scrolling viewport in window pixels, the clip for the cell loop. Themed: char_buttons intersected
	// with the canvas clip, advanced to the first whole lattice row below the client header.
	view sdl.Rect
	// originX/originY is cell (0,0) before scroll. Themed, it is never clamped or shifted: the stock char_buttons
	// overhangs its canvas by 30 px by design, and shifting it would walk every column off the art.
	originX, originY int32
	// cell is the button edge: iconCell classic, button_width x cellScale themed.
	cell int32
	// pitchX/pitchY are AO2's (size + spacing). Classic pitchY adds the caption and places cells;
	// themed it is only the wheel and page step, because a rounded pitch times a column index walks off the art.
	pitchX, pitchY int32
	// cols is AO2's char_columns, evaluated in design pixels on the themed path so it cannot change with window size.
	cols int32
	// trackX is the scrollbar lane's left edge, so both tabs put the bar in the same place.
	trackX int32
	// caption draws the name under the cell (classic only; themed cells get AO2's tooltip).
	caption bool
	// themed selects AO2 behaviour: theme overlay art, row-quantised scrolling, no caption.
	themed bool
	// haloPx is the hover selector's per-side overhang at this scale.
	haloPx int32

	// The design lattice (themed only): each cell is `canvas.<origin> + scaleToCanvas(design coordinate)`.
	// canvas is lay.area, the rectangle SDL blits the backdrop into.
	canvas sdl.Rect
	// designW/designH are the canvas in design pixels (char_select's w/h).
	designW, designH int32
	// designX/designY are char_buttons' origin in design pixels; designY is advanced past rows skipped under the
	// header.
	designX, designY int32
	// designPitchX/designPitchY are AO2's (button_width + char_button_spacing), floored at charGridMinPitchPx.
	designPitchX, designPitchY int32
}

// scaleToCanvas maps a design coordinate onto the canvas rect the backdrop is blitted into. Integer int64 arithmetic:
// the canvas is a truncation of design*scale, so a float scale would drift with the coordinate.
func scaleToCanvas(design, canvasExtent, designExtent int32) int32 {
	if designExtent < 1 {
		return design
	}
	return int32(int64(design) * int64(canvasExtent) / int64(designExtent))
}

// cellAt is put_button_in_place (charselect.cpp:246-276): the n-th matching cell at the given scroll.
// n counts only cells passing the search filter, as AO2 indexes ui_char_button_list_filtered.
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

// rowOffset is how far `row` sits below row 0 on this plan's lattice.
// Themed rows alternate between floor and ceil of the fractional pitch, so a multiple of a rounded pitch would drift.
func (g charGridPlan) rowOffset(row int32) int32 {
	if !g.themed {
		return row * g.pitchY
	}
	return scaleToCanvas(g.designY+row*g.designPitchY, g.canvas.H, g.designH) -
		scaleToCanvas(g.designY, g.canvas.H, g.designH)
}

// contentHeight is the scrollable height of n cells, which the scrollbar measures its thumb against.
func (g charGridPlan) contentHeight(n int32) int32 {
	return g.rowOffset(g.rows(n))
}

// toDesignY converts a device-pixel distance to design pixels, where the row lattice is uniform.
func (g charGridPlan) toDesignY(px int32) int32 {
	if g.canvas.H < 1 {
		return px
	}
	return int32(int64(px) * int64(g.designH) / int64(g.canvas.H))
}

// visible culls cells outside the viewport; rows not touching it are neither drawn nor hovered, so a cell under the
// header is not clickable.
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

// charSelectGridPlan resolves the cell metrics for a frame: the classic grid, or AO2's grid inside a usable theme
// canvas. The header widgets draw first, so the themed viewport advances to the first whole lattice row at or below
// gridTop. Any unmet themed condition falls back to the classic plan rather than drawing a broken grid.
func (a *App) charSelectGridPlan(w, h, gridTop int32, lay *charSelectLayoutCache) charGridPlan {
	// The classic grid: iconCell cells from x=pad, rows also spaced by the caption, scrollbar in the right gutter.
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
	// The scaled rect is only the check: rect() enforces the 11037 hide convention and the size floor.
	if _, ok := lay.rect(charSelectButtonsKey); !ok {
		return classic
	}
	// One uniform factor, as AO2 uses one themeScalingFactor(): a per-axis cell stops being square and loses a column.
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
	// AO2's design pitch, unscaled: cells and counts use it; the device pitch above is only the wheel/page step.
	dPitchX, dPitchY := int32(charSelectButtonPx)+lay.spacingDesignX, int32(charSelectButtonPx)+lay.spacingDesignY
	if dPitchX < charGridMinPitchPx {
		dPitchX = charGridMinPitchPx
	}
	if dPitchY < charGridMinPitchPx {
		dPitchY = charGridMinPitchPx
	}
	bd := lay.buttonsDesign
	// charselect.cpp:160, verbatim, in design pixels. A cell wider than char_buttons makes the numerator negative; Go
	// truncates to zero, giving 1 as in C++.
	cols := ((bd.W - charSelectButtonPx) / dPitchX) + 1
	if cols < 1 {
		cols = 1
	}
	// Paint/hit region: Qt clips buttons to ui_char_buttons and the canvas to the window edge (the stock 30 px
	// overhang). Derived from the design rect, so the viewport's edges are on the cells' lattice and don't clip the
	// first column.
	grid := sdl.Rect{
		X: lay.area.X + scaleToCanvas(bd.X, lay.area.W, lay.designW),
		Y: lay.area.Y + scaleToCanvas(bd.Y, lay.area.H, lay.designH),
		W: scaleToCanvas(bd.X+bd.W, lay.area.W, lay.designW) - scaleToCanvas(bd.X, lay.area.W, lay.designW),
		H: scaleToCanvas(bd.Y+bd.H, lay.area.H, lay.designH) - scaleToCanvas(bd.Y, lay.area.H, lay.designH),
	}
	view := intersectRect(grid, lay.clip)
	// Down to the first whole row clear of the header, in design pixels, rounding the shortfall up so the row is at or
	// below firstUsableY. firstUsableY is never above the grid's top, so the advance is never negative and the height
	// only shrinks.
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
	// Scrollbar lane: in the letterbox bar right of the canvas if there is one, else the right gutter, never left of
	// the grid's edge.
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
		// Mapped through the canvas like every cell, so `origin` and `cellAt(0,0)` agree.
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

// ceilDivInt32 is ceil(a/b) for non-negative a and positive b, narrowed to int32.
// Rounding down when converting to design pixels could pick a row still under the header.
func ceilDivInt32(a, b int64) int32 {
	if b < 1 {
		return int32(a)
	}
	if a < 0 {
		return int32(a / b)
	}
	return int32((a + b - 1) / b)
}

// intersectRect is a ∩ b in plain integers: sdl.Rect.Intersect takes pointers, which would heap-allocate a local.
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

// charGridScroll runs one tab's scroll: wheel band, scrollbar, and on the themed path snapping to whole rows.
// AO2 pages, so buttons always sit on frames; a pixel-scrolled grid would drift off them.
// maxRow rounds up so the last row can always come fully into view.
func (a *App) charGridScroll(w int32, id string, g charGridPlan, scroll, matches int32) int32 {
	c := a.ctx
	contentH := g.contentHeight(matches)
	// Themed band is the grid's own viewport: the char_list name column shares that Y range, WheelIn is
	// single-consumer, and the grid draws first, so a full-width band would swallow every notch aimed at the column.
	band := sdl.Rect{X: 0, Y: g.view.Y, W: w, H: g.view.H}
	// Themed step is one row: scrollStepPx (32 px) is under half the stock 67 px pitch, so a notch would snap back to
	// its start.
	step := int32(scrollStepPx)
	if g.themed {
		band = g.view
		step = g.pitchY
	}
	scroll -= c.WheelIn(band) * step
	scroll = c.VScrollbar(id, sdl.Rect{X: g.trackX, Y: g.view.Y, W: scrollBarW, H: g.view.H}, scroll, contentH, g.view.H)
	return g.snapScroll(scroll, contentH)
}

// snapScroll quantises a scroll offset onto the row lattice; classic plans are returned untouched.
// It snaps in design pixels and maps the row back to the art; a degenerate lattice uses the uniform arithmetic.
func (g charGridPlan) snapScroll(scroll, contentH int32) int32 {
	if !g.themed {
		return scroll
	}
	if g.designPitchY < 1 || g.canvas.H < 1 || g.designH < 1 {
		return snapScrollToRows(scroll, contentH, g.view.H, g.pitchY)
	}
	maxRow := int32(0)
	if over := contentH - g.view.H; over > 0 {
		// Round up twice so the last row is reachable in full, as snapScrollToRows does.
		d := ceilDivInt32(int64(over)*int64(g.designH), int64(g.canvas.H))
		maxRow = (d + g.designPitchY - 1) / g.designPitchY
	}
	row := (g.toDesignY(scroll) + g.designPitchY/2) / g.designPitchY
	row = value.Clamp(row, 0, maxRow)
	return g.rowOffset(row)
}

// snapScrollToRows rounds a scroll offset to the nearest whole row and clamps it to keep content on screen.
func snapScrollToRows(scroll, contentH, viewH, pitchY int32) int32 {
	if pitchY < 1 {
		return scroll
	}
	maxRow := int32(0)
	if over := contentH - viewH; over > 0 {
		maxRow = (over + pitchY - 1) / pitchY
	}
	row := (scroll + pitchY/2) / pitchY
	row = value.Clamp(row, 0, maxRow)
	return row * pitchY
}

// drawCharOverlayArt blits a char-select overlay stem into dst and reports whether the theme ships it.
func (a *App) drawCharOverlayArt(stem string, dst sdl.Rect) bool {
	page, ok := a.themePage(stem)
	if !ok {
		return false
	}
	c := a.ctx
	// Shared scratch, not &dst: a cgo pointer arg from a parameter heap-allocates per call, and this runs per taken
	// cell per frame.
	c.cgoRect = dst
	_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
	return true
}

// drawCharSelectorHalo paints the theme's char_selector around the hovered cell. AO2 raise()s it above every button
// (aocharbutton.cpp:20, :68-71); immediate mode has no z-order, so it must be called after the cell loop.
func (a *App) drawCharSelectorHalo(cell sdl.Rect, halo int32) {
	a.drawCharOverlayArt(themeStemCharSelector, charSelectorRect(cell, halo))
}

// charSelectorRect grows the cell by halo: at design scale, AO2's 62x62 selector at (x - 1, y - 1)
// (aocharbutton.cpp:10).
func charSelectorRect(cell sdl.Rect, halo int32) sdl.Rect {
	return sdl.Rect{
		X: cell.X - halo,
		Y: cell.Y - halo,
		W: cell.W + 2*halo,
		H: cell.H + 2*halo,
	}
}
