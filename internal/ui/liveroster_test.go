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
	// A prior /getarea snapshot enriches matching characters by name; its
	// Spectator rows fill the live spectator slots in order.
	snap := []areaPlayer{
		{uid: "3", name: "Phoenix", showname: "Wright", ooc: "nick_ooc", ipid: "AB12"},
		{uid: "5", name: "Franziska"},
		{uid: "9", name: specName, ooc: "lurker"},
	}

	// Head-count 5: 2 taken characters + 3 spectators.
	got := buildLiveRoster(chars, 5, true, "Courtroom 1", show, snap)
	if len(got) != 5 {
		t.Fatalf("roster len = %d, want 5 (2 chars + 3 spectators)", len(got))
	}
	// Phoenix inherits UID/IPID/OOC from the snapshot; the cached showname wins.
	if got[0].name != "Phoenix" || got[0].uid != "3" || got[0].ipid != "AB12" || got[0].ooc != "nick_ooc" || got[0].showname != "Nick" {
		t.Errorf("row0 = %+v, want Phoenix enriched (uid 3, ipid AB12), cached showname Nick", got[0])
	}
	if got[1].name != "Franziska" || got[1].uid != "5" || got[1].showname != "" {
		t.Errorf("row1 = %+v, want Franziska uid 5, no showname", got[1])
	}
	// First spectator slot uses the named snapshot row; the rest are anonymous.
	if got[2].name != specName || got[2].uid != "9" || got[2].ooc != "lurker" {
		t.Errorf("row2 = %+v, want the named snapshot spectator (uid 9)", got[2])
	}
	if got[3].name != specName || got[3].uid != "" || got[4].name != specName {
		t.Errorf("rows 3-4 = %+v %+v, want anonymous spectators", got[3], got[4])
	}

	// No ARUP count yet → characters only, no spectator rows.
	if n := len(buildLiveRoster(chars, 0, false, "", show, snap)); n != 2 {
		t.Errorf("no-count roster len = %d, want 2 (chars only)", n)
	}
	// Head-count below the character count (stale ARUP) → never negative rows.
	if n := len(buildLiveRoster(chars, 1, true, "", show, nil)); n != 2 {
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
	// A pure area move (PU type 3) changes ONLY the area field: it must count as
	// a change or the Players tab's room grouping freezes until a join/leave
	// (the playtest report).
	b[0].showname = "Nick"
	b[0].area = "Courtroom 2"
	if rosterEqual(a, b) {
		t.Error("an area move must not compare equal")
	}
}
