package render

import "testing"

// The invisible emoji-format runes are built from code points (not written as
// literals) so the source stays plain ASCII for them — staticcheck ST1018 rejects
// bare format characters (VS16/ZWJ) in string literals, and they're impossible to
// read in source anyway. Visible emoji (😀, ❤, 👨…) are fine as literals.
var (
	vs16   = string(rune(0xFE0F)) // VARIATION SELECTOR-16: emoji presentation
	zwj    = string(rune(0x200D)) // ZERO WIDTH JOINER
	keycap = string(rune(0x20E3)) // COMBINING ENCLOSING KEYCAP
)

// TestNeedsEmojiFallback pins the cheap per-message gate: plain/ASCII/CJK text
// stays on the single-font fast path; only supplementary-plane emoji OR a
// VS16-promoted BMP emoji (heart + VS16) trips the fallback.
func TestNeedsEmojiFallback(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"", false},
		{"hello world", false},
		{"Objection!", false},
		{"你好世界", false},    // CJK is BMP, not emoji
		{"hi 😀", true},     // supplementary-plane emoji
		{"❤" + vs16, true}, // BMP heart + VS16 (no supplementary byte — the second signal)
		{"gg 👍🏽", true},    // skin-tone modifier (supplementary)
		{vs16, true},       // lone VS16
	}
	for _, tc := range cases {
		if got := needsEmojiFallback(tc.s); got != tc.want {
			t.Errorf("needsEmojiFallback(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// TestAssignEmoji pins the per-rune routing, especially the COMPOUND sequences
// that must stay whole (the "the user will type a heart" case): VS16 promotion,
// ZWJ family joins, and keycaps — none may fragment back to the text font.
func TestAssignEmoji(t *testing.T) {
	check := func(name, s string, want []bool) {
		t.Helper()
		got := assignEmoji([]rune(s))
		if len(got) != len(want) {
			t.Errorf("%s: len(assignEmoji) = %d, want %d", name, len(got), len(want))
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: assignEmoji[%d] = %v, want %v (full: %v)", name, i, got[i], want[i], got)
				return
			}
		}
	}

	check("plain", "hi", []bool{false, false})
	check("text+emoji", "hi 😀", []bool{false, false, false, true})
	// a + heart(U+2764) + VS16 + b: VS16 promotes the heart, both go to the emoji font.
	check("vs16 heart", "a❤"+vs16+"b", []bool{false, true, true, false})
	// man ZWJ woman ZWJ girl: every rune (incl. the ZWJs) stays in one run.
	check("zwj family", "👨"+zwj+"👩"+zwj+"👧", []bool{true, true, true, true, true})
	// Keycap: '1' + VS16 + enclosing keycap (U+20E3) — all emoji.
	check("keycap", "1"+vs16+keycap, []bool{true, true, true})
	// A bare '1' (no keycap) stays text.
	check("bare digit", "1", []bool{false})
	check("cjk", "你好", []bool{false, false})
}
