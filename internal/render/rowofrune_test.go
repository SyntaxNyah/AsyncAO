package render

import "testing"

// RowOfRune is the vertical sibling of RuneAt: it answers "which display row is
// the crawl on", which is what the chatbox needs in order to follow its own
// typewriter the way AO2's ensureCursorVisible does (courtroom.cpp:4514-4521).
//
// It reads the SAME lineRanges table RuneAt and LineSpanX read, so these gates
// drive the real accessor against real line data rather than a stub.

// sixLineWrapped models a six-row wrapped message, each row 4 source runes wide
// with the wrap separator dropped between rows (so the ranges step by 5) — the
// shape a long IC post actually produces.
func sixLineWrapped() *MessageRaster {
	m := &MessageRaster{lineH: 20}
	for i := 0; i < 6; i++ {
		start := i * 5
		m.lineRanges = append(m.lineRanges, lineRange{start, start + 4})
		m.lines = append(m.lines, rasterLine{runes: 4, advances: []int32{0, 8, 16, 24, 32}})
	}
	return m
}

func TestRowOfRuneWalksTheWrappedRows(t *testing.T) {
	m := sixLineWrapped()
	for _, tc := range []struct {
		n, want int
		why     string
	}{
		{0, 0, "the first rune is on the first row"},
		{3, 0, "the last rune of row 0"},
		{4, 0, "the dropped separator belongs to the row it followed"},
		{5, 1, "the first rune of row 1"},
		{12, 2, "mid-message"},
		{29, 5, "the last rune of the last row"},
		{99, 5, "past the end clamps to the last row, never off the table"},
		{-7, 0, "a negative position clamps to the first row"},
	} {
		if got := m.RowOfRune(tc.n); got != tc.want {
			t.Errorf("RowOfRune(%d) = %d, want %d (%s)", tc.n, got, tc.want, tc.why)
		}
	}
}

func TestRowOfRuneIsTotalOnDegenerateRasters(t *testing.T) {
	var nilRaster *MessageRaster
	if got := nilRaster.RowOfRune(5); got != 0 {
		t.Errorf("nil raster = %d, want 0", got)
	}
	if got := (&MessageRaster{}).RowOfRune(5); got != 0 {
		t.Errorf("an unbuilt raster = %d, want 0", got)
	}
}

// TestRowOfRuneAgreesWithLineRange is the anti-drift gate: the row it answers
// must be a row whose OWN range contains the position, checked against the
// exported accessor the selection highlight uses. Two readings of one table that
// disagree is how a scroll ends up one row away from the glyphs it is following.
func TestRowOfRuneAgreesWithLineRange(t *testing.T) {
	m := sixLineWrapped()
	for n := 0; n < 30; n++ {
		row := m.RowOfRune(n)
		start, _ := m.LineRange(row)
		if start > n {
			t.Fatalf("RowOfRune(%d) = row %d, whose range starts at %d — the row is ahead of the position", n, row, start)
		}
		if row+1 < m.Lines() {
			if next, _ := m.LineRange(row + 1); next <= n {
				t.Fatalf("RowOfRune(%d) = row %d, but row %d starts at %d — a later row also holds it", n, row, row+1, next)
			}
		}
	}
}
