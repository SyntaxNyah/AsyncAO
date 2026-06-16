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

// TestParseAreaRealFormat pins a real mod /getarea block: a "[Title]" tag BEFORE
// the UID bracket, a "(pos)" tag after the char name, a Showname line per player,
// and a unicode showname. (The "speaker:" prefix is stripped upstream in pushOOC.)
func TestParseAreaRealFormat(t *testing.T) {
	a := &App{}
	// A verbatim slice of the user's live /ga output: header lines, "[Title]" tags
	// before the UID, "(pos)" suffixes, per-player "Showname:"/"OOC:" lines, and a
	// unicode showname.
	block := "Players\n" +
		"----------\n" +
		"Lobby:\n" +
		"21 players online.\n" +
		"[0] mikako kurokawa_hd\n" +
		"Showname: mikako kurokawa_hd\n" +
		"[2] m16a1 (gfl)\n" +
		"Showname: fünfzehn\n" +
		"[3] klavier (aj)\n" +
		"[Sparkle] [5] oneshot niko\n" +
		"Showname: The Pilot (the 2nd server cat)\n" +
		"[11] ciel-sensei (tsukihime)\n" +
		"Showname: Häschen\n" +
		"[Nakama] [12] hina sorasaki (ba)\n" +
		"Showname: Peen (🍅)\n" +
		"OOC: Peen\n" +
		"[Mario Kart Queen] [24] tlaloc (fgo)\n" +
		"Showname: [Poki]\n"
	a.parseAreaBlock(block)

	for k, want := range map[string]string{
		"häschen":      "11", // showname → the player you'd double-click
		"ciel-sensei":  "11", // char name (pos suffix stripped)
		"tlaloc":       "24", // "[Mario Kart Queen] [24] tlaloc" — past the title tag
		"[poki]":       "24", // bracketed showname
		"oneshot niko": "5",  // "[Sparkle] [5] oneshot niko"
		"m16a1":        "2",
		"fünfzehn":     "2", // unicode showname
	} {
		if got := a.areaUIDs[k]; got != want {
			t.Errorf("areaUIDs[%q] = %q, want %q", k, got, want)
		}
	}
	// Header / OOC lines must NOT pollute the map.
	for _, junk := range []string{"players", "lobby:", "21 players online.", "peen"} {
		if _, ok := a.areaUIDs[junk]; ok {
			t.Errorf("header/OOC junk %q leaked into the UID map", junk)
		}
	}
	if got := len(a.areaPlayers); got != 7 { // [0] [2] [3] [5] [11] [12] [24]
		t.Errorf("roster rows = %d, want 7 (shownames are aliases, not rows)", got)
	}
	// The roster carries the showname so the picker shows the recognisable name.
	var ciel areaPlayer
	for _, p := range a.areaPlayers {
		if p.uid == "11" {
			ciel = p
		}
	}
	if ciel.name != "ciel-sensei" || ciel.showname != "Häschen" {
		t.Errorf("roster uid 11 = {name:%q showname:%q}, want {ciel-sensei, Häschen}", ciel.name, ciel.showname)
	}
}

// TestParseAreaRosterIdentity pins the player-list identity model: one row PER
// UID (same-named players don't collapse), a header REPLACES the snapshot (no
// stale/recycled-UID accumulation), and OOC/IPID attach to their player.
func TestParseAreaRosterIdentity(t *testing.T) {
	a := &App{}
	a.parseAreaBlock("----------\n" +
		"3 players online.\n" +
		"[10] Spectator\n" +
		"[12] hina sorasaki (ba)\n" +
		"Showname: Peen\n" +
		"OOC: PeenOOC\n" +
		"IPID: ABC123\n" +
		"[17] Spectator\n")
	if got := len(a.areaPlayers); got != 3 {
		t.Fatalf("roster = %d, want 3 (the two Spectators must NOT collapse)", got)
	}
	if a.areaPlayers[0].uid != "10" || a.areaPlayers[2].uid != "17" {
		t.Errorf("spectator rows = %q,%q, want 10,17 (distinct)", a.areaPlayers[0].uid, a.areaPlayers[2].uid)
	}
	if h := a.areaPlayers[1]; h.showname != "Peen" || h.ooc != "PeenOOC" || h.ipid != "ABC123" {
		t.Errorf("hina = {showname:%q ooc:%q ipid:%q}, want {Peen, PeenOOC, ABC123}", h.showname, h.ooc, h.ipid)
	}

	// A header REPLACES (recycled UIDs must not ghost): re-fetch with new people.
	a.parseAreaBlock("----------\n2 players online.\n[0] phoenix\n[1] edgeworth\n")
	if got := len(a.areaPlayers); got != 2 {
		t.Errorf("after re-fetch roster = %d, want 2 (replaced, not accumulated)", got)
	}
}

