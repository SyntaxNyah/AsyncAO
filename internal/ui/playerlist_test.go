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
