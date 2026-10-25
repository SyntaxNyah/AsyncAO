package ui

import "testing"

// TestCJKHanCandidateOrder pins the Japanese-first Han preference — the root-cause fix.
// The renderer takes the FIRST loaded face that covers a Han rune (pickFont / coverRunes
// walk the chain in order), so whichever Han face firstReadable returns is the one a
// Japanese showname resolves to. Given a fake availability set, firstReadableFunc must
// prefer the modern proportional Japanese face over Microsoft YaHei (the old first-found
// Chinese winner) and over the legacy MS Gothic. No real font files are read — the
// existence predicate is injected — so this runs identically on CI / non-Windows.
func TestCJKHanCandidateOrder(t *testing.T) {
	yuGothM := `C:\Windows\Fonts\YuGothM.ttc`
	yuGothR := `C:\Windows\Fonts\YuGothR.ttc`
	meiryo := `C:\Windows\Fonts\meiryo.ttc`
	yahei := `C:\Windows\Fonts\msyh.ttc`
	msgothic := `C:\Windows\Fonts\msgothic.ttc`
	simsun := `C:\Windows\Fonts\simsun.ttc`

	// availOf makes an existence predicate from a set of present paths.
	availOf := func(present ...string) func(string) bool {
		set := make(map[string]bool, len(present))
		for _, p := range present {
			set[p] = true
		}
		return func(p string) bool { return set[p] }
	}

	cases := []struct {
		name    string
		present []string
		want    string
	}{
		{
			// THE regression: on a typical Windows box BOTH YaHei and Yu Gothic exist.
			// The old first-found list returned YaHei (Chinese) and rendered Japanese in
			// Chinese-styled glyphs; the modern Japanese Gothic must win now.
			name:    "yahei+yugoth present -> Yu Gothic wins (not YaHei)",
			present: []string{yahei, yuGothR, yuGothM, msgothic, simsun},
			want:    yuGothM,
		},
		{
			name:    "no yugothM -> Yu Gothic Regular",
			present: []string{yahei, yuGothR, msgothic},
			want:    yuGothR,
		},
		{
			name:    "older Windows: Meiryo over YaHei / MS Gothic",
			present: []string{yahei, meiryo, msgothic},
			want:    meiryo,
		},
		{
			// A CN-only box with no Japanese face: fall through to a Chinese face
			// (still correct coverage) rather than MS Gothic.
			name:    "CN-only -> YaHei, still ahead of MS Gothic",
			present: []string{yahei, simsun, msgothic},
			want:    yahei,
		},
		{
			name:    "only MS Gothic -> last resort still picked",
			present: []string{msgothic},
			want:    msgothic,
		},
		{
			name:    "nothing present -> empty",
			present: nil,
			want:    "",
		},
	}
	for _, tc := range cases {
		got := firstReadableFunc(cjkHanCandidates, availOf(tc.present...))
		if got != tc.want {
			t.Errorf("%s: firstReadableFunc(han) = %q, want %q", tc.name, got, tc.want)
		}
	}

	// The candidate list itself must keep Japanese ahead of every Chinese face and MS
	// Gothic dead last — a future edit that reorders it silently reintroduces the bug.
	idx := func(p string) int {
		for i, c := range cjkHanCandidates {
			if c == p {
				return i
			}
		}
		return -1
	}
	if !(idx(yuGothM) >= 0 && idx(yuGothM) < idx(yahei)) {
		t.Error("Yu Gothic Medium must come before Microsoft YaHei in cjkHanCandidates")
	}
	if !(idx(meiryo) >= 0 && idx(meiryo) < idx(yahei)) {
		t.Error("Meiryo must come before Microsoft YaHei in cjkHanCandidates")
	}
	if last := len(cjkHanCandidates) - 1; cjkHanCandidates[last] != msgothic {
		t.Errorf("MS Gothic must be the LAST resort, got %q at the tail", cjkHanCandidates[last])
	}
}

// TestCJKFontListChineseBackstop pins the CN-coverage backstop: Yu Gothic's cmap is a
// SUBSET of YaHei's (JIS vs GB18030), so Simplified-only Han would tofu if only Yu Gothic
// loaded. cjkFontList must load BOTH the JP pick and a distinct Chinese face on a common
// box, but must not double-load a Chinese face when the JP pick was already Chinese.
func TestCJKFontListChineseBackstop(t *testing.T) {
	yuGothM := `C:\Windows\Fonts\YuGothM.ttc`
	yahei := `C:\Windows\Fonts\msyh.ttc`
	simsun := `C:\Windows\Fonts\simsun.ttc`

	contains := func(s []string, v string) bool {
		for _, x := range s {
			if x == v {
				return true
			}
		}
		return false
	}

	// Common box: Yu Gothic wins Han (pretty JP) AND YaHei backstops CN-only Han.
	got := hanFacesFor(func(p string) bool { return p == yuGothM || p == yahei })
	if !contains(got, yuGothM) || !contains(got, yahei) {
		t.Errorf("common box should load BOTH Yu Gothic (JP) and YaHei (CN backstop), got %v", got)
	}
	if len(got) != 2 {
		t.Errorf("common box should load exactly 2 Han faces (JP + CN backstop), got %v", got)
	}

	// CN-only box: YaHei is the JP-list pick AND the backstop — must load once, not twice.
	got = hanFacesFor(func(p string) bool { return p == yahei || p == simsun })
	yaheiCount := 0
	for _, f := range got {
		if f == yahei {
			yaheiCount++
		}
	}
	if yaheiCount != 1 {
		t.Errorf("YaHei must appear exactly once on a CN-only box (no double-load), got %v", got)
	}
}

// hanFacesFor runs the Han-selection half of cjkFontList against an injected availability
// predicate and returns the Han faces it would load (excludes the Korean face). Mirrors the
// production logic exactly so the backstop can be asserted without touching disk.
func hanFacesFor(exists func(string) bool) []string {
	var out []string
	han := firstReadableFunc(cjkHanCandidates, exists)
	if han != "" {
		out = append(out, han)
	}
	if cn := firstReadableFunc(cjkHanChineseBackstop, exists); cn != "" && cn != han {
		out = append(out, cn)
	}
	return out
}

// TestCJKHangulCandidateOrder pins Malgun Gothic (modern proportional Korean) ahead of the
// legacy Gulim / Batang faces, mirroring the Han fix for Hangul.
func TestCJKHangulCandidateOrder(t *testing.T) {
	malgun := `C:\Windows\Fonts\malgun.ttf`
	gulim := `C:\Windows\Fonts\gulim.ttc`

	availOf := func(present ...string) func(string) bool {
		set := make(map[string]bool, len(present))
		for _, p := range present {
			set[p] = true
		}
		return func(p string) bool { return set[p] }
	}

	if got := firstReadableFunc(cjkHangulCandidates, availOf(malgun, gulim)); got != malgun {
		t.Errorf("Malgun Gothic must win for Hangul, got %q", got)
	}
	if got := firstReadableFunc(cjkHangulCandidates, availOf(gulim)); got != gulim {
		t.Errorf("Gulim should be picked when Malgun is absent, got %q", got)
	}
	if got := firstReadableFunc(cjkHangulCandidates, availOf()); got != "" {
		t.Errorf("no Korean face present -> empty, got %q", got)
	}
}
