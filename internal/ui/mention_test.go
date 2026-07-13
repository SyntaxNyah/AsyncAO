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

// TestCallwordHit pins the §3.5 callword matcher: a bare word matches as a WHOLE
// word (so "tif" no longer self-pings on "motif"), while a trailing '*' opts into
// a looser prefix match that only requires a WORD START (so "obj*" still catches
// "objection" but "tif*" still won't hit "motif"). A lone "*" must never match.
// Inputs arrive already lowercased from checkCallwords.
func TestCallwordHit(t *testing.T) {
	cases := []struct {
		text, word string
		want       bool
	}{
		// Bare word = whole-word (this is the reported bug's fix).
		{"hi tif", "tif", true},              // whole word hits
		{"tif, over here", "tif", true},      // punctuation edge still a boundary
		{"look at this motif", "tif", false}, // interior substring no longer fires
		{"objection!", "obj", false},         // bare "obj" is now whole-word only
		{"phoenix wins", "phoenix", true},    // plain word still fires (tabs_test relies on this)
		// Trailing '*' = word-start prefix, no word-end needed.
		{"objection!", "obj*", true},          // word-start prefix hits
		{"i am objecting", "obj*", true},      // still catches the family
		{"obj", "obj*", true},                 // the exact word also starts with the stem
		{"look at this motif", "tif*", false}, // "tif" isn't a word START in "motif"
		{"motif tiffany", "tif*", true},       // rejects "motif", accepts word-start "tiffany"
		{"a kobj thing", "obj*", false},       // interior — "obj" isn't a word start
		// A lone "*" (empty stem) must never fire on everything.
		{"any text at all", "*", false},
		// Empty inputs.
		{"", "obj", false},
		{"objection", "", false},
	}
	for _, tc := range cases {
		if got := callwordHit(tc.text, tc.word); got != tc.want {
			t.Errorf("callwordHit(%q, %q) = %v, want %v", tc.text, tc.word, got, tc.want)
		}
	}
}

// TestContainsWordPrefix pins the word-START matcher backing the '*' escape hatch:
// the needle must begin a word (bounded left by a non-word rune or the string
// start) but is free on its right edge, and the scan keeps going past an interior
// reject so a later real word-start still lands. Both args arrive lowercased.
func TestContainsWordPrefix(t *testing.T) {
	cases := []struct {
		hay, needle string
		want        bool
	}{
		{"objection", "obj", true},   // word start, right edge free
		{"an obj here", "obj", true}, // bounded left by a space
		{"kobj interior", "obj", false},
		{"motif tiffany", "tif", true}, // interior "motif" rejected, "tiffany" accepted
		{"only motif here", "tif", false},
		{"héllo maría!", "marí", true}, // Unicode word-start prefix
		{"", "obj", false},
		{"obj", "", false},
	}
	for _, tc := range cases {
		if got := containsWordPrefix(tc.hay, tc.needle); got != tc.want {
			t.Errorf("containsWordPrefix(%q, %q) = %v, want %v", tc.hay, tc.needle, got, tc.want)
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
