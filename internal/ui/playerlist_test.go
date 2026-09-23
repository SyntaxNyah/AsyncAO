package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
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
	a.rosterLegacy = true // snapshot path: rosterView() == areaPlayers
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
	a.rosterLegacy = true // snapshot path: rosterView() == areaPlayers
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

// TestPlayerRosterRowsAreaSort pins the Rooms button: the /gas area GROUPS reorder
// by mode — default keeps the server's /gas (parse) order, A→Z sorts by name, and
// "Most" puts the fullest room first. Flipping the mode between calls also proves
// the grouped-rows memo invalidates on playerAreaSort (else the cached order leaks).
func TestPlayerRosterRowsAreaSort(t *testing.T) {
	a := &App{}
	a.rosterLegacy = true // snapshot path: rosterView() == areaPlayers
	// /gas parse order: Courtroom 2 (3 players), Basement (1), Courtroom 1 (2).
	a.areaPlayers = []areaPlayer{
		{uid: "0", name: "a", area: "Courtroom 2"},
		{uid: "1", name: "b", area: "Courtroom 2"},
		{uid: "2", name: "c", area: "Courtroom 2"},
		{uid: "3", name: "d", area: "Basement"},
		{uid: "4", name: "e", area: "Courtroom 1"},
		{uid: "5", name: "f", area: "Courtroom 1"},
	}
	a.areaListAt = time.Now()
	a.playerSort = playerSortUID

	headerAreas := func() []string {
		var out []string
		for _, rw := range a.playerRosterRows("") {
			if rw.header {
				out = append(out, rw.area)
			}
		}
		return out
	}

	a.playerAreaSort = areaSortGas
	if got := headerAreas(); !equalStrings(got, []string{"Courtroom 2", "Basement", "Courtroom 1"}) {
		t.Errorf("/gas order = %v, want [Courtroom 2 Basement Courtroom 1]", got)
	}
	a.playerAreaSort = areaSortName
	if got := headerAreas(); !equalStrings(got, []string{"Basement", "Courtroom 1", "Courtroom 2"}) {
		t.Errorf("A→Z order = %v, want [Basement Courtroom 1 Courtroom 2]", got)
	}
	a.playerAreaSort = areaSortPop
	if got := headerAreas(); !equalStrings(got, []string{"Courtroom 2", "Courtroom 1", "Basement"}) {
		t.Errorf("most-populated = %v, want [Courtroom 2(3) Courtroom 1(2) Basement(1)]", got)
	}
}

// TestPlayerRosterRowsLiveUsesGasOrder pins the bug fix: on the LIVE (PR/PU)
// roster — which is UID-ordered — the grouped area headers must NOT follow the
// lowest-UID player's area. They follow OUR area first, then the server's /gas
// order (from the areaPlayers snapshot). Reproduces the report: UID 0 (Marty) in
// "Pizza Room 1" must not sit above the "Lobby" we're standing in.
func TestPlayerRosterRowsLiveUsesGasOrder(t *testing.T) {
	a := &App{}
	a.livePlayersOn = true // rosterView() == liveRoster (UID order)
	a.liveRoster = []areaPlayer{
		{uid: "0", name: "marty", area: "Pizza Room 1"}, // lowest UID, different area
		{uid: "1", name: "miles", area: "Lobby"},
		{uid: "2", name: "pam", area: "Movie Studio 1"},
	}
	a.liveRosterAt = time.Now()
	// The /gas snapshot (areaPlayers) lists areas in the server's own order.
	a.areaPlayers = []areaPlayer{
		{uid: "1", area: "Lobby"},
		{uid: "2", area: "Movie Studio 1"},
		{uid: "0", area: "Pizza Room 1"},
	}
	a.areaListAt = time.Now()
	a.curArea = "Lobby" // sess is nil here, so myAreaName falls back to curArea
	a.playerSort = playerSortUID
	a.playerAreaSort = areaSortGas

	var headers []string
	for _, rw := range a.playerRosterRows("") {
		if rw.header {
			headers = append(headers, rw.area)
		}
	}
	if !equalStrings(headers, []string{"Lobby", "Movie Studio 1", "Pizza Room 1"}) {
		t.Errorf("default order = %v, want [Lobby, Movie Studio 1, Pizza Room 1] (our area + /gas order, NOT the UID-first-seen Pizza Room 1)", headers)
	}
}

