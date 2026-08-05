package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestRefreshAreaFilter pins the #20 areas search: the filter matches
// case-insensitively on the area name, and — the load-bearing invariant — the
// stored indices are ORIGINAL indices into sess.Areas (which parallels
// sess.AreaInfo), so the draw loop looks up the right player count / status /
// lock for each shown row rather than the wrong parallel entry.
func TestRefreshAreaFilter(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{Areas: []string{
		"Courtroom 1", "Basement", "Courtyard", "Rooftop", "Court Records",
	}}

	got := a.refreshAreaFilter("court", areaQuerySlotOwn) // Courtroom 1 (0), Courtyard (2), Court Records (4)
	want := []int{0, 2, 4}
	if len(got) != len(want) {
		t.Fatalf("filtered %v, want indices %v", got, want)
	}
	for i, idx := range want {
		if got[i] != idx {
			t.Fatalf("filtered[%d] = %d (%q), want original index %d (%q)",
				i, got[i], a.sess.Areas[got[i]], idx, a.sess.Areas[idx])
		}
	}
}

// TestRefreshAreaFilterSlotsDoNotEvictEachOther pins the two-slot memo. The area
// list can be on screen twice in one frame under two DIFFERENT queries — a
// torn-off Areas panel filtered by its own box, the themed canvas copy filtered
// by AO2's music_search (drawAreaList's duplicate-box rule) — and with a single
// slot each drawer evicted the other every frame, so both O(N) scans ran forever
// and neither memo ever hit. Same clobber trick as the memo test below: a slot
// that re-scanned would refill its result.
func TestRefreshAreaFilterSlotsDoNotEvictEachOther(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{Areas: []string{"Alpha", "Beta", "Alabama"}}

	if got := a.refreshAreaFilter("al", areaQuerySlotOwn); len(got) != 2 {
		t.Fatalf("own slot: filtered %v, want 2 matches", got)
	}
	if got := a.refreshAreaFilter("be", areaQuerySlotThemed); len(got) != 1 {
		t.Fatalf("themed slot: filtered %v, want 1 match", got)
	}
	// Clobber BOTH results, then re-ask both with their own queries: two memo
	// hits, so neither is rebuilt.
	a.areaFiltered[areaQuerySlotOwn] = a.areaFiltered[areaQuerySlotOwn][:0]
	a.areaFiltered[areaQuerySlotThemed] = a.areaFiltered[areaQuerySlotThemed][:0]
	if got := a.refreshAreaFilter("al", areaQuerySlotOwn); len(got) != 0 {
		t.Errorf("own slot re-scanned after the themed slot ran: got %v", got)
	}
	if got := a.refreshAreaFilter("be", areaQuerySlotThemed); len(got) != 0 {
		t.Errorf("themed slot re-scanned after the own slot ran: got %v", got)
	}
}

// TestRefreshAreaFilterMemo pins the memoization: re-running with the same query
// and an unchanged Areas list must not re-scan (the per-frame guard). We prove it
// by mutating the RESULT slice out from under the memo — a re-scan would rebuild
// it, the memo leaves it as-is.
func TestRefreshAreaFilterMemo(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{Areas: []string{"Alpha", "Beta", "Alabama"}}

	if got := a.refreshAreaFilter("al", areaQuerySlotOwn); len(got) != 2 { // Alpha (0), Alabama (2)
		t.Fatalf("first pass: filtered %v, want 2 matches", got)
	}
	a.areaFiltered[areaQuerySlotOwn] = a.areaFiltered[areaQuerySlotOwn][:0] // clobber the cached result
	got := a.refreshAreaFilter("al", areaQuerySlotOwn)                      // same query + same list ⇒ memo hit, no rebuild
	if len(got) != 0 {
		t.Fatalf("memo failed: the identical query re-scanned (got %v)", got)
	}

	// A changed list identity (append) must invalidate the memo and re-scan.
	a.sess.Areas = append(a.sess.Areas, "Algeria")
	if got := a.refreshAreaFilter("al", areaQuerySlotOwn); len(got) != 3 { // + Algeria (3)
		t.Fatalf("list change did not re-scan: filtered %v, want 3 matches", got)
	}
}
