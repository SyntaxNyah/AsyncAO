package ui

import "testing"

// TestFuzzyMatch pins the palette filter: in-order subsequence, case-blind,
// empty query matches all.
func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		s, q string
		want bool
	}{
		{"Screenshot", "", true},
		{"Screenshot", "scr", true},
		{"Screenshot", "SST", true},   // subsequence, case-insensitive
		{"Screenshot", "shot", true},  // contiguous also works
		{"Screenshot", "xq", false},   // absent letters don't match
		{"Edit Layout", "elay", true}, // spans words
	}
	for _, tc := range cases {
		if got := fuzzyMatch(tc.s, tc.q); got != tc.want {
			t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tc.s, tc.q, got, tc.want)
		}
	}
}

// TestPaletteCommandForm pins reference-line extraction: the slash form comes
// out, footnote lines without one yield "".
func TestPaletteCommandForm(t *testing.T) {
	if got := paletteCommandForm(`Ban — /ban -i <ipid> | -u <uid>  -d <dur>  reason`); got != `/ban -i <ipid> | -u <uid>  -d <dur>  reason` {
		t.Errorf("command form = %q", got)
	}
	if got := paletteCommandForm("(no /cm model on Whisker)"); got == "" {
		t.Error("a line with a slash still extracts (even parenthesised)") // documents the behaviour
	}
	if got := paletteCommandForm("Unknown server software"); got != "" {
		t.Errorf("no slash → no command, got %q", got)
	}
}

// TestPaletteMatches pins the row builder: offline (sess nil) lists actions
// only, the query filters, and the result is bounded by paletteMaxN.
func TestPaletteMatches(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	all := a.paletteMatches("")
	if len(all) == 0 || len(all) > paletteMaxN {
		t.Fatalf("unfiltered matches = %d, want (0, %d]", len(all), paletteMaxN)
	}
	shots := a.paletteMatches("screenshot")
	if len(shots) == 0 || shots[0].widget < 0 {
		t.Fatalf("screenshot should match an Extras action: %+v", shots)
	}
	if got := a.paletteMatches("zzzzqqqq"); len(got) != 0 {
		t.Errorf("nonsense query must match nothing, got %d", len(got))
	}
}
