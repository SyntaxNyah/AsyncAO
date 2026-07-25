package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

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

// TestWrapMeasuredBreaksOnTheCallersWidth pins WrapMeasured (#42): the caller —
// not a fixed face — decides how wide a candidate line is, so a wrap can be made
// to agree with a draw that resolves DIFFERENT faces per row. Here the stand-in
// metric charges a row that contains the marker word far more per rune, and that
// alone must force the extra break; a fixed-metric wrap can't see it. Pure Go:
// no font, no SDL.
func TestWrapMeasuredBreaksOnTheCallersWidth(t *testing.T) {
	const (
		narrowPx = 6   // px per rune for an ordinary row
		widePx   = 30  // px per rune once the marker word is on the row
		colW     = 120 // the column both wraps break against
	)
	measure := func(s string) int32 {
		per := int32(narrowPx)
		if strings.Contains(s, "WIDE") {
			per = widePx
		}
		return int32(len([]rune(s))) * per
	}
	const text = "aa aa aa WIDE aa aa aa"
	wide := WrapMeasured(measure, text, colW, 0)
	flat := WrapMeasured(func(s string) int32 { return int32(len([]rune(s))) * narrowPx }, text, colW, 0)
	if len(wide) <= len(flat) {
		t.Errorf("the caller's per-row metric must drive the breaks: marker=%d rows, flat=%d rows", len(wide), len(flat))
	}
	// The invariant the OOC/IC log relies on: no emitted row measures wider than
	// the column (a lone rune that can't fit anywhere is the only exception).
	for i, row := range wide {
		if r := []rune(row); len(r) > 1 && measure(row) > colW {
			t.Errorf("row %d %q measures %d > column %d", i, row, measure(row), colW)
		}
	}
	if got, want := strings.Join(strings.Fields(strings.Join(wide, " ")), " "), text; got != want {
		t.Errorf("wrap lost text: %q, want %q", got, want)
	}

	// Rune granularity: a run with no spaces hard-splits at a RUNE boundary, so a
	// wall of 4-byte emoji stays valid UTF-8 (a byte-halving split would not).
	emoji := strings.Repeat("😀", 40) // 40 × narrowPx = 240 px, twice the column
	rows := WrapMeasured(measure, emoji, colW, 0)
	if len(rows) < 2 {
		t.Fatalf("a wall of emoji must hard-split, got %d rows", len(rows))
	}
	for i, row := range rows {
		if !utf8.ValidString(row) {
			t.Errorf("row %d is not valid UTF-8: %q", i, row)
		}
	}
	if got := strings.Join(rows, ""); got != emoji {
		t.Errorf("hard split lost runes: %q", got)
	}

	// maxLines caps the output; a nil metric returns nothing rather than panicking.
	if got := WrapMeasured(measure, text, colW, 2); len(got) > 2 {
		t.Errorf("maxLines=2 produced %d rows", len(got))
	}
	if got := WrapMeasured(nil, text, colW, 0); got != nil {
		t.Errorf("nil measure = %v, want nil", got)
	}
}
