package ui

// The emote grid's SCROLL POSITION — one concern, one file.
//
// The grid used to store a PAGE index and the wheel stepped it whole pages at a
// time, so one notch jumped a screenful and there was no way to see the two rows
// straddling a page boundary at once. The stored position is a first-visible ROW
// now, which makes the wheel a smooth per-row scroll while leaving the page
// arrows, the "page x/y" counter and the number-key row exactly as paged as they
// were — they are all pure functions of (row, cols, rows-per-page) computed here.
//
// Both grids (the classic row in screens.go and the themed one in
// theme_layout.go) drive the same seam, so the two can no longer disagree about
// where the grid is scrolled to; App.emoteRow / emoteCols / emoteRowsPerPage are
// the whole of the state.

// emoteWheelRows is how many grid rows one wheel notch moves. 1 = "smooth per
// row", which is the point of the change; named rather than inlined so a future
// "scroll speed" preference has exactly one place to land.
const emoteWheelRows = 1

// setEmoteGridMetrics stamps the layout the CURRENT grid draw resolved (columns
// and rows-per-page) and clamps the stored row into range for it. Every derived
// answer below reads these, so a grid that reflowed (window resize, a theme
// swap, the icon-spacing slider) cannot leave the number keys or the page label
// describing the previous shape.
//
// totalCells is the length of the VISIBLE emote list (favs-only already applied).
func (a *App) setEmoteGridMetrics(cols, rowsPerPage, totalCells int) {
	if cols < 1 {
		cols = 1
	}
	if rowsPerPage < 1 {
		rowsPerPage = 1
	}
	a.emoteCols, a.emoteRowsPerPage = cols, rowsPerPage
	a.emotePerPage = cols * rowsPerPage // the number-key row still reads this
	a.emoteTotalRows = (totalCells + cols - 1) / cols
	a.clampEmoteRow()
}

// clampEmoteRow keeps the first visible row inside [0, lastEmoteRow].
func (a *App) clampEmoteRow() {
	if max := a.lastEmoteRow(); a.emoteRow > max {
		a.emoteRow = max
	}
	if a.emoteRow < 0 {
		a.emoteRow = 0
	}
}

// lastEmoteRow is the largest first-visible row that still fills the grid — the
// bottom of the scroll range. Scrolling past it would show blank rows below a
// short tail, which is exactly what the page model never did and this must not
// start doing.
func (a *App) lastEmoteRow() int {
	if max := a.emoteTotalRows - a.emoteRowsPerPage; max > 0 {
		return max
	}
	return 0
}

// emoteFirstSlot is the index (into the visible list) of the grid's top-left
// cell. The draw loop starts here and the Alt+1..9 number row counts from here,
// so a half-scrolled grid's digits pick the cells that are actually on screen.
func (a *App) emoteFirstSlot() int { return a.emoteRow * a.emoteCols }

// scrollEmoteRows moves the grid by n rows (positive = further down the list)
// and clamps. The wheel calls it with -delta*emoteWheelRows; the page arrows
// call it with ±rows-per-page, which is how they stay a full page while the
// wheel is a single row.
func (a *App) scrollEmoteRows(n int) {
	a.emoteRow += n
	a.clampEmoteRow()
}

// scrollEmotePages moves the grid by whole pages — the "<" / ">" buttons and the
// theme's emote_left / emote_right.
func (a *App) scrollEmotePages(n int) { a.scrollEmoteRows(n * a.emoteRowsPerPage) }

// emoteCanScrollUp / emoteCanScrollDown gate the page arrows, so a grid already
// at an end does not offer a button that does nothing.
func (a *App) emoteCanScrollUp() bool   { return a.emoteRow > 0 }
func (a *App) emoteCanScrollDown() bool { return a.emoteRow < a.lastEmoteRow() }

// emotePagesShown / emotePageShown are the "page x of y" READOUT. They stay
// page-shaped on purpose: "row 37 of 112" is not what a person wants to read off
// a grid of pictures. With a part-scrolled row the page number is the page the
// TOP row belongs to, which is the same number the arrows would take you to.
func (a *App) emotePagesShown() int {
	if a.emoteRowsPerPage < 1 {
		return 1
	}
	if p := (a.emoteTotalRows + a.emoteRowsPerPage - 1) / a.emoteRowsPerPage; p > 1 {
		return p
	}
	return 1
}

func (a *App) emotePageShown() int {
	if a.emoteRowsPerPage < 1 {
		return 1
	}
	return a.emoteRow/a.emoteRowsPerPage + 1
}

// emoteGridScrolls reports whether the visible list overflows one page — the
// condition the wheel handler, the arrows and the counter all share.
func (a *App) emoteGridScrolls() bool { return a.emoteTotalRows > a.emoteRowsPerPage }

// revealEmoteSlot scrolls the MINIMUM distance that brings a slot on screen.
// Replaces the old `emotePage = slot / perPage` snap, which yanked the grid to a
// page boundary even when the target was already visible — a random pick or a
// Ctrl+emote-cycle step one cell down used to repaint the whole grid.
func (a *App) revealEmoteSlot(slot int) {
	if slot < 0 || a.emoteCols < 1 || a.emoteRowsPerPage < 1 {
		return
	}
	row := slot / a.emoteCols
	switch {
	case row < a.emoteRow:
		a.emoteRow = row
	case row >= a.emoteRow+a.emoteRowsPerPage:
		a.emoteRow = row - a.emoteRowsPerPage + 1
	}
	a.clampEmoteRow()
}
