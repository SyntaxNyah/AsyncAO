package render

// F5 — hearts, sparkles and other SYMBOLS drawing as boxes while CJK and Greek came
// out fine.
//
// The v1.87 Unicode census walked SCRIPTS. Symbol blocks were never in it, and they
// fail for a different reason than a script does: a script gap is answered by a text
// font (the curated tiers, then the system-font census), but the BMP pictographs
// live in the COLOUR-EMOJI face, and nothing routed a bare one to it.
// needsEmojiFallback only fires on a supplementary-plane rune or a VS16, and a bare
// ♥ / ✨ / ⭐ has neither — so the character went to the text chain, which has no
// glyph for it, forever.
//
// These gates pin the eligibility table. The ROUTING decision (only promote a rune no
// text face covers) is coverage-driven and lives in package ui, where the font chain
// is; see TestUncoveredSymbolsRouteToTheEmojiFace there.

import "testing"

// TestBMPPictographCoversTheReportedSymbolBlocks walks the characters the field
// report and its neighbours are made of. Every one must be ELIGIBLE for the colour
// face, or it can only ever draw as a box on a machine whose text fonts lack it.
func TestBMPPictographCoversTheReportedSymbolBlocks(t *testing.T) {
	for _, c := range []struct {
		r    rune
		what string
	}{
		{0x2665, "♥ black heart suit"},
		{0x2661, "♡ white heart suit"},
		{0x2764, "❤ heavy black heart"},
		{0x2728, "✨ sparkles"},
		{0x2605, "★ black star"},
		{0x2606, "☆ white star"},
		{0x2B50, "⭐ white medium star"},
		{0x2B55, "⭕ heavy large circle"},
		{0x2600, "☀ black sun with rays"},
		{0x2622, "☢ radioactive"},
		{0x263A, "☺ white smiling face"},
		{0x2702, "✂ black scissors"},
		{0x2705, "✅ white heavy check mark"},
		{0x274C, "❌ cross mark"},
		{0x27A1, "➡ black rightwards arrow"},
		{0x2B05, "⬅ leftwards black arrow"},
		{0x2194, "↔ left right arrow"},
		{0x21A9, "↩ leftwards arrow with hook"},
		{0x231A, "⌚ watch"},
		{0x23F3, "⏳ hourglass with flowing sand"},
		{0x25FE, "◾ black medium small square"},
		{0x2934, "⤴ arrow pointing rightwards then curving upwards"},
		{0x00A9, "© copyright"},
		{0x2122, "™ trade mark"},
		{0x24C2, "Ⓜ circled latin capital M"},
		{0x3030, "〰 wavy dash"},
		{0x3299, "㊙ circled ideograph secret"},
		{0x203C, "‼ double exclamation mark"},
	} {
		if !BMPPictograph(c.r) {
			t.Errorf("BMPPictograph(U+%04X) = false — %s can then only ever draw as a box on a machine whose text fonts lack it (F5)", c.r, c.what)
		}
	}
}

// TestBMPPictographExcludesOrdinaryText is the other half, and the more important
// one. The table only makes a rune ELIGIBLE, but eligibility that swept in letters,
// digits or the scripts the census answers would hand real text to a face that has
// none of it — the exact mistake isEmojiBase's doc records ("any rune above U+FFFF"
// forced Linear B and CJK Ext-B onto the emoji font).
func TestBMPPictographExcludesOrdinaryText(t *testing.T) {
	for _, c := range []struct {
		r    rune
		what string
	}{
		{' ', "space"},
		{'A', "latin capital A"},
		{'z', "latin small z"},
		{'7', "digit seven"},
		{'.', "full stop"},
		{'-', "hyphen"},
		{0x00E9, "é latin small e with acute"},
		{0x0416, "Ж cyrillic capital zhe"},
		{0x03A9, "Ω greek capital omega"},
		{0x05D0, "א hebrew alef"},
		{0x0627, "ا arabic alef"},
		{0x0E01, "ก thai ko kai"},
		{0x3042, "あ hiragana a"},
		{0x30A2, "ア katakana a"},
		{0x4E00, "一 han one"},
		{0xAC00, "가 hangul ga"},
		{0x0905, "अ devanagari a"},
		{0x2018, "‘ left single quote"},
		{0x2014, "— em dash"},
		{0x2026, "… horizontal ellipsis"},
		{0x1F600, "😀 grinning face (supplementary — isEmojiBase's job, not this table's)"},
		{0x10300, "𐌀 old italic a (supplementary, NOT emoji)"},
	} {
		if BMPPictograph(c.r) {
			t.Errorf("BMPPictograph(U+%04X) = true — %s is ordinary text and must stay on the face that can shape it (F5)", c.r, c.what)
		}
	}
}

// TestBMPPictographDoesNotChangeEmojiRouting is the closed half of open–closed: the
// new table must not have altered assignEmoji, which is what decides emoji runs for
// every message the client already renders correctly.
func TestBMPPictographDoesNotChangeEmojiRouting(t *testing.T) {
	// A bare BMP pictograph is still NOT an emoji run: it becomes one only when the
	// caller finds no text face covers it, and that decision is not made here.
	if got := assignEmoji([]rune("i ♥ you")); got[2] {
		t.Error("assignEmoji promoted a bare ♥ by codepoint — routing must stay coverage-driven, or every heart in a font that HAS one would render from the emoji face")
	}
	// VS16 still promotes, supplementary-plane emoji still route, plain text doesn't.
	seq := []rune("❤️ ok")
	m := assignEmoji(seq)
	if !m[0] || !m[1] {
		t.Error("VS16 promotion regressed — ❤️ must still render from the colour face")
	}
	if m[3] {
		t.Error("plain text after an emoji was pulled into the emoji run")
	}
	if !needsEmojiFallback("hi 😀") || needsEmojiFallback("i ♥ you") {
		t.Error("needsEmojiFallback changed shape — it must stay the cheap byte scan, so a line of hearts does not take the raster path on a machine that can draw them")
	}
}
