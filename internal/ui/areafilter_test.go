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

	a.refreshAreaFilter("court") // matches Courtroom 1 (0), Courtyard (2), Court Records (4)
	want := []int{0, 2, 4}
	if len(a.areaFiltered) != len(want) {
		t.Fatalf("filtered %v, want indices %v", a.areaFiltered, want)
	}
	for i, idx := range want {
		if a.areaFiltered[i] != idx {
			t.Fatalf("filtered[%d] = %d (%q), want original index %d (%q)",
				i, a.areaFiltered[i], a.sess.Areas[a.areaFiltered[i]], idx, a.sess.Areas[idx])
		}
	}
}

// TestRefreshAreaFilterMemo pins the memoization: re-running with the same query
// and an unchanged Areas list must not re-scan (the per-frame guard). We prove it
// by mutating the RESULT slice out from under the memo — a re-scan would rebuild
// it, the memo leaves it as-is.
func TestRefreshAreaFilterMemo(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{Areas: []string{"Alpha", "Beta", "Alabama"}}

	a.refreshAreaFilter("al") // Alpha (0), Alabama (2)
	if len(a.areaFiltered) != 2 {
		t.Fatalf("first pass: filtered %v, want 2 matches", a.areaFiltered)
	}
	a.areaFiltered = a.areaFiltered[:0] // clobber the cached result
	a.refreshAreaFilter("al")           // same query + same list ⇒ memo hit, no rebuild
	if len(a.areaFiltered) != 0 {
		t.Fatalf("memo failed: the identical query re-scanned (got %v)", a.areaFiltered)
	}

	// A changed list identity (append) must invalidate the memo and re-scan.
	a.sess.Areas = append(a.sess.Areas, "Algeria")
	a.refreshAreaFilter("al") // Alpha (0), Alabama (2), Algeria (3)
	if len(a.areaFiltered) != 3 {
		t.Fatalf("list change did not re-scan: filtered %v, want 3 matches", a.areaFiltered)
	}
}
