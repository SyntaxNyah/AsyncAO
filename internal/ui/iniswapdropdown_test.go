package ui

import "testing"

// TestIniswapCurrentIndexIsAbsolute pins the index mapping, which is the one
// destructive thing in this feature: it feeds RemoveWardrobe, which PERSISTS.
//
// The tempting implementation is to scan choices[1:] (skipping the own-folder row)
// and use the loop variable, because index 0 is special. That silently returns
// i-1, so clicking the X deletes the wardrobe entry ABOVE the one displayed —
// a saved favourite the user never selected. AO2 scans the whole list from 0 and
// uses the index directly (courtroom.cpp:5340-5347).
func TestIniswapCurrentIndexIsAbsolute(t *testing.T) {
	choices := []string{"Phoenix", "Edgeworth", "Godot", "Franziska"}
	for _, tc := range []struct {
		active string
		want   int
	}{
		{"Phoenix", 0},
		{"Edgeworth", 1},
		{"Godot", 2},
		{"Franziska", 3},
		{"godot", 2}, // folder lookups are case-folded
		{"", 0},      // wearing nothing = own folder
		{"Maya", 0},  // absent → own folder, never a stale index
	} {
		if got := iniswapCurrentIndex(choices, tc.active); got != tc.want {
			t.Errorf("iniswapCurrentIndex(%q) = %d, want %d — this index deletes a saved wardrobe entry",
				tc.active, got, tc.want)
		}
	}
	if got := iniswapCurrentIndex(nil, "Phoenix"); got != 0 {
		t.Errorf("an empty list must yield 0, got %d", got)
	}
}

// TestBuildIniswapChoicesKeepsTheWornFolder is the hole review found: a.iniChar
// can hold a folder that is NOT in the saved wardrobe, because wearFromMenu takes
// entries from the SERVER's iniswap.txt and never calls AddWardrobe.
//
// Without the trailing entry, iniswapCurrentIndex finds no match and falls back
// to 0 — so the dropdown would display the player's OWN character while they were
// visibly wearing something else, and iniswap_remove (gated on cur != 0) would
// hide itself exactly when it is needed. AO2 cannot hit this: its combobox is
// editable with InsertAtBottom and it rewrites iniswaps.ini on every change, so
// what you wear is always in the list.
func TestBuildIniswapChoicesKeepsTheWornFolder(t *testing.T) {
	// Worn folder absent from the wardrobe → appended, and findable.
	got := buildIniswapChoices(nil, "Phoenix", []string{"Edgeworth"}, "ServerOnlyChar")
	want := []string{"Phoenix", "Edgeworth", "ServerOnlyChar"}
	if len(got) != len(want) {
		t.Fatalf("choices = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("choices = %v, want %v", got, want)
		}
	}
	if idx := iniswapCurrentIndex(got, "ServerOnlyChar"); idx == 0 {
		t.Error("a worn-but-unsaved folder resolved to index 0 — the dropdown would show the " +
			"player's own character and the remove button would hide itself")
	}

	// Own folder is always index 0 (AO2 prepends it, courtroom.cpp:5331).
	if got[0] != "Phoenix" {
		t.Errorf("index 0 = %q, want the player's own folder", got[0])
	}

	// The own folder must not also appear as a wardrobe row, in either casing —
	// AO2 removeDuplicates'es, and a duplicate would make "remove" offerable on
	// the player's own character.
	dup := buildIniswapChoices(nil, "Phoenix", []string{"phoenix", "Edgeworth", "Edgeworth"}, "")
	if len(dup) != 2 || dup[0] != "Phoenix" || dup[1] != "Edgeworth" {
		t.Errorf("choices = %v, want the own folder once and Edgeworth once", dup)
	}

	// Wearing the own folder adds nothing.
	own := buildIniswapChoices(nil, "Phoenix", nil, "Phoenix")
	if len(own) != 1 {
		t.Errorf("choices = %v, want just the own folder", own)
	}

	// No character claimed yet → empty, which is AO2 hiding the pair outright
	// (courtroom.cpp:5323-5328 on m_cid == -1).
	if none := buildIniswapChoices(nil, "", []string{"Edgeworth"}, "Edgeworth"); len(none) != 0 {
		t.Errorf("choices = %v, want empty with no character claimed", none)
	}
}

// TestBuildIniswapChoicesReusesItsBuffer pins the allocation contract. The draw
// site calls the ensure wrapper every frame; the pure builder must refill the
// caller's slice rather than returning a fresh one.
func TestBuildIniswapChoicesReusesItsBuffer(t *testing.T) {
	buf := make([]string, 0, 8)
	buf = buildIniswapChoices(buf, "Phoenix", []string{"Edgeworth", "Godot"}, "")
	first := &buf[:1][0]
	buf = buildIniswapChoices(buf, "Phoenix", []string{"Edgeworth", "Godot"}, "")
	if &buf[:1][0] != first {
		t.Error("buildIniswapChoices reallocated instead of refilling — it runs behind a " +
			"per-frame draw site, so the buffer must be reused")
	}
	if n := testing.AllocsPerRun(100, func() {
		buf = buildIniswapChoices(buf, "Phoenix", []string{"Edgeworth", "Godot"}, "")
	}); n != 0 {
		t.Errorf("buildIniswapChoices allocates %v/op into a sized buffer, want 0", n)
	}
}
