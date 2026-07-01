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
		// Nyathena forks Athena → byte-identical ban syntax (same builder path).
		{"nyathena ipid", SoftwareNyathena, "1234", "5", Ban1Week, "ban evading", `/ban -i 1234 -d 1w ban evading`},
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
		// Athena/Nyathena kick a CONNECTED client by UID — never the IPID, even when we have it
		// (an IPID kick is a silent no-op there, the "kick does nothing" bug).
		{SoftwareAthena, "1234", "5", "spam", "/kick -u 5 spam"},
		{SoftwareAthena, "", "5", "spam", "/kick -u 5 spam"},
		{SoftwareNyathena, "1234", "5", "spamming", "/kick -u 5 spamming"},
		{SoftwareAthena, "1234", "", "spam", ""}, // no UID → can't kick (won't fall back to the IPID)
		{SoftwareWhisker, "", "5", "", "/kick 5"},
	}
	for _, tc := range cases {
		if got := KickCommand(tc.sw, tc.ipid, tc.uid, tc.reason); got != tc.want {
			t.Errorf("KickCommand(%v) = %q, want %q", tc.sw, got, tc.want)
		}
	}
}

// TestDetectSoftware pins the join-time auto-detection from the real software strings each
// server announces in its ID packet (the same strings the built-in login matches).
func TestDetectSoftware(t *testing.T) {
	cases := map[string]ServerSoftware{
		"Akashi 1.8":           SoftwareAkashi,
		"KFO-Server":           SoftwareTsuserver,
		"tsuserver3":           SoftwareTsuserver,
		"tsuserverCC":          SoftwareTsuserver,
		"Athena":               SoftwareAthena,
		"Nyathena":             SoftwareNyathena, // must NOT fall through to Athena (substring)
		"Nyathena v1.0.2":      SoftwareNyathena,
		"Whisker":              SoftwareWhisker,
		"WAP-Akashi":           SoftwareWitches, // the ID string the fork now announces
		"witches-akashi-party": SoftwareWitches, // …and its canonical name — both precede "akashi"
		"some random server":   SoftwareUnknown,
		"":                     SoftwareUnknown,
	}
	for s, want := range cases {
		if got := DetectSoftware(s); got != want {
			t.Errorf("DetectSoftware(%q) = %v, want %v", s, got, want)
		}
	}
}

// TestCMControls pins the area (CM) room-control commands per software, including the cases that
// vary (the area-kick spelling) or are absent (Whisker has no /cm model).
func TestCMControls(t *testing.T) {
	// /cm + /uncm exist on tsuserver/Athena/Nyathena/Akashi, NOT Whisker, NOT until a software is picked.
	for _, sw := range []ServerSoftware{SoftwareTsuserver, SoftwareAthena, SoftwareNyathena, SoftwareAkashi} {
		if CMClaim(sw) != "/cm" || CMRelease(sw) != "/uncm" {
			t.Errorf("%v: want /cm + /uncm, got %q + %q", sw, CMClaim(sw), CMRelease(sw))
		}
	}
	if CMClaim(SoftwareWhisker) != "" || CMRelease(SoftwareWhisker) != "" {
		t.Error("Whisker has no /cm model — CMClaim/CMRelease must be blank")
	}
	if CMClaim(SoftwareUnknown) != "" {
		t.Error("Unknown software must not offer /cm until the user picks one")
	}
	// Lock/unlock are universal across the four known families.
	for _, sw := range []ServerSoftware{SoftwareTsuserver, SoftwareAthena, SoftwareAkashi, SoftwareWhisker} {
		if LockArea(sw) != "/lock" || UnlockArea(sw) != "/unlock" {
			t.Errorf("%v: want /lock + /unlock, got %q + %q", sw, LockArea(sw), UnlockArea(sw))
		}
	}
	if LockArea(SoftwareUnknown) != "" {
		t.Error("Unknown software must not offer /lock until the user picks one")
	}
	// Area-kick varies: /kickarea (Athena) vs /area_kick (KFO/Akashi); Whisker has none.
	if got := AreaKick(SoftwareAthena, "12"); got != "/kickarea 12" {
		t.Errorf("Athena area-kick = %q, want /kickarea 12", got)
	}
	if got := AreaKick(SoftwareNyathena, "12"); got != "/kickarea 12" {
		t.Errorf("Nyathena area-kick = %q, want /kickarea 12", got)
	}
	if got := AreaKick(SoftwareTsuserver, "12"); got != "/area_kick 12" {
		t.Errorf("KFO area-kick = %q, want /area_kick 12", got)
	}
	if got := AreaKick(SoftwareAkashi, "12"); got != "/area_kick 12" {
		t.Errorf("Akashi area-kick = %q, want /area_kick 12", got)
	}
	if AreaKick(SoftwareWhisker, "12") != "" {
		t.Error("Whisker has no area-kick command")
	}
	if AreaKick(SoftwareAkashi, "") != "" {
		t.Error("a blank uid must refuse the area-kick")
	}
}

// TestCommandReference pins that every known software has a non-empty reference and the unknown
// one prompts the user to pick (the dashboard's "look at the server's commands" panel).
func TestCommandReference(t *testing.T) {
	for sw := SoftwareUnknown; sw < ServerSoftwareCount; sw++ {
		if len(CommandReference(sw)) == 0 {
			t.Errorf("%v: empty command reference", sw)
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
	// Longer presets (#130 follow-up): 3 weeks short/human, and 1 month → a universal day count.
	if got := durationToken(SoftwareAthena, Ban3Weeks); got != "3w" {
		t.Errorf("Athena 3 weeks = %q, want '3w'", got)
	}
	if got := durationToken(SoftwareTsuserver, Ban3Weeks); got != "3 weeks" {
		t.Errorf("KFO 3 weeks = %q, want '3 weeks'", got)
	}
	if got := durationToken(SoftwareTsuserver, Ban1Month); got != "30 days" {
		t.Errorf("KFO 1 month = %q, want '30 days' (months parse inconsistently)", got)
	}
	if got := durationToken(SoftwareAkashi, Ban1Month); got != "30d" {
		t.Errorf("Akashi 1 month = %q, want '30d'", got)
	}
}