// TestParseAreaGasGroups pins /gas multi-area parsing (a verbatim slice of the
// user's Skrapegropen /gas): the FIRST "----" block replaces the roster, each
// LATER block accumulates as its own area, players carry their area name, and
// the "N players online." counts + trailing "N empty area(s) hidden." footer add
// nobody.
func TestParseAreaGasGroups(t *testing.T) {
	a := &App{}
	a.areaPlayers = []areaPlayer{{uid: "99", name: "stale ghost"}} // a prior snapshot the /gas must replace
	a.pairAreaReset = true
	block := "Players\n" +
		"----------\n" +
		"Lobby:\n" +
		"3 players online.\n" +
		"[0] 2 anonymous\n" +
		"[Coffee Brewer] [2] sinclair lc\n" +
		"Showname: Cocoa Bean\n" +
		"[19] meursault lc\n" +
		"Showname: Si Yang\n" +
		"----------\n" +
		"Pizza Room 3:\n" +
		"2 players online.\n" +
		"[16] kanade_hd\n" +
		"[28] diana venicia_eg\n" +
		"----------\n" +
		"Teto Cafe:\n" +
		"2 players online.\n" +
		"[14] mahiru_drs\n" +
		"OOC: web999\n" +
		"[15] seiko kimura_hd\n" +
		"OOC: tbh\n" +
		"----------\n" +
		"17 empty area(s) hidden.\n"
	a.parseAreaBlock(block)

	if got := len(a.areaPlayers); got != 7 { // 3 Lobby + 2 Pizza + 2 Teto; footer adds nobody
		t.Fatalf("roster = %d, want 7 (stale replaced, footer ignored)", got)
	}
	if a.areaPlayers[0].uid != "0" {
		t.Errorf("first row uid = %q, want 0 (the prior snapshot must be replaced)", a.areaPlayers[0].uid)
	}
	wantArea := map[string]string{
		"0": "Lobby", "2": "Lobby", "19": "Lobby",
		"16": "Pizza Room 3", "28": "Pizza Room 3",
		"14": "Teto Cafe", "15": "Teto Cafe",
	}
	for _, p := range a.areaPlayers {
		if want := wantArea[p.uid]; p.area != want {
			t.Errorf("uid %s area = %q, want %q", p.uid, p.area, want)
		}
		if p.uid == "14" && p.ooc != "web999" { // OOC attaches across the area boundary correctly
			t.Errorf("uid 14 ooc = %q, want web999", p.ooc)
		}
	}
	a.rosterLegacy = true // rosterMultiArea reads the active roster — the snapshot here
	if !a.rosterMultiArea() {
		t.Error("a /gas spanning 3 areas must read as multi-area")
	}
}

// TestParseAreaGaSingle pins that a single-area /ga (one "----" block) is NOT
// multi-area, so the player list stays flat (no group headers).
func TestParseAreaGaSingle(t *testing.T) {
	a := &App{}
	a.parseAreaBlock("Players\n----------\nLobby:\n2 players online.\n[0] phoenix\n[1] edgeworth\n")
	if len(a.areaPlayers) != 2 {
		t.Fatalf("roster = %d, want 2", len(a.areaPlayers))
	}
	if a.areaPlayers[0].area != "Lobby" {
		t.Errorf("area = %q, want Lobby", a.areaPlayers[0].area)
	}
	if a.rosterMultiArea() {
		t.Error("a single-area /ga must NOT read as multi-area")
	}
}

// TestLooksLikeAreaList pins that /getarea output reads as non-chat (so running
// /ga never self-pings your callword), while ordinary OOC — even mentioning your
// name — still does.
func TestLooksLikeAreaList(t *testing.T) {
	for _, s := range []string{
		"21 players online.",
		"Showname: Häschen",
		"OOC: Peen",
		"IPID: ABC123",
		"[11] ciel-sensei (tsukihime)",
		"[Mario Kart Queen] [24] tlaloc (fgo)",
	} {
		if !looksLikeAreaList(s) {
			t.Errorf("looksLikeAreaList(%q) = false, want true (/ga output)", s)
		}
	}
	for _, s := range []string{
		"hey Häschen how are you",
		"lol nice one",
		"check out [this] cool link",
		"",
	} {
		if looksLikeAreaList(s) {
			t.Errorf("looksLikeAreaList(%q) = true, want false (real chat)", s)
		}
	}
}
