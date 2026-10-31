package ui

// The emote grid's scroll position: a first-visible row, shared by the classic and themed grids.
// Page arrows, counter and number keys are functions of it and stay paged.

// emoteWheelRows is how many grid rows one wheel notch moves.
const emoteWheelRows = 1

// setEmoteGridMetrics stamps the layout the current grid draw resolved and clamps the stored row into range.
// totalCells is the length of the visible emote list, favs-only already applied.
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

// lastEmoteRow is the largest first-visible row that still fills the grid; past it blank rows would show.
func (a *App) lastEmoteRow() int {
	if max := a.emoteTotalRows - a.emoteRowsPerPage; max > 0 {
		return max
	}
	return 0
}

// emoteFirstSlot is the visible-list index of the top-left cell; the draw loop and Alt+1..9 count from here.
func (a *App) emoteFirstSlot() int { return a.emoteRow * a.emoteCols }

// scrollEmoteRows moves the grid by n rows (positive is further down the list) and clamps.
func (a *App) scrollEmoteRows(n int) {
	a.emoteRow += n
	a.clampEmoteRow()
}

// scrollEmotePages moves the grid by whole pages for "<", ">" and the theme's emote_left and emote_right.
func (a *App) scrollEmotePages(n int) { a.scrollEmoteRows(n * a.emoteRowsPerPage) }

// emoteCanScrollUp and emoteCanScrollDown control the page arrows, so a grid at an end offers no dead button.
func (a *App) emoteCanScrollUp() bool   { return a.emoteRow > 0 }
func (a *App) emoteCanScrollDown() bool { return a.emoteRow < a.lastEmoteRow() }

// emotePagesShown and emotePageShown are the page x of y readout; a part-scrolled grid shows the top row's page.
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

// emoteGridScrolls reports whether the visible list overflows one page.
func (a *App) emoteGridScrolls() bool { return a.emoteTotalRows > a.emoteRowsPerPage }

// revealEmoteSlot scrolls the minimum distance that brings a slot on screen; a visible target stays put.
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
