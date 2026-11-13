package text_test

import (
	"testing"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/text"
)

// mixed is 6 runes in 9 bytes: é is 2 bytes (1-2), → is 3 (6-8).
const (
	mixed      = "héllo→"
	mixedRunes = 6
	mixedBytes = 9
	cutRunes   = 2
	cutInsideE = 2
	cutAfterE  = 3
	cutInArrow = 7
	arrowStart = 6
)

func TestTruncateCountsRunes(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{mixed, mixedRunes + 1, mixed},
		{mixed, mixedRunes, mixed},
		{mixed, mixedRunes - 1, "héllo"},
		{mixed, cutRunes, "hé"},
		{mixed, 0, ""},
		{mixed, -1, ""},
		{"", cutRunes, ""},
	}
	for _, tc := range cases {
		if got := text.Truncate(tc.in, tc.max); got != tc.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

func TestTruncateBytesNeverSplitsARune(t *testing.T) {
	cases := []struct {
		max  int
		want string
	}{
		{mixedBytes + 1, mixed},
		{mixedBytes, mixed},
		{cutInsideE, "h"},
		{cutAfterE, "hé"},
		{cutInArrow, mixed[:arrowStart]},
		{0, ""},
		{-1, ""},
	}
	for _, tc := range cases {
		got := text.TruncateBytes(mixed, tc.max)
		if got != tc.want {
			t.Errorf("TruncateBytes(%q, %d) = %q, want %q", mixed, tc.max, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("TruncateBytes(%q, %d) produced invalid UTF-8 %q", mixed, tc.max, got)
		}
	}
}
