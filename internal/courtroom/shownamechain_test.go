package courtroom

// REGRESSION: the char.ini showname stopped reaching the name plate.
//
// Live report, 2026-08-10: "char.ini showname no longer displays — the folder
// name shows". The plate read `DR2/Nekomaru Nidai`, a nested character FOLDER,
// for a character whose char.ini names it `Nekomaru Nidai`.
//
// Canon is AOApplication::get_showname (text_file_functions.cpp:446-473), called
// from Courtroom::initialize_chatbox (courtroom.cpp:3283-3292) for the plate and
// from log_chatmessage (:2528-2538) for the log. AsyncAO implemented rungs 1 and
// 3 of that three-rung chain and never rung 2.
//
// These gates DRIVE the resolution — real ParseCharINI bytes through
// ShownameOrFolder into a real Courtroom's begin() — rather than restating the
// rule, because a mirror of the rule is exactly what would have kept passing
// while the plate showed a folder path.

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// iniShownameFromBytes is the PRODUCTION path a live client walks: parse the
// char.ini the server serves, then ask it get_showname's question. Nothing here
// re-implements the precedence — ShownameOrFolder owns it.
func iniShownameFromBytes(t *testing.T, ini string) IniShowname {
	t.Helper()
	parsed, err := ParseCharINI([]byte(ini))
	if err != nil {
		t.Fatalf("parsing the fixture char.ini: %v", err)
	}
	return func(char string) (string, bool) { return parsed.ShownameOrFolder(char), true }
}

// TestCharINIShownameReachesTheNamePlate is the reported defect, end to end: a
// message with NO wire showname, from a character whose char.ini declares one,
// must put the DECLARED name on the plate — not the folder it lives in.
func TestCharINIShownameReachesTheNamePlate(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.IniShownameFor = iniShownameFromBytes(t, "[Options]\nshowname = Nekomaru Nidai\n[Emotions]\nnumber = 0\n")

	room.RestoreMessage(&protocol.ChatMessage{
		CharName: "DR2/Nekomaru Nidai", // the nested folder the report showed
		Emote:    "normal",
		Message:  "GO GO GO!",
	})
	if got := room.Scene.ShownameText; got != "Nekomaru Nidai" {
		t.Fatalf("name plate = %q, want %q — the char.ini rung (get_showname, "+
			"text_file_functions.cpp:446) is not being consulted", got, "Nekomaru Nidai")
	}
	if strings.Contains(room.Scene.ShownameText, "/") {
		t.Error("the plate is still showing a folder PATH")
	}
}

// TestCustomShownameStillBeatsTheCharINI pins rung 1 against the fix: a sender
// who typed a showname must not have it overwritten by the character's ini.
// This is the assertion that would fail if the new rung were inserted above the
// wire field instead of below it.
func TestCustomShownameStillBeatsTheCharINI(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.IniShownameFor = iniShownameFromBytes(t, "[Options]\nshowname = Nekomaru Nidai\n")

	room.RestoreMessage(&protocol.ChatMessage{
		CharName: "DR2/Nekomaru Nidai",
		Showname: "The Ultimate Manager",
		Emote:    "normal",
		Message:  "hi",
	})
	if got := room.Scene.ShownameText; got != "The Ultimate Manager" {
		t.Fatalf("name plate = %q, want the sender's own showname", got)
	}
}

// TestShownamelessCharINIFallsBackToTheFolder pins rung 3: a char.ini that
// declares no showname answers the folder name, which is get_showname's own
// fallback (text_file_functions.cpp:470-472) and not a client shortcut.
func TestShownamelessCharINIFallsBackToTheFolder(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.IniShownameFor = iniShownameFromBytes(t, "[Options]\nblips = male\n[Emotions]\nnumber = 0\n")

	room.RestoreMessage(&protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Message: "hi"})
	if got := room.Scene.ShownameText; got != "Phoenix" {
		t.Fatalf("name plate = %q, want the folder name %q", got, "Phoenix")
	}
}

// TestNeedsShownameFalseDrawsABlankPlate pins the opt-out, which canon tests
// BEFORE the showname key (:465-468) and which is a real answer rather than a
// missing one: an empty plate, not the folder name. Downstream that is what
// selects the plate-less chatblank skin, so treating it as "unknown" would put
// both the name AND the wrong chatbox art back.
func TestNeedsShownameFalseDrawsABlankPlate(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.IniShownameFor = iniShownameFromBytes(t,
		"[Options]\nshowname = Narrator\nneeds_showname = false\n")

	room.RestoreMessage(&protocol.ChatMessage{CharName: "Narration", Emote: "normal", Message: "..."})
	if got := room.Scene.ShownameText; got != "" {
		t.Fatalf("name plate = %q, want a BLANK plate (needs_showname = false)", got)
	}
}

