package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestUIDLess pins that UIDs sort numerically (so "10" follows "2", not the
// lexical "10" < "2"), with a lexical fallback for the odd non-numeric id.
func TestUIDLess(t *testing.T) {
	if !uidLess("2", "10") {
		t.Error("uidLess(2,10) = false, want true (numeric)")
	}
	if uidLess("10", "2") {
		t.Error("uidLess(10,2) = true, want false")
	}
	if uidLess("x", "x") {
		t.Error("equal non-numeric must be false")
	}
}

// TestPlayerRosterOrder pins the three roster sorts: UID (numeric), Name
// (case-insensitive IC name), and Speaking (the current speaker floats up).
func TestPlayerRosterOrder(t *testing.T) {
	a := &App{}
	a.areaPlayers = []areaPlayer{
		{uid: "10", name: "zeta", showname: "Zed"},
		{uid: "2", name: "alpha"},
		{uid: "7", name: "mike", showname: "Mike"},
	}
	a.areaListAt = time.Now()

	a.playerSort = playerSortUID
	ord := a.playerRosterOrder("")
	if a.areaPlayers[ord[0]].uid != "2" || a.areaPlayers[ord[1]].uid != "7" || a.areaPlayers[ord[2]].uid != "10" {
		t.Errorf("UID order = [%s %s %s], want [2 7 10]",
			a.areaPlayers[ord[0]].uid, a.areaPlayers[ord[1]].uid, a.areaPlayers[ord[2]].uid)
	}

	a.playerSort = playerSortName
	ord = a.playerRosterOrder("")
	if a.areaPlayers[ord[0]].name != "alpha" || a.areaPlayers[ord[2]].showname != "Zed" {
		t.Errorf("Name order first=%q last=%q, want alpha…Zed",
			a.areaPlayers[ord[0]].name, a.areaPlayers[ord[2]].showname)
	}

	a.playerSort = playerSortSpeaking
	ord = a.playerRosterOrder("mike") // Mike's char name == the speaker
	if a.areaPlayers[ord[0]].name != "mike" {
		t.Errorf("Speaking order first=%q, want mike (the speaker)", a.areaPlayers[ord[0]].name)
	}
}

// TestPlayerRosterRowsGrouped pins the /gas grouped display: a header per area
// in parse order, that area's players under it (sorted by the active mode), and
// the player rows still index into areaPlayers (the icon-cache key).
func TestPlayerRosterRowsGrouped(t *testing.T) {
	a := &App{}
	a.areaPlayers = []areaPlayer{
		{uid: "0", name: "phoenix", area: "Lobby"},
		{uid: "5", name: "maya", area: "Lobby"},
		{uid: "3", name: "kanade", area: "Pizza Room 3"},
	}
	a.areaListAt = time.Now()
	a.playerSort = playerSortUID

	rows := a.playerRosterRows("") // [hdr Lobby(2)] [0] [5] [hdr Pizza(1)] [3]
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5 (2 headers + 3 players)", len(rows))
	}
	if !rows[0].header || rows[0].area != "Lobby" || rows[0].count != 2 {
		t.Errorf("row0 = %+v, want Lobby header count 2", rows[0])
	}
	if rows[1].header || a.areaPlayers[rows[1].idx].uid != "0" {
		t.Errorf("row1 = %+v, want player uid 0", rows[1])
	}
	if !rows[3].header || rows[3].area != "Pizza Room 3" || rows[3].count != 1 {
		t.Errorf("row3 = %+v, want Pizza Room 3 header count 1", rows[3])
	}
	if rows[4].header || a.areaPlayers[rows[4].idx].uid != "3" {
		t.Errorf("row4 = %+v, want player uid 3", rows[4])
	}
}

// TestPlayerRosterRowsFlat pins that a single-area roster (/ga) yields no
// headers — just the flat sorted player rows.
func TestPlayerRosterRowsFlat(t *testing.T) {
	a := &App{}
	a.areaPlayers = []areaPlayer{
		{uid: "5", name: "maya", area: "Lobby"},
		{uid: "0", name: "phoenix", area: "Lobby"},
	}
	a.areaListAt = time.Now()
	a.playerSort = playerSortUID
	rows := a.playerRosterRows("")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (no headers for one area)", len(rows))
	}
	for _, rw := range rows {
		if rw.header {
			t.Fatal("a single-area roster must have no group headers")
		}
	}
	if a.areaPlayers[rows[0].idx].uid != "0" { // UID sort
		t.Errorf("first row uid = %q, want 0", a.areaPlayers[rows[0].idx].uid)
	}
}

// TestRosterNameOOCFallback pins the display-name chain: showname, else the OOC
// name (an iniswapper with no showname), else the character.
func TestRosterNameOOCFallback(t *testing.T) {
	cases := []struct {
		p    areaPlayer
		want string
	}{
		{areaPlayer{name: "phoenix", showname: "Nick", ooc: "wright"}, "Nick"},
		{areaPlayer{name: "phoenix", ooc: "wright"}, "wright"}, // no showname → OOC
		{areaPlayer{name: "phoenix"}, "phoenix"},               // neither → character
	}
	for _, tc := range cases {
		if got := rosterName(&tc.p); got != tc.want {
			t.Errorf("rosterName(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

// TestCMNameSet pins that the CM marker source excludes ""/FREE and splits a
// multi-CM list into individual names.
func TestCMNameSet(t *testing.T) {
	a := &App{}
	a.sess = &courtroom.Session{AreaInfo: []courtroom.AreaInfo{
		{CM: "FREE"},
		{CM: "Phoenix, Edgeworth"},
		{CM: ""},
	}}
	set := a.cmNameSet()
	if !set["phoenix"] || !set["edgeworth"] {
		t.Errorf("cmNameSet = %v, want phoenix+edgeworth", set)
	}
	if set["free"] {
		t.Error("FREE must not be a CM name")
	}
}

// TestCurAreaPlayers pins the live ARUP count lookup: matched by area name,
// area-0 fallback on a fresh join, and ok=false when the server hasn't reported
// the count (-1).
func TestCurAreaPlayers(t *testing.T) {
	a := &App{}
	a.sess = &courtroom.Session{
		Areas:    []string{"Lobby", "Courtroom"},
		AreaInfo: []courtroom.AreaInfo{{Players: 5}, {Players: 2}},
	}
	a.curArea = "Courtroom"
	if n, ok := a.curAreaPlayers(); !ok || n != 2 {
		t.Errorf("Courtroom = %d,%v, want 2,true", n, ok)
	}
	a.curArea = "" // fresh join → assume the spawn area (index 0)
	if n, ok := a.curAreaPlayers(); !ok || n != 5 {
		t.Errorf("fresh join = %d,%v, want 5,true (area 0)", n, ok)
	}
	a.sess.AreaInfo[0].Players = -1 // server hasn't reported it
	if _, ok := a.curAreaPlayers(); ok {
		t.Error("Players=-1 must give ok=false")
	}
}
