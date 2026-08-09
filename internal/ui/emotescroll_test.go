package ui

// Gates for the emote grid's scroll position (emotescroll.go). The grid stored a
// PAGE index and the wheel stepped whole pages, so one notch jumped a screenful
// and the two rows straddling a page boundary could never be seen together. The
// stored position is a first-visible ROW now, and every paged answer — the arrows,
// the "page x/y" readout, the Alt+1..9 row — is derived from it.

import "testing"

// gridApp is a grid of `total` cells laid out cols × rowsPerPage.
func gridApp(cols, rowsPerPage, total int) *App {
	a := &App{}
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
	if a.emoteRow != 1 {
		t.Errorf("one wheel notch moved to row %d, want 1", a.emoteRow)
	}
	// The arrows stay a whole page, which is the split the change is FOR.
	a.emoteRow = 0
	a.scrollEmotePages(1)
	if a.emoteRow != 3 {
		t.Errorf("one page arrow moved to row %d, want 3 (rowsPerPage)", a.emoteRow)
	}
}

// TestScrollStopsWithAFullGrid pins the bottom of the range: scrolling past
// lastEmoteRow would show blank rows under a short tail, which the page model
// never did and this must not start doing.
func TestScrollStopsWithAFullGrid(t *testing.T) {
	a := gridApp(5, 3, 100) // 20 rows, 3 visible → last first-row is 17
	a.scrollEmoteRows(999)
	if a.emoteRow != 17 {
		t.Errorf("scrolled to row %d, want the clamp at 17", a.emoteRow)
	}
	a.scrollEmoteRows(-999)
	if a.emoteRow != 0 {
		t.Errorf("scrolled up to row %d, want 0", a.emoteRow)
	}
	// A list that fits has nowhere to go, and neither arrow is offered.
	b := gridApp(5, 3, 10) // 2 rows in a 3-row grid
	b.scrollEmoteRows(5)
	if b.emoteRow != 0 || b.emoteCanScrollDown() || b.emoteCanScrollUp() || b.emoteGridScrolls() {
		t.Errorf("a grid that fits scrolled (row %d) or offered an arrow", b.emoteRow)
	}
}

// TestReflowClampsTheStoredRow pins why the metrics are stamped by the DRAW: a
// window resize, a theme swap or the icon-spacing slider reflows the grid, and a
// row index left over from the old shape would put the number keys and the page
// label on cells that are not there.
func TestReflowClampsTheStoredRow(t *testing.T) {
	a := gridApp(5, 3, 100)
	a.emoteRow = 17
	a.setEmoteGridMetrics(10, 3, 100) // twice as wide: 10 rows, last first-row is 7
	if a.emoteRow != 7 {
		t.Errorf("after a reflow the row is %d, want the new clamp 7", a.emoteRow)
	}
	if a.emotePerPage != 30 {
		t.Errorf("emotePerPage = %d, want cols*rowsPerPage = 30", a.emotePerPage)
	}
	// Degenerate metrics (a panel too small to lay out) must not divide by zero.
	b := gridApp(0, 0, 7)
	if b.emoteCols != 1 || b.emoteRowsPerPage != 1 {
		t.Errorf("degenerate metrics were not floored: cols=%d rows=%d", b.emoteCols, b.emoteRowsPerPage)
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
		a.emoteRow = tc.row
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
	a.emoteRow = 6 // showing rows 6,7,8
	a.revealEmoteSlot(35)
	if a.emoteRow != 6 {
		t.Errorf("revealing an already-visible slot moved the grid to row %d — it must not move at all", a.emoteRow)
	}
	a.revealEmoteSlot(20) // row 4, above the view
	if a.emoteRow != 4 {
		t.Errorf("revealing above the view landed on row %d, want 4 (scroll up to it)", a.emoteRow)
	}
	a.revealEmoteSlot(60) // row 12, below the view
	if a.emoteRow != 10 {
		t.Errorf("revealing below the view landed on row %d, want 10 (the target as the LAST visible row)", a.emoteRow)
	}
	a.revealEmoteSlot(-1) // a nothing-selected index must be inert
	if a.emoteRow != 10 {
		t.Errorf("a negative slot moved the grid to row %d", a.emoteRow)
	}
}

// TestBothGridsDriveTheSameScrollSeam is the encapsulation gate. The classic row
// (screens.go) and the themed grid (theme_layout.go) each lay the grid out
// themselves; if either stopped stamping its metrics, the number keys and the page
// label would describe the OTHER grid's shape while every unit gate above stayed
// green — which is precisely the drift the seam was extracted to end.
func TestBothGridsDriveTheSameScrollSeam(t *testing.T) {
	for _, h := range []struct{ file, fn string }{
		{"screens.go", "drawEmoteRow"},
		{"theme_layout.go", "drawEmoteGridThemed"},
	} {
		body := funcBodySource(t, h.file, h.fn)
		if !containsCall(body, "setEmoteGridMetrics") {
			t.Errorf("%s does not stamp setEmoteGridMetrics — the grid on screen and the number row disagree", h.fn)
		}
		if !containsCall(body, "emoteFirstSlot") {
			t.Errorf("%s does not draw from emoteFirstSlot — it is still paging on its own", h.fn)
		}
	}
}
