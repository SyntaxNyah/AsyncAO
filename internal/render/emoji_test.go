package render

import "testing"

// TestNeedsEmojiFallback pins the cheap per-message gate: plain/ASCII/CJK text
// stays on the single-font fast path; only supplementary-plane emoji OR a
// VS16-promoted BMP emoji (❤️) trips the fallback.
func TestNeedsEmojiFallback(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"", false},
		{"hello world", false},
		{"Objection!", false},
		{"你好世界", false}, // CJK is BMP, not emoji
		{"hi 😀", true},  // supplementary-plane emoji
		{"❤️", true},    // BMP heart + VS16 (no supplementary byte — the second signal)
		{"gg 👍🏽", true}, // skin-tone modifier (supplementary)
		{"️", true},     // lone VS16
	}
	for _, tc := range cases {
		if got := needsEmojiFallback(tc.s); got != tc.want {
			t.Errorf("needsEmojiFallback(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// TestAssignEmoji pins the per-rune routing, especially the COMPOUND sequences
// that must stay whole (the advisor's "the user will type ❤️" case): VS16
// promotion, ZWJ family joins, and keycaps — none may fragment back to the text
// font mid-emoji.
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
	// ❤️ = U+2764 U+FE0F: the VS16 promotes the heart, both render from the emoji font.
	check("vs16 heart", "a❤️b", []bool{false, true, true, false})
	// 👨‍👩‍👧 = man ZWJ woman ZWJ girl: every rune (incl. the ZWJs) stays in one run.
	check("zwj family", "\U0001F468‍\U0001F469‍\U0001F467",
		[]bool{true, true, true, true, true})
	// Keycap 1️⃣ = '1' U+FE0F U+20E3: base + VS16 + keycap all emoji.
	check("keycap", "1️⃣", []bool{true, true, true})
	// A bare '1' (no keycap) stays text.
	check("bare digit", "1", []bool{false})
	check("cjk", "你好", []bool{false, false})
}
