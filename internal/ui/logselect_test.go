package ui

import "testing"

func TestSelPointOrder(t *testing.T) {
	a := selPoint{entry: 1, off: 3}
	b := selPoint{entry: 1, off: 7}
	c := selPoint{entry: 2, off: 0}
	if !a.before(b) || !b.before(c) || !a.before(c) {
		t.Error("ordering by (entry, off) is wrong")
	}
	if a.before(a) {
		t.Error("a point is not before itself")
	}
	// orderSel swaps a backwards drag.
	lo, hi := orderSel(c, a)
	if !lo.equal(a) || !hi.equal(c) {
		t.Errorf("orderSel = (%v,%v), want (%v,%v)", lo, hi, a, c)
	}
}

// fixedMeasure pretends each rune is w px wide — lets the hit-test be checked
// deterministically without a font.
func fixedMeasure(w int32) func([]rune) int32 {
	return func(r []rune) int32 { return int32(len(r)) * w }
}

func TestHitTestRune(t *testing.T) {
	runes := []rune("hello") // 5 runes, 10 px each => width 50
	m := fixedMeasure(10)
	cases := []struct {
		x    int32
		want int
	}{
		{-5, 0},  // left of the line
		{0, 0},   // exact start
		{4, 0},   // within the first glyph, nearer the left edge
		{6, 1},   // within the first glyph, nearer the right edge
		{23, 2},  // nearer the boundary at 20 (index 2) than 30
		{27, 3},  // nearer the boundary at 30 (index 3) than 20
		{50, 5},  // exact end
		{999, 5}, // past the end clamps to len
	}
	for _, tc := range cases {
		if got := hitTestRune(runes, tc.x, m); got != tc.want {
			t.Errorf("hitTestRune(x=%d) = %d, want %d", tc.x, got, tc.want)
		}
	}
	if got := hitTestRune(nil, 10, m); got != 0 {
		t.Errorf("empty line => %d, want 0", got)
	}
}

func TestSelectedText(t *testing.T) {
	entries := []string{
		"Phoenix: objection!",
		"Edgeworth: hold it",
		"Judge: order",
	}
	get := func(e int) string { return entries[e] }

	// Single-entry span: "objection" out of "Phoenix: objection!".
	lo := selPoint{entry: 0, off: 9}
	hi := selPoint{entry: 0, off: 18}
	if got := selectedText(get, lo, hi); got != "objection" {
		t.Errorf("single-entry = %q, want %q", got, "objection")
	}

	// Multi-entry span: tail of entry 0, all of 1, head of 2.
	lo = selPoint{entry: 0, off: 9} // from "objection!"
	hi = selPoint{entry: 2, off: 5} // through "Judge"
	want := "objection!\nEdgeworth: hold it\nJudge"
	if got := selectedText(get, lo, hi); got != want {
		t.Errorf("multi-entry = %q, want %q", got, want)
	}

	// Out-of-range offsets clamp instead of panicking.
	lo = selPoint{entry: 0, off: -3}
	hi = selPoint{entry: 0, off: 999}
	if got := selectedText(get, lo, hi); got != entries[0] {
		t.Errorf("clamped span = %q, want the whole entry", got)
	}
}
