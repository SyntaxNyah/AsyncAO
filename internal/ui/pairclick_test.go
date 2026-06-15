package ui

import "testing"

// TestParseAreaBlock pins the /getarea harvesting that feeds click-to-pair:
// "[uid] name" rows populate the map + ordered roster, a trailing "(showname)"
// is stripped, non-numeric/garbage rows are rejected, and pairAreaReset starts
// a fresh roster (the Refresh button) instead of accumulating stale players.
func TestParseAreaBlock(t *testing.T) {
	a := &App{}
	a.parseAreaBlock("=== Area 0 ===\n[0] Phoenix Wright\n[2] Miles Edgeworth (Edgey)\njust chatter\n[abc] bad uid")

	if got := a.areaUIDs["phoenix wright"]; got != "0" {
		t.Errorf("Phoenix uid = %q, want 0", got)
	}
	if got := a.areaUIDs["miles edgeworth"]; got != "2" { // trailing "(Edgey)" dropped
		t.Errorf("Edgeworth uid = %q, want 2", got)
	}
	if _, ok := a.areaUIDs["bad uid"]; ok {
		t.Error("[abc] must be rejected (non-numeric uid)")
	}
	if len(a.areaPlayers) != 2 {
		t.Fatalf("areaPlayers = %d, want 2", len(a.areaPlayers))
	}

	// Refresh path: pairAreaReset starts the roster over on the next block.
	a.pairAreaReset = true
	a.parseAreaBlock("[5] Franziska")
	if len(a.areaPlayers) != 1 || a.areaPlayers[0].uid != "5" {
		t.Errorf("after reset: players = %+v, want just {5 Franziska}", a.areaPlayers)
	}
	if _, ok := a.areaUIDs["phoenix wright"]; ok {
		t.Error("reset must clear the previous roster's map")
	}

	// Plain chat with no bracket fast-rejects (no panic, no spurious entries).
	a.parseAreaBlock("hey what's up everyone")
	if len(a.areaPlayers) != 1 {
		t.Errorf("non-getarea OOC must not add players, got %d", len(a.areaPlayers))
	}
}

// TestParseAreaShowname pins the verbose /getarea format: a "Showname:" line
// (inline "Showname: X" or the name on the next line) aliases that showname to
// the preceding [uid], so a double-clicked IC line (which displays the SHOWNAME)
// auto-fills the UID. Shownames are aliases, not extra roster rows.
func TestParseAreaShowname(t *testing.T) {
	a := &App{}
	a.parseAreaBlock("[21] dante pxz\nShowname:\niumiro\n[22] Klavier\nShowname: Gavin")
	if got := a.areaUIDs["iumiro"]; got != "21" {
		t.Errorf("next-line showname iumiro -> uid = %q, want 21", got)
	}
	if got := a.areaUIDs["gavin"]; got != "22" {
		t.Errorf("inline showname Gavin -> uid = %q, want 22", got)
	}
	if got := a.areaUIDs["dante pxz"]; got != "21" {
		t.Errorf("char name still maps: dante pxz -> %q, want 21", got)
	}
	if len(a.areaPlayers) != 2 {
		t.Errorf("roster = %d, want 2 (shownames are aliases, not rows)", len(a.areaPlayers))
	}
}
