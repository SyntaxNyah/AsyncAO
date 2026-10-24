package ui

// Gates for the emote grid's scroll position (emotescroll.go). The grid stored a
// PAGE index and the wheel stepped whole pages, so one notch jumped a screenful
// and the two rows straddling a page boundary could never be seen together. The
// stored position is a first-visible ROW now, and every paged answer — the arrows,
// the "page x/y" readout, the Alt+1..9 row — is derived from it.

import "testing"

// gridApp is a grid of `total` cells laid out cols × rowsPerPage.
func gridApp(cols, rowsPerPage, total int) *App {
	a := &App{env: env{}}
	a.setEmoteGridMetrics(cols, rowsPerPage, total)
	return a
}

// TestWheelScrollsOneRowNotOnePage is the reported behaviour change.
func TestWheelScrollsOneRowNotOnePage(t *testing.T) {
	a := gridApp(5, 3, 100) // 20 rows, 3 on screen
	if emoteWheelRows != 1 {
		t.Fatalf("emoteWheelRows = %d — the wheel is supposed to be a SMOOTH per-row scroll", emoteWheelRows)
	}
	a.scrollEmoteRows(emoteWheelRows)
	if a.emotepick.emoteRow != 1 {
		t.Errorf("one wheel notch moved to row %d, want 1", a.emotepick.emoteRow)
	}
	// The arrows stay a whole page, which is the split the change is FOR.
	a.emotepick.emoteRow = 0
	a.scrollEmotePages(1)
	if a.emotepick.emoteRow != 3 {
		t.Errorf("one page arrow moved to row %d, want 3 (rowsPerPage)", a.emotepick.emoteRow)
	}
}

// TestScrollStopsWithAFullGrid pins the bottom of the range: scrolling past
// lastEmoteRow would show blank rows under a short tail, which the page model
// never did and this must not start doing.
func TestScrollStopsWithAFullGrid(t *testing.T) {
	a := gridApp(5, 3, 100) // 20 rows, 3 visible → last first-row is 17
	a.scrollEmoteRows(999)
	if a.emotepick.emoteRow != 17 {
		t.Errorf("scrolled to row %d, want the clamp at 17", a.emotepick.emoteRow)
	}
	a.scrollEmoteRows(-999)
	if a.emotepick.emoteRow != 0 {
		t.Errorf("scrolled up to row %d, want 0", a.emotepick.emoteRow)
	}
	// A list that fits has nowhere to go, and neither arrow is offered.
	b := gridApp(5, 3, 10) // 2 rows in a 3-row grid
	b.scrollEmoteRows(5)
	if b.emotepick.emoteRow != 0 || b.emoteCanScrollDown() || b.emoteCanScrollUp() || b.emoteGridScrolls() {
		t.Errorf("a grid that fits scrolled (row %d) or offered an arrow", b.emotepick.emoteRow)
	}
}

// TestReflowClampsTheStoredRow pins why the metrics are stamped by the DRAW: a
// window resize, a theme swap or the icon-spacing slider reflows the grid, and a
// row index left over from the old shape would put the number keys and the page
// label on cells that are not there.
func TestReflowClampsTheStoredRow(t *testing.T) {
	a := gridApp(5, 3, 100)
	a.emotepick.emoteRow = 17
	a.setEmoteGridMetrics(10, 3, 100) // twice as wide: 10 rows, last first-row is 7
	if a.emotepick.emoteRow != 7 {
		t.Errorf("after a reflow the row is %d, want the new clamp 7", a.emotepick.emoteRow)
	}
	if a.emotepick.emotePerPage != 30 {
		t.Errorf("emotePerPage = %d, want cols*rowsPerPage = 30", a.emotepick.emotePerPage)
	}
	// Degenerate metrics (a panel too small to lay out) must not divide by zero.
	b := gridApp(0, 0, 7)
	if b.emotepick.emoteCols != 1 || b.emotepick.emoteRowsPerPage != 1 {
		t.Errorf("degenerate metrics were not floored: cols=%d rows=%d", b.emotepick.emoteCols, b.emotepick.emoteRowsPerPage)
	}
}

// TestNumberRowFollowsTheVisibleGrid pins the Alt+1..9 mapping: the digits pick
// the cells actually ON SCREEN, so a half-scrolled grid does not send you to the
// cells of the page you would have been on under the old model.
func TestNumberRowFollowsTheVisibleGrid(t *testing.T) {
	a := gridApp(5, 3, 100)
	if got := a.emoteFirstSlot(); got != 0 {
		t.Errorf("the top-left slot is %d, want 0", got)
	}
	a.scrollEmoteRows(4) // a position no page boundary lands on
	if got := a.emoteFirstSlot(); got != 20 {
		t.Errorf("after 4 rows the top-left slot is %d, want 20", got)
	}
}

// TestPageReadoutStaysPageShaped pins the deliberate asymmetry: the wheel is by
// row, but "row 37 of 112" is not what a person wants to read off a grid of
// pictures, so the counter still names the page the TOP row belongs to.
func TestPageReadoutStaysPageShaped(t *testing.T) {
	a := gridApp(5, 3, 100) // 20 rows → 7 pages
	if got := a.emotePagesShown(); got != 7 {
		t.Errorf("pages = %d, want 7", got)
	}
	for _, tc := range []struct{ row, page int }{{0, 1}, {2, 1}, {3, 2}, {4, 2}, {17, 6}} {
		a.emotepick.emoteRow = tc.row
		if got := a.emotePageShown(); got != tc.page {
			t.Errorf("row %d reads as page %d, want %d", tc.row, got, tc.page)
		}
	}
	// A grid that fits still reads "1 of 1", never "0".
	b := gridApp(5, 3, 4)
	if b.emotePagesShown() != 1 || b.emotePageShown() != 1 {
		t.Errorf("a single-page grid reads %d of %d, want 1 of 1", b.emotePageShown(), b.emotePagesShown())
	}
}

// TestRevealScrollsTheMinimumDistance pins the replacement for the old
// `page = slot / perPage` snap, which yanked the grid to a page boundary even when
// the target was already visible — a random pick, or a Ctrl+emote-cycle step one
// cell down, repainted the whole grid.
func TestRevealScrollsTheMinimumDistance(t *testing.T) {
	a := gridApp(5, 3, 100)
	a.emotepick.emoteRow = 6 // showing rows 6,7,8
	a.revealEmoteSlot(35)
	if a.emotepick.emoteRow != 6 {
		t.Errorf("revealing an already-visible slot moved the grid to row %d — it must not move at all", a.emotepick.emoteRow)
	}
	a.revealEmoteSlot(20) // row 4, above the view
	if a.emotepick.emoteRow != 4 {
		t.Errorf("revealing above the view landed on row %d, want 4 (scroll up to it)", a.emotepick.emoteRow)
	}
	a.revealEmoteSlot(60) // row 12, below the view
	if a.emotepick.emoteRow != 10 {
		t.Errorf("revealing below the view landed on row %d, want 10 (the target as the LAST visible row)", a.emotepick.emoteRow)
	}
	a.revealEmoteSlot(-1) // a nothing-selected index must be inert
	if a.emotepick.emoteRow != 10 {
		t.Errorf("a negative slot moved the grid to row %d", a.emotepick.emoteRow)
	}
}
