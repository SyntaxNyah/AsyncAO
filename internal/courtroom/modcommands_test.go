package courtroom

import "testing"

// TestBanCommand pins the EXACT /ban string per server software — the safety-critical contract
// (a wrong format silently fails to ban). Formats verified against each server's source.
func TestBanCommand(t *testing.T) {
	cases := []struct {
		name      string
		sw        ServerSoftware
		ipid, uid string
		dur       BanDuration
		reason    string
		want      string
	}{
		// KFO / tsuserver: IPID, "reason" then "duration" (both quoted).
		{"kfo", SoftwareTsuserver, "1234", "5", Ban1Day, "trolling", `/ban 1234 "trolling" "1 day"`},
		{"kfo perma", SoftwareTsuserver, "1234", "", BanPerma, "spam", `/ban 1234 "spam" "perma"`},
		// Akashi: IPID, DURATION then reason (positional, unquoted), short token.
		{"akashi", SoftwareAkashi, "1234", "5", Ban1Day, "trolling", `/ban 1234 1d trolling`},
		{"akashi 1h", SoftwareAkashi, "1234", "", Ban1Hour, "rule 3", `/ban 1234 1h rule 3`},
		// Athena / Nyathena: flags; prefers IPID (-i, offline-capable), -d duration, reason last.
		{"athena ipid", SoftwareAthena, "1234", "5", Ban1Week, "ban evading", `/ban -i 1234 -d 1w ban evading`},
		{"athena uid only", SoftwareAthena, "", "5", Ban6Hours, "spam", `/ban -u 5 -d 6h spam`},
		// Whisker: UID, "reason" then "duration" (quoted), human token.
		{"whisker", SoftwareWhisker, "1234", "5", Ban3Days, "ban evading", `/ban 5 "ban evading" "3 days"`},
		// Blank reason → a default (so the quoted-reason servers get a non-empty arg).
		{"kfo blank reason", SoftwareTsuserver, "1234", "", Ban1Hour, "  ", `/ban 1234 "No reason given" "1 hour"`},
		// A quote in the reason can't break the quoting.
		{"reason with quote", SoftwareWhisker, "", "5", Ban1Day, `said "hi"`, `/ban 5 "said 'hi'" "1 day"`},
	}
	for _, tc := range cases {
		if got := BanCommand(tc.sw, tc.ipid, tc.uid, tc.dur, tc.reason); got != tc.want {
			t.Errorf("%s: BanCommand = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestBanMissingIdentifier pins that a ban is refused (empty string) when the software's required
// identifier isn't available — so the UI disables the button rather than send a broken command.
func TestBanMissingIdentifier(t *testing.T) {
	if got := BanCommand(SoftwareTsuserver, "", "5", Ban1Day, "x"); got != "" {
		t.Errorf("KFO ban without an IPID should refuse, got %q", got)
	}
	if got := BanCommand(SoftwareAkashi, "", "5", Ban1Day, "x"); got != "" {
		t.Errorf("Akashi ban without an IPID should refuse, got %q", got)
	}
	if got := BanCommand(SoftwareWhisker, "1234", "", Ban1Day, "x"); got != "" {
		t.Errorf("Whisker ban without a UID should refuse, got %q", got)
	}
	if got := BanCommand(SoftwareAthena, "", "", Ban1Day, "x"); got != "" {
		t.Errorf("Athena ban with neither IPID nor UID should refuse, got %q", got)
	}
}

// TestKickCommand pins /kick per software, and that a blank reason is allowed (kick doesn't
// force one) while ban does.
func TestKickCommand(t *testing.T) {
	cases := []struct {
		sw        ServerSoftware
		ipid, uid string
		reason    string
		want      string
	}{
		{SoftwareTsuserver, "1234", "", "spam", "/kick 1234 spam"},
		{SoftwareTsuserver, "1234", "", "", "/kick 1234"}, // blank reason → no trailing arg
		{SoftwareAkashi, "1234", "", "rude", "/kick 1234 rude"},
		{SoftwareAthena, "1234", "5", "spam", "/kick -i 1234 spam"},
		{SoftwareAthena, "", "5", "spam", "/kick -u 5 spam"},
		{SoftwareWhisker, "", "5", "", "/kick 5"},
	}
	for _, tc := range cases {
		if got := KickCommand(tc.sw, tc.ipid, tc.uid, tc.reason); got != tc.want {
			t.Errorf("KickCommand(%v) = %q, want %q", tc.sw, got, tc.want)
		}
	}
}

// TestDurationToken pins the human-vs-short split + perma.
func TestDurationToken(t *testing.T) {
	if durationToken(SoftwareAkashi, BanPerma) != "perma" || durationToken(SoftwareTsuserver, BanPerma) != "perma" {
		t.Error("perma must be 'perma' on every software")
	}
	if got := durationToken(SoftwareTsuserver, Ban1Week); got != "1 week" {
		t.Errorf("KFO 1 week = %q, want '1 week'", got)
	}
	if got := durationToken(SoftwareAthena, Ban1Week); got != "1w" {
		t.Errorf("Athena 1 week = %q, want '1w'", got)
	}
	if got := durationToken(SoftwareAkashi, Ban1Day); got != "1d" {
		t.Errorf("Akashi 1 day = %q, want '1d'", got)
	}
	if got := durationToken(SoftwareWhisker, Ban6Hours); got != "6 hours" {
		t.Errorf("Whisker 6 hours = %q, want '6 hours'", got)
	}
}
