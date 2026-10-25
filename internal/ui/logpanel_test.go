package ui

import "testing"

// TestLogSearchRowH pins the log panel's search-row sizing (playtest, Tifera:
// a custom whole-UI font taller than the fixed 24px box spilled its glyphs
// onto the first log line): the row grows with the chrome font, and the
// stock-font case keeps the historical 24px exactly — byte-identical layout.
func TestLogSearchRowH(t *testing.T) {
	if got := logSearchRowH(14); got != logSearchRowMinH {
		t.Errorf("stock-size font must keep the 24px row, got %d", got)
	}
	if got := logSearchRowH(16); got != logSearchRowMinH {
		t.Errorf("boundary font (16+8=24) must keep the 24px row, got %d", got)
	}
	if got := logSearchRowH(30); got != 38 {
		t.Errorf("a 30px chrome font needs a 38px row (font + padding), got %d", got)
	}
}