// TestPlayerRosterRowsFlat pins that a single-area roster (/ga) yields no
// headers — just the flat sorted player rows.
func TestPlayerRosterRowsFlat(t *testing.T) {
	a := &App{}
	a.rosterLegacy = true // snapshot path: rosterView() == areaPlayers
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

// TestJumpToAreaSelectsAndSends pins the consolidated jump path: it moves the
// optimistic local selection (curArea) AND sends the MC area transfer, so every
// jump entry point (header click, cards, chips, auto-follow) updates the
// current-area highlight — the reported "click to jump doesn't select" bug.
// Per-area scrollback is OFF here (the default), so switchAreaScrollback no-ops
// and exactly one MC packet goes out.
func TestJumpToAreaSelectsAndSends(t *testing.T) {
	a := testTabApp(t)
	var sent []protocol.Packet
	a.sess = courtroom.NewSession(func(p protocol.Packet) error { sent = append(sent, p); return nil }, "")

	a.jumpToArea("Courtroom 2")

	if a.curArea != "Courtroom 2" {
		t.Errorf("jump must set the optimistic selection, curArea = %q", a.curArea)
	}
	if len(sent) != 1 || sent[0].Header != "MC" {
		t.Fatalf("want exactly one MC transfer out, got %+v", sent)
	}
	if sent[0].Field(0) != "Courtroom 2" {
		t.Errorf("MC must carry the target area, field0 = %q", sent[0].Field(0))
	}
}

// TestJumpToAreaDrivesScrollback pins that jumpToArea runs switchAreaScrollback
// BEFORE curArea moves: the outgoing area's log is keyed on the OLD curArea, so
// jumping away saves it under the previous name and jumping back restores it.
// If the order were reversed, the save would land under the new area and the
// old log would be lost.
func TestJumpToAreaDrivesScrollback(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetPerAreaScrollback(true)
	a.sess = courtroom.NewSession(func(protocol.Packet) error { return nil }, "")
	a.curArea = "C1"
	a.icLog = []icEntry{{text: "c1 line"}}

	a.jumpToArea("C2") // saves "c1 line" under the OLD curArea (C1), loads empty C2
	if len(a.icLog) != 0 {
		t.Fatalf("entering a fresh area must start empty, got %d", len(a.icLog))
	}
	if a.curArea != "C2" {
		t.Fatalf("jump must advance curArea, got %q", a.curArea)
	}

	a.jumpToArea("C1") // back to C1 → its saved line returns
	if len(a.icLog) != 1 || a.icLog[0].text != "c1 line" {
		t.Fatalf("returning to an area must restore its log, got %v", a.icLog)
	}
}

// TestJumpToAreaGuards pins the early-return: a nil session or an empty area
// name leaves the selection and the IC log untouched (no half-applied jump).
func TestJumpToAreaGuards(t *testing.T) {
	// nil session: nothing to transfer to.
	a := testTabApp(t)
	a.d.Prefs.SetPerAreaScrollback(true)
	a.curArea = "orig"
	a.icLog = []icEntry{{text: "keep me"}}
	a.jumpToArea("Elsewhere")
	if a.curArea != "orig" || len(a.icLog) != 1 || a.icLog[0].text != "keep me" {
		t.Errorf("nil sess must no-op, got curArea=%q log=%v", a.curArea, a.icLog)
	}

	// Live session but empty area name (the guard's other half).
	a.sess = courtroom.NewSession(func(protocol.Packet) error { return nil }, "")
	a.jumpToArea("")
	if a.curArea != "orig" || len(a.icLog) != 1 || a.icLog[0].text != "keep me" {
		t.Errorf("empty area must no-op, got curArea=%q log=%v", a.curArea, a.icLog)
	}
}

// scrollFollowTestApp builds a grouped roster with a known geometry for the
// scroll-into-view math: at playerPct=100 a player row is playerRowH (44) and a
// header is playerHeaderH (26). Rows come out as
//
//	row0 hdr Lobby         top=0   h=26
//	row1 player (phoenix)  top=26  h=44
//	row2 player (maya)     top=70  h=44
//	row3 hdr Pizza Room 3  top=114 h=26
//	row4 player (kanade)   top=140 h=44
//
// so "Lobby" is the above-the-viewport header (top 0) and "Pizza Room 3" the
// below-it header (top 114) — the two nudge directions.
func scrollFollowTestApp() *App {
	a := &App{}
	a.rosterLegacy = true // snapshot path: rosterView() == areaPlayers
	a.playerPct = 100     // zero value would make player rows 0-high and collapse the geometry
	a.areaPlayers = []areaPlayer{
		{uid: "0", name: "phoenix", area: "Lobby"},
		{uid: "5", name: "maya", area: "Lobby"},
		{uid: "3", name: "kanade", area: "Pizza Room 3"},
	}
	a.areaListAt = time.Now()
	a.playerSort = playerSortUID
	return a
}

// TestScrollAreaIntoView pins the nudge math: a header below the viewport pulls
// the scroll down just enough to reveal it (top+headerH-viewH), one above pulls up
// to its top, an already-visible header is left alone, the scroll never goes
// negative, and an unknown/absent area is a clean no-op.
func TestScrollAreaIntoView(t *testing.T) {
	const (
		lobbyTop = int32(0)   // header rows: Lobby at content-space top 0
		pizzaTop = int32(114) // Pizza Room 3 header at top 114
	)
	cases := []struct {
		name    string
		area    string
		start   int32
		viewH   int32
		want    int32
		wantMsg string
	}{
		{
			name: "below viewport pulls down to reveal", area: "Pizza Room 3",
			start: 0, viewH: 100,
			// pizzaTop(114)+headerH(26) - viewH(100) = 40
			want: pizzaTop + playerHeaderH - 100, wantMsg: "scroll to bottom-align the header",
		},
		{
			name: "above viewport pulls up to its top", area: "Lobby",
			start: 60, viewH: 100,
			want: lobbyTop, wantMsg: "scroll up to the header's top",
		},
		{
			name: "already fully visible is unchanged", area: "Pizza Room 3",
			start: 50, viewH: 200, // header 114..140 fits inside 50..250
			want: 50, wantMsg: "no nudge when it already fits",
		},
		{
			name: "clamp keeps scroll non-negative", area: "Lobby",
			start: -30, viewH: 100, // Lobby top 0 < scroll(-30)? no (0 > -30) → below branch would push down; clamp still ≥0
			want: 0, wantMsg: "scroll clamps at 0",
		},
		{
			name: "unknown area is a no-op", area: "Nowhere",
			start: 77, viewH: 100,
			want: 77, wantMsg: "leave the scroll alone for a missing header",
		},
		{
			name: "empty area is a no-op", area: "",
			start: 77, viewH: 100,
			want: 77, wantMsg: "empty target leaves the scroll alone",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := scrollFollowTestApp()
			rows := a.playerRosterRows("")
			a.playerScroll = tc.start
			a.scrollAreaIntoView(tc.area, rows, tc.viewH)
			if a.playerScroll != tc.want {
				t.Errorf("%s: playerScroll = %d, want %d", tc.wantMsg, a.playerScroll, tc.want)
			}
		})
	}
}

