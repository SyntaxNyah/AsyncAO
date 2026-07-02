package render

import "testing"

// TestPrefixWidthBothShapes pins the text-field caret metric for BOTH raster
// shapes. The regression this guards: RasterizeFallback (non-Latin/emoji field
// text) builds the STYLED shape, and a lines-only PrefixWidth returned 0 for
// it — the drawn caret sat pinned at the field's left edge.
func TestPrefixWidthBothShapes(t *testing.T) {
	// Single-font shape: advances[i] = width of the first i runes.
	plain := &MessageRaster{lines: []rasterLine{{
		runes:    3,
		advances: []int32{0, 7, 15, 24},
	}}}
	for n, want := range map[int]int32{-1: 0, 0: 0, 1: 7, 2: 15, 3: 24, 9: 24} {
		if got := plain.PrefixWidth(n); got != want {
			t.Errorf("plain PrefixWidth(%d) = %d, want %d", n, got, want)
		}
	}

	// Styled/fallback shape: two spans on one line (e.g. a Latin run + a
	// Cyrillic run in a different face), each with its own xOffset + advances.
	styled := &MessageRaster{styled: [][]rasterSpan{{
		{runes: 2, xOffset: 0, advances: []int32{0, 6, 13}},
		{runes: 3, xOffset: 13, advances: []int32{0, 9, 19, 30}},
	}}}
	for n, want := range map[int]int32{
		0: 0,
		1: 6,       // inside span 1
		2: 13,      // span 1 fully revealed
		3: 13 + 9,  // one rune into span 2 (xOffset + its advance)
		5: 13 + 30, // everything
		8: 13 + 30, // clamped past the end
	} {
		if got := styled.PrefixWidth(n); got != want {
			t.Errorf("styled PrefixWidth(%d) = %d, want %d", n, got, want)
		}
	}
	if got := (&MessageRaster{}).PrefixWidth(3); got != 0 {
		t.Errorf("empty raster PrefixWidth = %d, want 0", got)
	}
}
