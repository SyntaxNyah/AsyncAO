package ui

import (
	"testing"
)

// TestIniswapCurrentIndexIsAbsolute pins the index mapping: AO2 scans the whole list from 0 and uses the index
// directly (courtroom.cpp:5340-5347), and an unmatched or empty worn folder is the own folder, row 0.
func TestIniswapCurrentIndexIsAbsolute(t *testing.T) {
	choices := []string{"Phoenix", "Edgeworth"}
	for _, tc := range []struct {
		active string
		want   int
	}{
		{"Phoenix", 0},
		{"Edgeworth", 1},
		{"edgeworth", 1}, // folder lookups are case-folded
		{"", 0},          // wearing nothing = own folder
		{"Maya", 0},      // absent → own folder, never a stale index
	} {
		if got := iniswapCurrentIndex(choices, tc.active); got != tc.want {
			t.Errorf("iniswapCurrentIndex(%q) = %d, want %d", tc.active, got, tc.want)
		}
	}
	if got := iniswapCurrentIndex(nil, "Phoenix"); got != 0 {
		t.Errorf("an empty list must yield 0, got %d", got)
	}
}

// TestBuildIniswapChoicesListsOwnThenWorn pins the list: the own folder first (AO2 prepends it,
// courtroom.cpp:5331), then the worn folder, so the dropdown shows what is worn and iniswap_remove is offered.
func TestBuildIniswapChoicesListsOwnThenWorn(t *testing.T) {
	got := buildIniswapChoices(nil, "Phoenix", "Godot")
	if len(got) != 2 || got[0] != "Phoenix" || got[1] != "Godot" {
		t.Fatalf("choices = %v, want [Phoenix Godot]", got)
	}
	if idx := iniswapCurrentIndex(got, "Godot"); idx != 1 {
		t.Errorf("the worn folder resolved to %d, want 1: the remove button would hide", idx)
	}
	// Wearing the own folder, in either casing, adds nothing: a duplicate would offer remove on the own character.
	if own := buildIniswapChoices(nil, "Phoenix", "phoenix"); len(own) != 1 {
		t.Errorf("choices = %v, want just the own folder", own)
	}
	// No character claimed yet → empty, which is AO2 hiding the pair (courtroom.cpp:5323-5328 on m_cid == -1).
	if none := buildIniswapChoices(nil, "", "Godot"); len(none) != 0 {
		t.Errorf("choices = %v, want empty with no character claimed", none)
	}
}

// TestBuildIniswapChoicesReusesItsBuffer pins the allocation contract: the builder refills the caller's slice.
func TestBuildIniswapChoicesReusesItsBuffer(t *testing.T) {
	const bufCap = 2
	buf := make([]string, 0, bufCap)
	if n := testing.AllocsPerRun(100, func() {
		buf = buildIniswapChoices(buf, "Phoenix", "Godot")
	}); n != 0 {
		t.Errorf("buildIniswapChoices allocates %v/op into a sized buffer, want 0", n)
	}
}

// TestPosRemoveHiddenWhileTheCharINIIsInFlight is the wire-safety pin for
// pos_remove, and it is about a WINDOW, not a steady state.
//
// The button reverts to the character's declared side, and clicking it SENDS
// /pos. Before the fix, charDefaultSide was written only on the char.ini success
// path, so three situations left the PREVIOUS character's default in place — the
// char.ini failure branch, a character switch (enterCourtroom clears sidePref but
// resetSessionState does not run there), and setIniswap. In that window the
// button would paint for a side the user never overrode and one click would put a
// wrong /pos on the wire.
//
// Clearing the field makes defaultSide() read "wit", which is exactly what AO2's
// get_char_side returns for an unreadable ini (text_file_functions.cpp:480-483),
// so the loading window matches mySide()'s own fallback and the button stays down.
func TestPosRemoveHiddenWhileTheCharINIIsInFlight(t *testing.T) {
	a := testTabApp(t)

	// Nothing loaded yet: default is "wit", side is "wit" → hidden.
	if a.defaultSide() != "wit" {
		t.Errorf("defaultSide with no char.ini = %q, want \"wit\" (get_char_side's fallback)", a.defaultSide())
	}
	if posRemoveVisible(a.mySide(), a.defaultSide()) {
		t.Error("pos_remove must stay hidden before any char.ini lands — clicking it sends /pos")
	}

	// A character that declares "def": standing on it is not an override.
	a.charDefaultSide = "def"
	a.sidePref = "def"
	if posRemoveVisible(a.mySide(), a.defaultSide()) {
		t.Error("standing on the character's OWN default is not an override")
	}

	// A real /pos override: now it shows, and reverting targets the declared side.
	a.sidePref = "pro"
	if !posRemoveVisible(a.mySide(), a.defaultSide()) {
		t.Error("a side that differs from the character's default must offer the revert")
	}
	if a.defaultSide() != "def" {
		t.Errorf("revert target = %q, want the declared \"def\"", a.defaultSide())
	}

	// THE BUG: switching characters must not leave the old default behind. With a
	// stale "def" and a fresh character sitting at "wit", the button would paint
	// and one click would send "/pos def" for a character that never asked for it.
	a.charDefaultSide = ""
	a.sidePref = ""
	if posRemoveVisible(a.mySide(), a.defaultSide()) {
		t.Error("a cleared default must hide the button, not offer the previous character's side")
	}
}

// TestCharacterChangeClearsTheDeclaredSide is the other half of the wire-safety
// pin above, and it is the one that can only be checked by driving the real code
// path. The test above proves the PREDICATE hides the button for an empty default;
// this proves the default actually gets emptied when the character changes.
//
// setIniswap is the path review flagged: it resets the emote list and reloads
// char.ini but used to touch neither side field, so the previous character's
// declared side survived into the window before the new ini landed. Verified in
// the other direction too — deleting the clear makes this fail.
func TestCharacterChangeClearsTheDeclaredSide(t *testing.T) {
	a := testTabApp(t)
	a.charDefaultSide = "def"
	a.setIniswap("SomeOtherFolder")
	if a.charDefaultSide != "" {
		t.Errorf("setIniswap left charDefaultSide=%q — the previous character's default "+
			"survives, so pos_remove would offer to /pos a side this character never declared",
			a.charDefaultSide)
	}
	if posRemoveVisible(a.mySide(), a.defaultSide()) {
		t.Error("pos_remove must be hidden in the window between an iniswap and its char.ini")
	}
}
