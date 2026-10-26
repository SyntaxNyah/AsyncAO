package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestDashDetection pins the #130 wire-signal detection: software (ID packet, with override),
// mod status (AUTH), and CM status (ARUP CM column names us).
func TestDashDetection(t *testing.T) {
	a := &App{}
	a.sess = courtroom.NewRehearsalSession("", nil)

	// Software: auto-detect from the ID-packet string, then the override wins.
	a.sess.Software = "Akashi 1.8"
	if a.detectedSoftware() != courtroom.SoftwareAkashi {
		t.Errorf("auto-detect = %v, want Akashi", a.detectedSoftware())
	}
	if !a.dashSoftwareKnown() {
		t.Error("Akashi should count as known")
	}
	a.cmSoftwareOverride = courtroom.SoftwareWhisker
	if a.detectedSoftware() != courtroom.SoftwareWhisker {
		t.Error("override didn't win over auto-detect")
	}
	a.cmSoftwareOverride = courtroom.SoftwareUnknown // back to auto

	// Mod status from the AUTH flag.
	if a.amIMod() {
		t.Error("not a mod before AUTH")
	}
	a.sess.ModGranted = true
	if !a.amIMod() {
		t.Error("ModGranted not reflected by amIMod")
	}

	// CM status from the ARUP CM column for our area.
	a.sess.Areas = []string{"Lobby", "Court 1"}
	a.sess.AreaInfo = []courtroom.AreaInfo{{}, {CM: "Phoenix Wright"}}
	a.curArea = "Court 1"
	a.shownameOverride = "Phoenix" // effectiveShowname returns this (no prefs needed)
	if !a.amICM() {
		t.Error("ARUP CM 'Phoenix Wright' should match showname 'Phoenix'")
	}
	a.sess.AreaInfo[1].CM = "Edgeworth"
	if a.amICM() {
		t.Error("another player's name must not read as us being CM")
	}
	a.sess.AreaInfo[1].CM = "FREE"
	if a.amICM() {
		t.Error("'FREE' (no CM) must not count")
	}
	// UID match: Athena/Nyathena write the CM column as "<char> (<uid>)", so our own client uid
	// (PlayerID) in that parened form is an exact CM signal — even when the name doesn't match.
	a.sess.PlayerID = 7
	a.shownameOverride = "Zzz" // ensure ONLY the uid can match
	a.sess.AreaInfo[1].CM = "Apollo Justice (7)"
	if !a.amICM() {
		t.Error("our uid in the '(7)' form should read as us being CM")
	}
	a.sess.AreaInfo[1].CM = "Apollo Justice (17)" // a different uid that merely contains '7'
	if a.amICM() {
		t.Error("uid 7 must not match '(17)' — the parens fence the number off")
	}
	a.sess.PlayerID = 0 // reset so the substring-only checks below aren't affected

	// A non-matching identity reads as not-CM (no false positive).
	a.shownameOverride = "Nobody"
	a.sess.AreaInfo[1].CM = "Apollo"
	if a.amICM() {
		t.Error("a non-matching name must not read as us being CM")
	}
}
