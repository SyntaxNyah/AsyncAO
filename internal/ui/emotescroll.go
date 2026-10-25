package ui

// The emote grid's SCROLL POSITION — one concern, one file.

// emoteWheelRows is how many grid rows one wheel notch moves. 1 = "smooth per
// row", which is the point of the change; named rather than inlined so a future
// "scroll speed" preference has exactly one place to land.
const emoteWheelRows = 1

// setEmoteGridMetrics stamps the layout the CURRENT grid draw resolved (columns
// and rows-per-page) and clamps the stored row into range for it. Every derived
// answer below reads these, so a grid that reflowed (window resize, a theme
// swap, the icon-spacing slider) cannot leave the number keys or the page label
// describing the previous shape.
func (a *App) setEmoteGridMetrics(cols, rowsPerPage, totalCells int) {
	if cols < 1 {
		cols = 1
	}
	if rowsPerPage < 1 {
		rowsPerPage = 1
	}
	a.emotepick.emoteCols, a.emotepick.emoteRowsPerPage = cols, rowsPerPage
	a.emotepick.emotePerPage = cols * rowsPerPage // the number-key row still reads this
	a.emotepick.emoteTotalRows = (totalCells + cols - 1) / cols
	a.clampEmoteRow()
}

// clampEmoteRow keeps the first visible row inside [0, lastEmoteRow].
func (a *App) clampEmoteRow() {
	if max := a.lastEmoteRow(); a.emotepick.emoteRow > max {
		a.emotepick.emoteRow = max
	}
	if a.emotepick.emoteRow < 0 {
		a.emotepick.emoteRow = 0
	}
}

// lastEmoteRow is the largest first-visible row that still fills the grid — the
// bottom of the scroll range. Scrolling past it would show blank rows below a
// short tail, which is exactly what the page model never did and this must not
// start doing.
func (a *App) lastEmoteRow() int {
	if max := a.emotepick.emoteTotalRows - a.emotepick.emoteRowsPerPage; max > 0 {
		return max
	}
	return 0
}

// emoteFirstSlot is the index (into the visible list) of the grid's top-left
// cell. The draw loop starts here and the Alt+1..9 number row counts from here,
// so a half-scrolled grid's digits pick the cells that are actually on screen.
func (a *App) emoteFirstSlot() int { return a.emotepick.emoteRow * a.emotepick.emoteCols }

// scrollEmoteRows moves the grid by n rows (positive = further down the list)
func (a *App) scrollEmoteRows(n int) {
	a.emotepick.emoteRow += n
	a.clampEmoteRow()
}

// scrollEmotePages moves the grid by whole pages — the "<" / ">" buttons and the
// theme's emote_left / emote_right.
func (a *App) scrollEmotePages(n int) { a.scrollEmoteRows(n * a.emotepick.emoteRowsPerPage) }

// emoteCanScrollUp / emoteCanScrollDown gate the page arrows, so a grid already
// at an end does not offer a button that does nothing.
func (a *App) emoteCanScrollUp() bool   { return a.emotepick.emoteRow > 0 }
func (a *App) emoteCanScrollDown() bool { return a.emotepick.emoteRow < a.lastEmoteRow() }

// emotePagesShown / emotePageShown are the "page x of y" READOUT. They stay
// page-shaped on purpose: "row 37 of 112" is not what a person wants to read off
// a grid of pictures. With a part-scrolled row the page number is the page the
// TOP row belongs to, which is the same number the arrows would take you to.
func (a *App) emotePagesShown() int {
	if a.emotepick.emoteRowsPerPage < 1 {
		return 1
	}
	if p := (a.emotepick.emoteTotalRows + a.emotepick.emoteRowsPerPage - 1) / a.emotepick.emoteRowsPerPage; p > 1 {
		return p
	}
	return 1
}

func (a *App) emotePageShown() int {
	if a.emotepick.emoteRowsPerPage < 1 {
		return 1
	}
	return a.emotepick.emoteRow/a.emotepick.emoteRowsPerPage + 1
}

// emoteGridScrolls reports whether the visible list overflows one page — the
// condition the wheel handler, the arrows and the counter all share.
func (a *App) emoteGridScrolls() bool {
	return a.emotepick.emoteTotalRows > a.emotepick.emoteRowsPerPage
}

// revealEmoteSlot scrolls the MINIMUM distance that brings a slot on screen.
func (a *App) revealEmoteSlot(slot int) {
	if slot < 0 || a.emotepick.emoteCols < 1 || a.emotepick.emoteRowsPerPage < 1 {
		return
	}
	row := slot / a.emotepick.emoteCols
	switch {
	case row < a.emotepick.emoteRow:
		a.emotepick.emoteRow = row
	case row >= a.emotepick.emoteRow+a.emotepick.emoteRowsPerPage:
		a.emotepick.emoteRow = row - a.emotepick.emoteRowsPerPage + 1
	}
	a.clampEmoteRow()
}
