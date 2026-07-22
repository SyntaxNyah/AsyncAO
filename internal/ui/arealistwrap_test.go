package ui

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestAreaWrappedWordWraps pins Issue #22: a long area name/detail no longer
// clips off the panel edge — it wraps into multiple lines instead. Uses a nil
// font (the wrapToWidth headless-test convention: ~8px/char) so no SDL is
// needed.
func TestAreaWrappedWordWraps(t *testing.T) {
	a := testTabApp(t)
	long := "Courtroom Number One With A Very Long Extended Name For Wrapping"
	a.sess = &courtroom.Session{
		Areas:    []string{long, "Lobby"},
		AreaInfo: []courtroom.AreaInfo{{Players: -1}, {Players: -1}},
	}

	const cardW = int32(100) // nameW = 88 → at 8px/char, ~11 chars/line
	rows := a.areaWrapped(nil, cardW)
	if len(rows) != 2 {
		t.Fatalf("areaWrapped rows = %d, want 2", len(rows))
	}
	if len(rows[0].nameLines) <= 1 {
		t.Fatalf("long area name did not wrap: nameLines = %v", rows[0].nameLines)
	}
	if rows[0].lineCount() != int32(len(rows[0].nameLines))+1 { // detail is empty → reserves 1 line
		t.Fatalf("lineCount() = %d, want %d name lines + 1 reserved detail line",
			rows[0].lineCount(), len(rows[0].nameLines)+1)
	}
	// A short name stays a single line — no regression for the common case.
	if len(rows[1].nameLines) != 1 {
		t.Fatalf("short area name unexpectedly wrapped: %v", rows[1].nameLines)
	}
}

// TestAreaWrappedCache pins the memo: an unchanged ARUP generation, query,
// width, zoom, and font chain must return the cached slice untouched, and a
// bump of areaInfoSeq (what EventAreasUpdated does) must force a rebuild.
func TestAreaWrappedCache(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{
		Areas:    []string{"Alpha", "Beta"},
		AreaInfo: []courtroom.AreaInfo{{Players: -1}, {Players: -1}},
	}

	first := a.areaWrapped(nil, 200)
	if len(first) != 2 {
		t.Fatalf("first pass: %d rows, want 2", len(first))
	}
	a.areaWrap = a.areaWrap[:0] // clobber the cached result
	second := a.areaWrapped(nil, 200)
	if len(second) != 0 {
		t.Fatalf("memo failed: unchanged inputs re-scanned (got %d rows)", len(second))
	}

	a.areaInfoSeq++ // simulate an ARUP update
	third := a.areaWrapped(nil, 200)
	if len(third) != 2 {
		t.Fatalf("areaInfoSeq bump did not invalidate the memo: got %d rows", len(third))
	}
}

// TestAreaRowLineHNGeneralizesTwoLine pins that areaRowLineHN(fontH, 2) still
// matches the historical areaRowLineH formula, and grows for taller rows.
func TestAreaRowLineHNGeneralizesTwoLine(t *testing.T) {
	if got, want := areaRowLineHN(17, 2), areaRowLineH(17); got != want {
		t.Fatalf("areaRowLineHN(17,2) = %d, want %d (must match areaRowLineH)", got, want)
	}
	if areaRowLineHN(17, 3) <= areaRowLineHN(17, 2) {
		t.Fatalf("a 3-line row must be taller than a 2-line row")
	}
}

