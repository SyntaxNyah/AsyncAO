package render

import "testing"

// Selection geometry pins (chatbox partial-copy): RuneAt maps points to the
// nearest source-rune boundary and LineSpanX maps a source range back to
// pixels, for BOTH raster shapes, with centering honoured — the same
// synthetic-advances style as the PrefixWidth pin.

// twoLinePlain models "hello world" wrapped as "hello" / "world" (the space
// dropped at the wrap): 5 runes per line, 8 px per rune, source ranges
// [0,5) and [6,11), line height 16.
func twoLinePlain() *MessageRaster {
	adv := []int32{0, 8, 16, 24, 32, 40}
	return &MessageRaster{
		text:       "hello world",
		lineH:      16,
		lines:      []rasterLine{{runes: 5, advances: adv}, {runes: 5, advances: adv}},
		lineRanges: []lineRange{{0, 5}, {6, 11}},
	}
}

func TestRuneAtPlain(t *testing.T) {
	m := twoLinePlain()
	cases := []struct {
		x, y int32
		want int
	}{
		{0, 0, 0},     // top-left → first boundary
		{-50, -9, 0},  // above/left clamps
		{8, 5, 1},     // exactly at the 1st boundary of line 0
		{11, 5, 1},    // left of halfway into rune 1 → boundary 1
		{13, 5, 2},    // past halfway → boundary 2
		{400, 5, 5},   // past line 0's end → its end boundary (the wrap point)
		{0, 16, 6},    // line 1 starts at source rune 6 (the dropped space sits between)
		{19, 20, 8},   // just left of boundary 2/3's midpoint on line 1 → 6+2
		{400, 90, 11}, // below + right clamps to the very end
	}
	for _, tc := range cases {
		if got := m.RuneAt(tc.x, tc.y); got != tc.want {
			t.Errorf("RuneAt(%d,%d) = %d, want %d", tc.x, tc.y, got, tc.want)
		}
	}
	if got := (&MessageRaster{}).RuneAt(10, 10); got != 0 {
		t.Errorf("empty raster RuneAt = %d, want 0", got)
	}
}

func TestLineSpanXPlain(t *testing.T) {
	m := twoLinePlain()
	// Selection [2, 8): covers "llo" on line 0 and "wo" on line 1.
	if x0, x1, ok := m.LineSpanX(0, 2, 8); !ok || x0 != 16 || x1 != 40 {
		t.Errorf("line 0 span = %d..%d ok=%v, want 16..40 true", x0, x1, ok)
	}
	if x0, x1, ok := m.LineSpanX(1, 2, 8); !ok || x0 != 0 || x1 != 16 {
		t.Errorf("line 1 span = %d..%d ok=%v, want 0..16 true", x0, x1, ok)
	}
	// A selection that never touches line 1.
	if _, _, ok := m.LineSpanX(1, 0, 5); ok {
		t.Error("selection [0,5) must not touch line 1 (its range starts at 6)")
	}
	// Degenerate + out-of-range inputs.
	if _, _, ok := m.LineSpanX(0, 3, 3); ok {
		t.Error("empty selection must be ok=false")
	}
	if _, _, ok := m.LineSpanX(7, 0, 11); ok {
		t.Error("out-of-range line must be ok=false")
	}
	// Centering shifts both ends by the line's offset.
	m.centerOff = []int32{10, 0}
	if x0, x1, ok := m.LineSpanX(0, 2, 8); !ok || x0 != 26 || x1 != 50 {
		t.Errorf("centered line 0 span = %d..%d ok=%v, want 26..50 true", x0, x1, ok)
	}
	// …and RuneAt un-shifts the click by the same offset: 29 px − 10 center
	// = 19, just left of boundary 2/3's midpoint → boundary 2.
	if got := m.RuneAt(29, 0); got != 2 {
		t.Errorf("centered RuneAt(29) = %d, want 2", got)
	}
}

func TestSelectionGeometryStyled(t *testing.T) {
	// One line, two spans (6+13 px then 9/19/30 px at xOffset 13) — the
	// styled/fallback shape; source range [3, 8) (say a wrap dropped runes
	// 0-2 onto an earlier line in a bigger message).
	m := &MessageRaster{
		lineH: 14,
		styled: [][]rasterSpan{{
			{runes: 2, xOffset: 0, advances: []int32{0, 6, 13}},
			{runes: 3, xOffset: 13, advances: []int32{0, 9, 19, 30}},
		}},
		lineRanges: []lineRange{{3, 8}},
	}
	// Boundaries land at 0,6,13,22,32,43.
	if got := m.RuneAt(0, 0); got != 3 {
		t.Errorf("RuneAt(0) = %d, want the line's start rune 3", got)
	}
	if got := m.RuneAt(10, 0); got != 5 { // 10 px: past mid(6,13), left of mid(13,22) → boundary 2 → source 3+2
		t.Errorf("RuneAt(10) = %d, want 5", got)
	}
	if got := m.RuneAt(500, 0); got != 8 {
		t.Errorf("RuneAt past end = %d, want 8", got)
	}
	// Span [4,7): from boundary 1 (6 px) to boundary 4 (13+19=32 px).
	if x0, x1, ok := m.LineSpanX(0, 4, 7); !ok || x0 != 6 || x1 != 32 {
		t.Errorf("styled span = %d..%d ok=%v, want 6..32 true", x0, x1, ok)
	}
	// Clamping: a selection wider than the line clips to its range.
	if x0, x1, ok := m.LineSpanX(0, 0, 99); !ok || x0 != 0 || x1 != 43 {
		t.Errorf("clamped span = %d..%d ok=%v, want 0..43 true", x0, x1, ok)
	}
}
