package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestBuildLiveRoster pins the live-roster reconciliation: one row per taken
// character (showname merged from the IC cache), then anonymous Spectator rows
// for the head-count beyond those characters — so spectators come and go with
// the ARUP count even though CharsCheck can't name them.
func TestBuildLiveRoster(t *testing.T) {
	chars := []courtroom.CharacterSlot{
		{Name: "Phoenix", Taken: true},
		{Name: "Edgeworth", Taken: false}, // free → not in the roster
		{Name: "Franziska", Taken: true},
	}
	show := map[string]string{"phoenix": "Nick"}

	// Head-count 5: 2 taken characters + 3 spectators.
	got := buildLiveRoster(chars, 5, true, "Courtroom 1", show)
	if len(got) != 5 {
		t.Fatalf("roster len = %d, want 5 (2 chars + 3 spectators)", len(got))
	}
	if got[0].name != "Phoenix" || got[0].showname != "Nick" || got[0].area != "Courtroom 1" {
		t.Errorf("row0 = %+v, want Phoenix/Nick/Courtroom 1", got[0])
	}
	if got[1].name != "Franziska" || got[1].showname != "" {
		t.Errorf("row1 = %+v, want Franziska with no cached showname", got[1])
	}
	for i := 2; i < 5; i++ {
		if got[i].name != specName {
			t.Errorf("row%d name = %q, want %q", i, got[i].name, specName)
		}
	}

	// No ARUP count yet → characters only, no spectator rows.
	if n := len(buildLiveRoster(chars, 0, false, "", show)); n != 2 {
		t.Errorf("no-count roster len = %d, want 2 (chars only)", n)
	}
	// Head-count below the character count (stale ARUP) → never negative rows.
	if n := len(buildLiveRoster(chars, 1, true, "", show)); n != 2 {
		t.Errorf("stale-count roster len = %d, want 2 (no negative spectators)", n)
	}
}

// TestRosterEqual pins the change-detector that gates a rebuild (and the icon-
// cache invalidation it forces): identical rosters compare equal; a showname,
// membership, or length change does not.
func TestRosterEqual(t *testing.T) {
	a := []areaPlayer{{name: "Phoenix", showname: "Nick"}, {name: specName}}
	b := []areaPlayer{{name: "Phoenix", showname: "Nick"}, {name: specName}}
	if !rosterEqual(a, b) {
		t.Error("identical rosters must compare equal")
	}
	if rosterEqual(a, b[:1]) {
		t.Error("different lengths must not compare equal")
	}
	b[0].showname = "Wright"
	if rosterEqual(a, b) {
		t.Error("a showname change must not compare equal")
	}
}
