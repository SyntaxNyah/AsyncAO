package ui

import "testing"

// TestContainsWord pins the #203 whole-word matcher used by self-mention alerts:
// a name matches when bounded by a non-letter/digit edge (spaces, punctuation,
// string ends) but never as a substring of a larger word. Both args arrive
// lowercased from the caller, so these inputs are lowercase.
func TestContainsWord(t *testing.T) {
	cases := []struct {
		hay, needle string
		want        bool
	}{
		{"hey max, over here", "max", true},
		{"max!", "max", true},
		{"max", "max", true},
		{"@max pinged you", "max", true},
		{"i want maximum output", "max", false}, // interior substring
		{"climax reached", "max", false},        // trailing substring
		{"the maxwell case", "max", false},      // leading substring
		{"objection, phoenix!", "phoenix", true},
		{"phoenixwright is one word", "phoenix", false},
		{"", "max", false},
		{"max", "", false},
		{"héllo maría!", "maría", true}, // Unicode name, whole word
		{"maríabelle waved", "maría", false},
	}
	for _, tc := range cases {
		if got := containsWord(tc.hay, tc.needle); got != tc.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", tc.hay, tc.needle, got, tc.want)
		}
	}
}

// TestIsSelfName pins the self-ping guard: a case-insensitive match against any
// identity name (blank name / blank list never match), so your own echoed
// message can't alert you by name.
func TestIsSelfName(t *testing.T) {
	names := []string{"Phoenix", "phoenix wright", ""}
	if !isSelfName("PHOENIX", names) {
		t.Error("case-insensitive self match failed")
	}
	if !isSelfName("  Phoenix Wright ", names) {
		t.Error("trimmed multi-word self match failed")
	}
	if isSelfName("Maya", names) {
		t.Error("a different speaker must not read as self")
	}
	if isSelfName("", names) || isSelfName("Phoenix", nil) {
		t.Error("blank name or empty list must never match")
	}
}