// TestScrollAreaIntoViewFlatRoster pins that a flat /ga roster (no area headers)
// is a no-op: there's no header to scroll to, so the follow leaves the scroll put.
func TestScrollAreaIntoViewFlatRoster(t *testing.T) {
	a := &App{}
	a.rosterLegacy = true
	a.playerPct = 100
	a.areaPlayers = []areaPlayer{ // single area → flat rows, no headers
		{uid: "0", name: "phoenix", area: "Lobby"},
		{uid: "1", name: "maya", area: "Lobby"},
	}
	a.areaListAt = time.Now()
	rows := a.playerRosterRows("")
	a.playerScroll = 42
	a.scrollAreaIntoView("Lobby", rows, 100)
	if a.playerScroll != 42 {
		t.Errorf("flat roster must not scroll (no headers), playerScroll = %d, want 42", a.playerScroll)
	}
}

// TestApplyAreaJumpFollowLifecycle pins the latch: while armed and before the
// deadline it nudges the target header into view; once the deadline passes it
// disarms (both fields cleared) and leaves the scroll alone; disarmed it's a
// no-op. Uses frameNow to drive a.now() deterministically.
func TestApplyAreaJumpFollowLifecycle(t *testing.T) {
	base := time.Unix(1_000, 0)

	t.Run("armed and live nudges", func(t *testing.T) {
		a := scrollFollowTestApp()
		a.frameNow = base
		rows := a.playerRosterRows("")
		a.areaJumpFollow = "Pizza Room 3"
		a.areaJumpFollowUntil = base.Add(time.Second) // still in the window
		a.playerScroll = 0
		a.applyAreaJumpFollow(rows, 100)
		if want := int32(114) + playerHeaderH - 100; a.playerScroll != want {
			t.Errorf("armed+live must nudge, playerScroll = %d, want %d", a.playerScroll, want)
		}
		if a.areaJumpFollow != "Pizza Room 3" {
			t.Errorf("still-live latch must stay armed, got %q", a.areaJumpFollow)
		}
	})

	t.Run("past deadline disarms and holds scroll", func(t *testing.T) {
		a := scrollFollowTestApp()
		a.frameNow = base
		rows := a.playerRosterRows("")
		a.areaJumpFollow = "Pizza Room 3"
		a.areaJumpFollowUntil = base // now is NOT before the deadline → expired
		a.playerScroll = 5
		a.applyAreaJumpFollow(rows, 100)
		if a.playerScroll != 5 {
			t.Errorf("expired latch must not move the scroll, playerScroll = %d, want 5", a.playerScroll)
		}
		if a.areaJumpFollow != "" || !a.areaJumpFollowUntil.IsZero() {
			t.Errorf("expiry must clear the latch, got %q / %v", a.areaJumpFollow, a.areaJumpFollowUntil)
		}
	})

	t.Run("disarmed is a no-op", func(t *testing.T) {
		a := scrollFollowTestApp()
		a.frameNow = base
		rows := a.playerRosterRows("")
		a.areaJumpFollow = "" // disarmed
		a.playerScroll = 9
		a.applyAreaJumpFollow(rows, 100)
		if a.playerScroll != 9 {
			t.Errorf("disarmed latch must be a no-op, playerScroll = %d, want 9", a.playerScroll)
		}
	})
}

// TestJumpToAreaArmsFollow pins that jumping arms the scroll-follow latch with the
// target area and a deadline one window out — the state the per-frame nudge reads.
// Driven through jumpToArea (with a live session, or the guard returns early) so the
// arming site, not just the field, is covered.
func TestJumpToAreaArmsFollow(t *testing.T) {
	a := testTabApp(t)
	base := time.Unix(2_000, 0)
	a.frameNow = base
	a.sess = courtroom.NewSession(func(protocol.Packet) error { return nil }, "")

	a.jumpToArea("Courtroom 2")

	if a.areaJumpFollow != "Courtroom 2" {
		t.Errorf("jump must arm the follow with the target, got %q", a.areaJumpFollow)
	}
	if want := base.Add(areaJumpFollowWindow); !a.areaJumpFollowUntil.Equal(want) {
		t.Errorf("follow deadline = %v, want now+window %v", a.areaJumpFollowUntil, want)
	}
}
