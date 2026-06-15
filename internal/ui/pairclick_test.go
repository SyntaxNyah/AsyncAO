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
