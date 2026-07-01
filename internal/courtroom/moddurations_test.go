package courtroom

import "testing"

// TestCanonicalBanDuration pins the custom-duration parser: every accepted spelling
// lands on the canonical short token the saved chips store, and garbage is refused
// (ok=false) so it can never become a chip.
func TestCanonicalBanDuration(t *testing.T) {
	ok := []struct{ in, want string }{
		{"45m", "45m"}, {"45 min", "45m"}, {"45 minutes", "45m"},
		{"12h", "12h"}, {"12H", "12h"}, {"12 hours", "12h"}, {"1 hr", "1h"},
		{"2d", "2d"}, {"2 days", "2d"}, {" 2 days ", "2d"},
		{"1w", "1w"}, {"3 weeks", "3w"}, {"1wk", "1w"},
		{"perma", "perma"}, {"Permanent", "perma"}, {"forever", "perma"},
		{"9999d", "9999d"},
	}
	for _, c := range ok {
		got, k := CanonicalBanDuration(c.in)
		if !k || got != c.want {
			t.Errorf("CanonicalBanDuration(%q) = %q,%v — want %q,true", c.in, got, k, c.want)
		}
	}
	for _, bad := range []string{"", "m", "45", "45x", "0d", "-3d", "10000d", "2 months", "1y", "a1d", "1d2h"} {
		if got, k := CanonicalBanDuration(bad); k {
			t.Errorf("CanonicalBanDuration(%q) accepted as %q — must refuse", bad, got)
		}
	}
}

// TestBanDurationTokenLabel pins the friendly custom-chip label (singular/plural + perma).
func TestBanDurationTokenLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"45m", "45 minutes"}, {"1h", "1 hour"}, {"2d", "2 days"}, {"1w", "1 week"}, {"perma", "Permanent"},
	}
	for _, c := range cases {
		if got := BanDurationTokenLabel(c.in); got != c.want {
			t.Errorf("BanDurationTokenLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBanCommandToken pins the custom-duration /ban per software: the SAME canonical
// token renders short for the Go/C++ servers and human for the pytimeparse/Whisker
// ones — mirroring durationToken's split — and the command shape matches BanCommand's
// pinned formats exactly.
func TestBanCommandToken(t *testing.T) {
	cases := []struct {
		name  string
		sw    ServerSoftware
		token string
		want  string
	}{
		{"tsuserver human quoted", SoftwareTsuserver, "45m", `/ban 123 "spam" "45 minutes"`},
		{"akashi short, dur before reason", SoftwareAkashi, "45m", "/ban 123 45m spam"},
		{"witches follows akashi", SoftwareWitches, "2d", "/ban 123 2d spam"},
		{"athena short via -d", SoftwareAthena, "12h", "/ban -i 123 -d 12h spam"},
		{"whisker human quoted, uid", SoftwareWhisker, "1w", `/ban 42 "spam" "1 week"`},
		{"perma stays perma everywhere", SoftwareTsuserver, "perma", `/ban 123 "spam" "perma"`},
	}
	for _, c := range cases {
		if got := BanCommandToken(c.sw, "123", "42", c.token, "spam"); got != c.want {
			t.Errorf("%s: BanCommandToken = %q, want %q", c.name, got, c.want)
		}
	}
	if got := BanCommandToken(SoftwareAkashi, "123", "42", "", "spam"); got != "" {
		t.Errorf("empty token must build no command, got %q", got)
	}
	// The preset path is untouched by the refactor: BanCommand still builds its
	// pinned format (a spot-check; TestBanCommand pins the full matrix).
	if got := BanCommand(SoftwareAkashi, "123", "42", Ban1Day, "spam"); got != "/ban 123 1d spam" {
		t.Errorf("BanCommand after refactor = %q, want '/ban 123 1d spam'", got)
	}
}