// TestMusicCategoryLabel pins the "==Category==" wrapper strip (Issue #17):
// only strips when BOTH markers are present, leaving anything else untouched.
func TestMusicCategoryLabel(t *testing.T) {
	cases := map[string]string{
		"==Cave Story OST==": "Cave Story OST",
		"  ==Spaced==  ":     "Spaced",
		"No markers":         "No markers",
		"==Only prefix":      "==Only prefix",
		"Only suffix==":      "Only suffix==",
	}
	for in, want := range cases {
		if got := musicCategoryLabel(in); got != want {
			t.Errorf("musicCategoryLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRefreshMusicGroups pins Issue #17's grouped row layout: headers stay
// visible, a collapsed header hides its tracks, and an EXPANDED header (or no
// header at all yet) shows every track.
func TestRefreshMusicGroups(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{Music: []string{
		"==Cave Story==", "a.mp3", "b.mp3",
		"==Umineko==", "c.mp3",
	}}

	a.refreshMusicGroups("")
	if len(a.musicRows) != 5 {
		t.Fatalf("expanded: got %d rows, want 5 (2 headers + 3 tracks)", len(a.musicRows))
	}
	if !a.musicRows[0].header || a.musicRows[0].ti != 0 {
		t.Fatalf("row 0 = %+v, want header at index 0", a.musicRows[0])
	}

	// Collapse the first category: its two tracks (indices 1,2) vanish, the
	// second category and its track stay.
	a.musicCollapsed = map[int]bool{0: true}
	a.musicCollapseGen++
	a.refreshMusicGroups("")
	want := []musicRow{{ti: 0, header: true}, {ti: 3, header: true}, {ti: 4}}
	if len(a.musicRows) != len(want) {
		t.Fatalf("collapsed: got %v, want %v", a.musicRows, want)
	}
	for i, r := range want {
		if a.musicRows[i] != r {
			t.Fatalf("collapsed row %d = %+v, want %+v", i, a.musicRows[i], r)
		}
	}
}

// TestRefreshMusicGroupsSearchBypassesCollapse pins the VSCode-style search
// behaviour: even with a category collapsed, a search that matches one of its
// tracks must reveal that track AND pull in its header for context — manual
// collapse state is bypassed while searching, not mutated.
func TestRefreshMusicGroupsSearchBypassesCollapse(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{Music: []string{
		"==Cave Story==", "balcony.mp3", "gravity.mp3",
		"==Umineko==", "dawn.mp3",
	}}
	a.musicCollapsed = map[int]bool{0: true} // Cave Story collapsed

	a.refreshMusicGroups(strings.ToLower("gravity"))
	want := []musicRow{{ti: 0, header: true}, {ti: 2}} // header pulled in for context + the one match
	if len(a.musicRows) != len(want) {
		t.Fatalf("search rows = %v, want %v", a.musicRows, want)
	}
	for i, r := range want {
		if a.musicRows[i] != r {
			t.Fatalf("search row %d = %+v, want %+v", i, a.musicRows[i], r)
		}
	}
	// Collapse state itself must be untouched — clearing the query returns to
	// the manually-collapsed layout, not a stale expanded one.
	if !a.musicCollapsed[0] {
		t.Fatalf("search must not mutate collapse state")
	}
}

// TestSetAllMusicCollapsed pins the Expand All / Collapse All bulk toggle
// (Issue #17's explicit ask for an actual button, not per-row only): Collapse
// All folds every header in one pass, Expand All clears the map entirely
// (the default all-expanded state), and both bump musicCollapseGen so the
// memoized row layout rebuilds.
func TestSetAllMusicCollapsed(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{Music: []string{
		"==A==", "a1.mp3", "==B==", "b1.mp3", "b2.mp3",
	}}

	genBefore := a.musicCollapseGen
	a.setAllMusicCollapsed(true)
	if a.musicCollapseGen == genBefore {
		t.Fatalf("Collapse All did not bump musicCollapseGen")
	}
	if !a.musicCollapsed[0] || !a.musicCollapsed[2] {
		t.Fatalf("Collapse All must fold every header: %v", a.musicCollapsed)
	}

	genBefore = a.musicCollapseGen
	a.setAllMusicCollapsed(false)
	if a.musicCollapseGen == genBefore {
		t.Fatalf("Expand All did not bump musicCollapseGen")
	}
	if len(a.musicCollapsed) != 0 {
		t.Fatalf("Expand All must clear all collapse state: %v", a.musicCollapsed)
	}
}
