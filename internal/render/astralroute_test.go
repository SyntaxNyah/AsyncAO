package render

import "testing"

// TestAstralNonEmojiKeepsItsTextFace is the routing regression. isEmojiBase used
// to read "any rune above U+FFFF", and the three consumers apply it as an
// UNCONDITIONAL override of the covering text face — so every supplementary-plane
// SCRIPT was forced onto the bundled colour-emoji face, which holds emoji and
// nothing else, and drew that face's .notdef box even when a face already in the
// chain had the glyph.
func TestAstralNonEmojiKeepsItsTextFace(t *testing.T) {
	notEmoji := []struct {
		name string
		r    rune
	}{
		{"Linear B syllable", 0x10000},
		{"Old Italic", 0x10300},
		{"Gothic", 0x10330},
		{"Deseret", 0x10400},
		{"Osage", 0x104B0},
		{"Cuneiform", 0x12000},
		{"Egyptian hieroglyph", 0x13000},
		{"Mathematical bold A", 0x1D400},
		{"Adlam", 0x1E900},
		{"CJK Extension B", 0x20000},
		{"CJK Extension C", 0x2A700},
	}
	for _, tc := range notEmoji {
		if isEmojiBase(tc.r) {
			t.Errorf("%s (U+%04X) routes to the colour-emoji face, which has no glyph for it — it must keep the text face that covers it", tc.name, tc.r)
		}
	}

	realEmoji := []struct {
		name string
		r    rune
	}{
		{"Mahjong tile (block start)", 0x1F000},
		{"Regional indicator A", 0x1F1E6},
		{"Skin tone modifier", 0x1F3FB},
		{"Grinning face", 0x1F600},
		{"Rocket", 0x1F680},
		{"Pictographs Extended-A", 0x1FA70},
	}
	for _, tc := range realEmoji {
		if !isEmojiBase(tc.r) {
			t.Errorf("%s (U+%04X) must still route to the colour-emoji face", tc.name, tc.r)
		}
	}
}

// TestAstralScriptRunsStayOnOneFace walks the whole mask the way the raster does:
// a supplementary-plane script run must produce an all-false mask (every rune on
// its text face), while an emoji run stays all-true.
func TestAstralScriptRunsStayOnOneFace(t *testing.T) {
	cuneiform := []rune("\U00012000\U00012001\U00012002")
	for i, v := range assignEmoji(cuneiform) {
		if v {
			t.Errorf("cuneiform rune %d was marked emoji", i)
		}
	}
	faces := []rune("\U0001F600\U0001F601")
	for i, v := range assignEmoji(faces) {
		if !v {
			t.Errorf("emoji rune %d was NOT marked emoji", i)
		}
	}
	// A mixed line: the CJK Extension B ideograph keeps its text face while the
	// emoji beside it still goes to the emoji face.
	mixed := []rune("\U00020000\U0001F600")
	got := assignEmoji(mixed)
	if got[0] {
		t.Error("the CJK Ext-B ideograph was routed to the emoji face")
	}
	if !got[1] {
		t.Error("the emoji beside it lost its emoji routing")
	}
}
