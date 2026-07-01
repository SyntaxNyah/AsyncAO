package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestWitchesIPIDFromPlayerlist pins the witches-akashi-party (WAP) fix: that fork
// streams a mod's target IPID inside the live PU name field as a trailing "(<hex>)"
// token, so the ban box can fill straight from the player list with no /getarea.
// The extraction is gated on the Akashi/WAP family and peels only an IPID-looking
// token, so an ordinary parenthesised OOC name (and every other server) is untouched.
// (../witches-akashi-party/src/playerstateobserver.cpp.)
func TestWitchesIPIDFromPlayerlist(t *testing.T) {
	newWAP := func() *Session {
		s := NewSession(func(protocol.Packet) error { return nil }, "hdid")
		s.Software = "WAP-Akashi" // the ID string the fork now announces → SoftwareWitches
		feed(t, s, "PR#5#0#%")
		return s
	}

	// 1. "name (ipid)" → OOC name kept, IPID peeled off.
	s := newWAP()
	feed(t, s, "PU#5#0#web71 (eea20f10)#%")
	if p := s.players[5]; p.OOCName != "web71" || p.IPID != "eea20f10" {
		t.Fatalf("name (ipid): got OOC %q IPID %q, want web71 / eea20f10", p.OOCName, p.IPID)
	}

	// 2. A bare "(ipid)" ADD follow-up sets the IPID but must NOT clobber the name.
	feed(t, s, "PU#5#0#(bbccddee)#%")
	if p := s.players[5]; p.OOCName != "web71" || p.IPID != "bbccddee" {
		t.Fatalf("bare (ipid): got OOC %q IPID %q, want web71 / bbccddee", p.OOCName, p.IPID)
	}

	// 3. An AFK prefix is opaque leading text — only the trailing token is the IPID.
	s = newWAP()
	feed(t, s, "PU#5#0#[AFK] web71 (eea20f10)#%")
	if p := s.players[5]; p.OOCName != "[AFK] web71" || p.IPID != "eea20f10" {
		t.Fatalf("afk prefix: got OOC %q IPID %q, want [AFK] web71 / eea20f10", p.OOCName, p.IPID)
	}

	// 4. A later clean-name update (no token) keeps the IPID sticky (mods still see it).
	feed(t, s, "PU#5#0#web71#%")
	if p := s.players[5]; p.OOCName != "web71" || p.IPID != "eea20f10" {
		t.Fatalf("sticky: got OOC %q IPID %q, want web71 / eea20f10 (IPID must persist)", p.OOCName, p.IPID)
	}

	// 5. A short hex-word in parens is NOT an IPID — the name stays intact.
	s = newWAP()
	feed(t, s, "PU#5#0#Bob (cafe)#%")
	if p := s.players[5]; p.OOCName != "Bob (cafe)" || p.IPID != "" {
		t.Fatalf("short token: got OOC %q IPID %q, want Bob (cafe) / empty", p.OOCName, p.IPID)
	}

	// 6. The gate: on any family that ISN'T WAP — including stock Akashi (which,
	// unlike WAP, never puts IPIDs in the player list) and Athena (which pushes PU
	// packets of its own) — an 8-hex parenthesised name is left verbatim, never
	// mis-read as an IPID (which BanCommand's -i flag could turn into a wrong ban).
	for _, sw := range []string{"Athena", "Akashi 1.8"} {
		sg := NewSession(func(protocol.Packet) error { return nil }, "hdid")
		sg.Software = sw
		feed(t, sg, "PR#5#0#%")
		feed(t, sg, "PU#5#0#coder (deadbeef)#%")
		if p := sg.players[5]; p.OOCName != "coder (deadbeef)" || p.IPID != "" {
			t.Fatalf("%s gate: got OOC %q IPID %q, want verbatim / empty", sw, p.OOCName, p.IPID)
		}
	}

	// 7. PR REMOVE drops the row, so a recycled UID starts clean (no IPID leak).
	feed(t, s, "PR#5#1#%")
	if _, ok := s.players[5]; ok {
		t.Fatal("PR REMOVE must delete the row so a recycled UID can't inherit the IPID")
	}
}

// TestSplitTrailingIPID unit-pins the pure peeler across the shapes WAP emits and
// the ones it must ignore.
func TestSplitTrailingIPID(t *testing.T) {
	for _, tc := range []struct {
		in, name, ipid string
	}{
		{"web71 (eea20f10)", "web71", "eea20f10"},
		{"(eea20f10)", "", "eea20f10"},
		{"[AFK] web71 (eea20f10)", "[AFK] web71", "eea20f10"},
		{"coder (dead) (deadbeef)", "coder (dead)", "deadbeef"}, // only the LAST token
		{"web71", "web71", ""},                                  // no token
		{"Bob (cafe)", "Bob (cafe)", ""},                        // too short to be an IPID
		{"Bob (not-hex!)", "Bob (not-hex!)", ""},                // not hex
		{"", "", ""},
	} {
		if name, ipid := splitTrailingIPID(tc.in); name != tc.name || ipid != tc.ipid {
			t.Errorf("splitTrailingIPID(%q) = %q,%q want %q,%q", tc.in, name, ipid, tc.name, tc.ipid)
		}
	}
}

// TestWitchesDetectionAndCommands pins that "WAP-Akashi" is detected as its own
// family and that its ban/kick syntax reuses Akashi's exact positional form.
func TestWitchesDetectionAndCommands(t *testing.T) {
	if got := DetectSoftware("WAP-Akashi"); got != SoftwareWitches {
		t.Fatalf("DetectSoftware(WAP-Akashi) = %v, want SoftwareWitches", got)
	}
	if got := DetectSoftware("Akashi 1.8"); got != SoftwareAkashi { // plain Akashi still Akashi
		t.Fatalf("DetectSoftware(Akashi) = %v, want SoftwareAkashi", got)
	}
	if got := BanCommand(SoftwareWitches, "eea20f10", "5", Ban1Week, "ban evading"); got != "/ban eea20f10 1w ban evading" {
		t.Fatalf("WAP ban = %q, want /ban eea20f10 1w ban evading", got)
	}
	if got := KickCommand(SoftwareWitches, "eea20f10", "5", "spam"); got != "/kick eea20f10 spam" {
		t.Fatalf("WAP kick = %q, want /kick eea20f10 spam", got)
	}
	if got := BanCommand(SoftwareWitches, "", "5", Ban1Week, "x"); got != "" {
		t.Fatalf("WAP ban without an IPID should refuse, got %q", got)
	}
}
