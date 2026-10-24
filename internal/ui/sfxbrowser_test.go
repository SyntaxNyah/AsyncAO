package ui

import "testing"

// TestSelectSFXOverride pins #12's find-or-append: an existing choice is selected (case-insensitive),
// a new sound is appended and selected — reusing the IC-bar dropdown's sfxChoiceIdx so the dropdown
// reflects the browser's pick.
func TestSelectSFXOverride(t *testing.T) {
	a := testTabApp(t)
	a.emotepick.sfxChoices = []string{sfxAutoLabel, "stab", "boom"}
	a.emotepick.sfxChoiceIdx = 0

	a.selectSFXOverride("boom")
	if a.emotepick.sfxChoiceIdx != 2 {
		t.Errorf("selecting existing 'boom' = idx %d, want 2", a.emotepick.sfxChoiceIdx)
	}
	a.selectSFXOverride("STAB") // case-insensitive match of "stab"
	if a.emotepick.sfxChoiceIdx != 1 {
		t.Errorf("case-insensitive select = idx %d, want 1", a.emotepick.sfxChoiceIdx)
	}
	a.selectSFXOverride("zap") // not present -> append + select
	if a.emotepick.sfxChoiceIdx != 3 || len(a.emotepick.sfxChoices) != 4 || a.emotepick.sfxChoices[3] != "zap" {
		t.Errorf("append select: idx=%d choices=%v", a.emotepick.sfxChoiceIdx, a.emotepick.sfxChoices)
	}
	a.selectSFXOverride("   ") // blank is a no-op
	if a.emotepick.sfxChoiceIdx != 3 {
		t.Errorf("blank override moved the index to %d", a.emotepick.sfxChoiceIdx)
	}
}

// TestSfxBrowserRows pins the list: starred favourites first, then this character's distinct sounds
// (a sound that's BOTH is shown once, as a favourite), and the query filters case-insensitively.
func TestSfxBrowserRows(t *testing.T) {
	a := testTabApp(t)
	a.iniChar = "tester"                                                        // activeCharName() -> "tester" (no session needed)
	a.emotepick.sfxChoices = []string{sfxAutoLabel, "stab", "boom"}             // idx 0 is auto; "stab"/"boom" are char sounds
	a.emotepick.sfxChoicesForName, a.emotepick.sfxChoicesForCount = "tester", 0 // matches ensureSFXChoices' guard (0 emotes) -> no rebuild
	a.d.Prefs.ToggleSfxFavorite("boom")                                         // also a char sound -> dedup to one (fav) row
	a.d.Prefs.ToggleSfxFavorite("zap")                                          // favourite only

	rows := a.sfxBrowserRows()
	if len(rows) != 3 {
		t.Fatalf("rows = %d (%v), want 3", len(rows), rows)
	}
	if !rows[0].fav || !rows[1].fav { // favourites first
		t.Errorf("favourites must lead the list: %v", rows)
	}
	if rows[2].name != "stab" || rows[2].fav {
		t.Errorf("char-only sound should follow, unstarred: %v", rows[2])
	}

	a.emotepick.sfxBrowserQuery = "bo" // filter
	rows = a.sfxBrowserRows()
	if len(rows) != 1 || rows[0].name != "boom" {
		t.Errorf("query 'bo' = %v, want [boom]", rows)
	}
}