// TestUnresolvedCharINIDegradesToTheFolder pins the streaming-client gap the
// `known` flag exists for. AO2 reads the ini synchronously off disk and can
// always answer; we cannot, and a first message from an unseen character must
// show the folder name rather than an empty plate.
func TestUnresolvedCharINIDegradesToTheFolder(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.IniShownameFor = func(string) (string, bool) { return "", false } // fetch in flight

	room.RestoreMessage(&protocol.ChatMessage{CharName: "Edgeworth", Emote: "normal", Message: "hm"})
	if got := room.Scene.ShownameText; got != "Edgeworth" {
		t.Fatalf("name plate = %q, want the folder name while the char.ini is unresolved", got)
	}
	// …and with no callback wired at all (a direct NewCourtroom caller: replay,
	// export, the scene maker) the behaviour is identical.
	room.IniShownameFor = nil
	room.RestoreMessage(&protocol.ChatMessage{CharName: "Edgeworth", Emote: "normal", Message: "hm"})
	if got := room.Scene.ShownameText; got != "Edgeworth" {
		t.Fatalf("nil callback: name plate = %q, want the folder name", got)
	}
}

// TestForceCharNamesStillConsultsTheCharINI pins the interaction canon is
// explicit about and which is easy to get backwards: custom_shownames OFF
// suppresses what the SENDER claims, not what the CHARACTER declares —
// initialize_chatbox still calls get_showname in that arm (courtroom.cpp:3285).
func TestForceCharNamesStillConsultsTheCharINI(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.ForceCharNames = true
	room.IniShownameFor = iniShownameFromBytes(t, "[Options]\nshowname = Nekomaru Nidai\n")

	room.RestoreMessage(&protocol.ChatMessage{
		CharName: "DR2/Nekomaru Nidai",
		Showname: "Totally Phoenix Wright", // the impersonation force-char-names exists to stop
		Emote:    "normal",
		Message:  "hi",
	})
	if got := room.Scene.ShownameText; got != "Nekomaru Nidai" {
		t.Fatalf("force-char-names plate = %q, want the CHARACTER's own declared name", got)
	}
}

// TestShownameOrFolderIsGetShownameTransposed pins the helper itself against
// canon's table, including the order that matters (needs_showname is tested
// first) and the nil receiver an unreadable ini produces.
func TestShownameOrFolderIsGetShownameTransposed(t *testing.T) {
	for _, tc := range []struct {
		name, ini, folder, want string
	}{
		{"declared", "[Options]\nshowname = Nick\n", "Phoenix", "Nick"},
		{"absent", "[Options]\nblips = male\n", "Phoenix", "Phoenix"},
		{"empty value", "[Options]\nshowname =\n", "Phoenix", "Phoenix"},
		{"whitespace value", "[Options]\nshowname =    \n", "Phoenix", "Phoenix"},
		{"opted out", "[Options]\nshowname = Nick\nneeds_showname = false\n", "Phoenix", ""},
		// startsWith, not equality — canon's own test (:465).
		{"opted out verbosely", "[Options]\nneeds_showname = false (narrator)\n", "Phoenix", ""},
		{"opted IN explicitly", "[Options]\nshowname = Nick\nneeds_showname = true\n", "Phoenix", "Nick"},
		{"nested folder", "[Options]\nshowname = Nekomaru Nidai\n", "DR2/Nekomaru Nidai", "Nekomaru Nidai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ini, err := ParseCharINI([]byte(tc.ini))
			if err != nil {
				t.Fatal(err)
			}
			if got := ini.ShownameOrFolder(tc.folder); got != tc.want {
				t.Fatalf("ShownameOrFolder(%q) = %q, want %q", tc.folder, got, tc.want)
			}
		})
	}
	var missing *CharINI // an unreadable / unfetched char.ini
	if got := missing.ShownameOrFolder("Phoenix"); got != "Phoenix" {
		t.Fatalf("nil CharINI = %q, want the folder name (canon's unreadable-ini answer)", got)
	}
}

// TestTheNamePlateRoutesThroughTheSharedChain is the encapsulation gate. The
// plate and the IC log are two surfaces in two packages, and they agreed on the
// wrong answer for as long as they both kept their own copy of the rule. This
// fails the moment displayName grows a second opinion instead of calling
// DisplayName — which is how the middle rung would go missing again on one
// surface only, the hardest version of this bug to see.
func TestTheNamePlateRoutesThroughTheSharedChain(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	seen := 0
	room.IniShownameFor = func(char string) (string, bool) {
		seen++
		return "FROM THE CHAIN", true
	}
	room.RestoreMessage(&protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Message: "hi"})
	if seen == 0 {
		t.Fatal("the name plate never asked IniShownameFor — displayName is not going through DisplayName")
	}
	if room.Scene.ShownameText != "FROM THE CHAIN" {
		t.Fatalf("plate = %q: the plate asked the chain and then ignored it", room.Scene.ShownameText)
	}
	// And the shared function is the one that decides, for any caller.
	if got := DisplayName(&protocol.ChatMessage{CharName: "Phoenix"}, false, room.IniShownameFor); got != "FROM THE CHAIN" {
		t.Fatalf("DisplayName = %q", got)
	}
	if got := DisplayName(nil, false, room.IniShownameFor); got != "" {
		t.Fatalf("DisplayName(nil) = %q, want the empty string", got)
	}
}
