package theme

import "testing"

// TestPublishedEnumNamesMatchTheTables is the point of the accessors: the editor's
// dropdowns must offer the FORMAT's own wire names, indexed by the enum's own
// values, not a second list that drifts.
//
// It drives each enum's String() — the same function the reader and writer resolve
// through — so a name published here that String() does not agree with fails, and
// so does a table that grew a value the accessor forgot.
func TestPublishedEnumNamesMatchTheTables(t *testing.T) {
	check := func(what string, got []string, count int, name func(int) string) {
		t.Helper()
		if len(got) != count {
			t.Fatalf("%s publishes %d names for %d values — an editor dropdown built on this could not "+
				"select every value the format defines", what, len(got), count)
		}
		for i, n := range got {
			if n == "" {
				t.Errorf("%s[%d] is empty — the wire name IS the format", what, i)
			}
			if s := name(i); s != n {
				t.Errorf("%s[%d] publishes %q but String() resolves %q — two spellings of one wire "+
					"name is two names", what, i, n, s)
			}
		}
	}
	check("ElementKindNames", ElementKindNames(), int(ElementKindCount),
		func(i int) string { return ElementKind(i).String() })
	check("ElementBandNames", ElementBandNames(), int(ElementBandCount),
		func(i int) string { return ElementBand(i).String() })
	check("SpaceNames", SpaceNames(), int(SpaceCount),
		func(i int) string { return Space(i).String() })
	check("FitNames", FitNames(), int(FitCount),
		func(i int) string { return ElementFit(i).String() })
	check("EffectNames", EffectNames(), int(EffectKindCount),
		func(i int) string { return EffectKind(i).String() })
	check("AlignNames", AlignNames(), int(AlignCount),
		func(i int) string { return TextAlign(i).String() })
	check("ConditionAxisNames", ConditionAxisNames(), int(ConditionAxisCount),
		func(i int) string { return ConditionAxis(i).String() })
}

// TestPublishedEnumNamesAreCopies pins the reason every accessor allocates: they
// hand out package state that the parse path reads, and the append-only rule cannot
// survive a caller renaming a wire value at runtime.
func TestPublishedEnumNamesAreCopies(t *testing.T) {
	got := ElementKindNames()
	if len(got) == 0 {
		t.Fatal("no element kinds published")
	}
	want := got[0]
	got[0] = "hijacked"
	if again := ElementKindNames(); again[0] != want {
		t.Fatalf("mutating the returned slice changed the package's own table (%q) — an editor dropdown "+
			"would be able to rename a wire value for every reader in the process", again[0])
	}
}
