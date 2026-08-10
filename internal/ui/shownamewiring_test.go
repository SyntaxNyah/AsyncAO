package ui

// The App half of the char.ini showname chain (see
// internal/courtroom/shownamechain_test.go for the chain itself).
//
// The rung exists in internal/courtroom, but a client that never WIRES it is
// exactly as broken as one that never had it — and the wiring is invisible: the
// plate falls back to the folder name, which is a legitimate answer, so nothing
// errors and nothing looks obviously wrong. That is the parsed-but-never-applied
// shape CLAUDE.md names, and it is why these gates are here.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestEveryRoomGetsTheShownameRung pins the wiring at the ONE sanctioned funnel.
// wireRoomCharMeta is where a room is handed the char.ini-backed callbacks; a
// room that leaves this one nil silently loses the middle rung of the name chain.
func TestEveryRoomGetsTheShownameRung(t *testing.T) {
	a := testTabApp(t)
	room := &courtroom.Courtroom{}
	a.wireRoomCharMeta(room)
	if room.IniShownameFor == nil {
		t.Fatal("wireRoomCharMeta leaves IniShownameFor nil — the name plate cannot reach the " +
			"char.ini showname and every showname-less message wears its folder name")
	}
	// The siblings it rides with must still be wired: it shares their single fetch,
	// so an accidental swap that dropped one would cost a network probe as well as
	// the feature.
	if room.BlipNameFor == nil || room.ChatSkinFor == nil || room.SpriteScaling == nil {
		t.Error("wireRoomCharMeta dropped one of the char.ini callbacks the showname rung shares its fetch with")
	}
	// nil room is a no-op, not a panic (the guard every wire* helper keeps).
	a.wireRoomCharMeta(nil)
}

// TestShownameRungIsUnknownUntilTheFetchLands pins the streaming contract the
// two-value callback exists for. `known` must be the fetch's `done` flag and NOT
// "the string is non-empty": a character that declares needs_showname=false
// answers a real EMPTY name, and reading emptiness as "not fetched" would put
// the folder name back on exactly the characters that asked for none.
func TestShownameRungIsUnknownUntilTheFetchLands(t *testing.T) {
	a := testTabApp(t)
	// A real origin, because charMetaFor keys by the full char.ini URL and answers
	// nothing at all without one — the same per-server separation every cache in
	// this client is built on.
	a.urls = courtroom.NewURLBuilder("http://assets.example.test/base/")
	url := a.charINIURL("Phoenix")
	// The IN-FLIGHT marker charMetaFor plants on a miss: the entry exists, the
	// fetch has not settled. Seeded rather than provoked so this gate measures the
	// contract instead of starting a network goroutine.
	a.charMetaCache = map[string]charMeta{url: {}}

	if name, known := a.remoteIniShownameFor("Phoenix"); known {
		t.Fatalf("an unfetched character answered %q as KNOWN", name)
	}
	if got := courtroom.DisplayName(&protocol.ChatMessage{CharName: "Phoenix"}, false, a.remoteIniShownameFor); got != "Phoenix" {
		t.Errorf("unresolved chain = %q, want the folder name", got)
	}

	// A settled fetch that resolved to a real name.
	a.charMetaCache[url] = charMeta{showname: "Nick", done: true}
	if name, known := a.remoteIniShownameFor("Phoenix"); !known || name != "Nick" {
		t.Fatalf("resolved rung = (%q, %v), want (\"Nick\", true)", name, known)
	}

	// A settled fetch whose answer is a deliberate BLANK (needs_showname=false).
	a.charMetaCache[a.charINIURL("Narration")] = charMeta{showname: "", done: true}
	name, known := a.remoteIniShownameFor("Narration")
	if !known {
		t.Fatal("a needs_showname=false character reads as UNKNOWN — `known` is testing the " +
			"string instead of the fetch, so a blank plate becomes a folder name")
	}
	if name != "" {
		t.Fatalf("blank rung = %q, want the empty string", name)
	}
}

// TestTheICLogUsesTheSameNameChainAsThePlate is the drift catcher. The plate and
// the log are two surfaces in two packages and they had both quietly implemented
// the same TWO of canon's three rungs. Canon draws the log through get_showname
// too (log_chatmessage, courtroom.cpp:2528-2538), so they must resolve
// identically for the same message.
func TestTheICLogUsesTheSameNameChainAsThePlate(t *testing.T) {
	ini := func(char string) (string, bool) { return "Nekomaru Nidai", true }
	m := &protocol.ChatMessage{CharName: "DR2/Nekomaru Nidai", Message: "GO GO GO!"}

	if got := icSpeakerName(m, false, ini); got != "Nekomaru Nidai" {
		t.Fatalf("log speaker = %q, want the char.ini showname the plate shows", got)
	}
	if got := courtroom.DisplayName(m, false, ini); got != icSpeakerName(m, false, ini) {
		t.Fatalf("the log (%q) and the plate (%q) disagree about the same message",
			icSpeakerName(m, false, ini), got)
	}
	line, speaker := icLogLineDisplay(m, false, "", ini)
	if speaker != "Nekomaru Nidai" || line != "Nekomaru Nidai: GO GO GO!" {
		t.Fatalf("log line = %q / speaker %q", line, speaker)
	}
	// A wire showname still wins in the log too, and force-char-names still drops
	// to the character's own declared name rather than to the folder.
	withShowname := &protocol.ChatMessage{CharName: "DR2/Nekomaru Nidai", Showname: "Coach", Message: "hi"}
	if got := icSpeakerName(withShowname, false, ini); got != "Coach" {
		t.Errorf("log speaker = %q, want the sender's own showname", got)
	}
	if got := icSpeakerName(withShowname, true, ini); got != "Nekomaru Nidai" {
		t.Errorf("force-char log speaker = %q, want the character's declared name", got)
	}
	// No rung available: the folder, exactly as before this change.
	if got := icSpeakerName(m, false, nil); got != "DR2/Nekomaru Nidai" {
		t.Errorf("nil rung = %q, want the folder name", got)
	}
}

// TestTheLiveLogPassesTheShownameRung is the deletion catcher for the ONE line
// that connects them. handleSessionEvents could pass nil forever and every gate
// above would stay green while the log went back to folder names — which is the
// half-fixed state this change exists to make impossible.
func TestTheLiveLogPassesTheShownameRung(t *testing.T) {
	body := funcBodySource(t, "app.go", "handleSessionEvents")
	if !containsCall(body, "icLogLineDisplay") {
		t.Fatal("handleSessionEvents no longer builds its log line through icLogLineDisplay")
	}
	// readsIdent, not containsCall: the rung is passed as a METHOD VALUE, which is
	// an argument rather than a call, so a call census cannot see it at all.
	if !readsIdent(body, "remoteIniShownameFor") {
		t.Fatal("the live IC log is built without the char.ini showname rung — the log and " +
			"the name plate will show different names for the same message")
	}
}
