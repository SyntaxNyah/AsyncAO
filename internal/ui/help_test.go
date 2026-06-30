package ui

import (
	"strings"
	"testing"
)

// TestGlossaryContent pins the newcomer glossary: every entry has a term + a
// definition, terms are unique, and the core acronyms a beginner always asks
// about are covered.
func TestGlossaryContent(t *testing.T) {
	if len(glossaryEntries) < 10 {
		t.Fatalf("glossary too short: %d entries", len(glossaryEntries))
	}
	seen := map[string]bool{}
	for _, e := range glossaryEntries {
		if strings.TrimSpace(e.term) == "" || strings.TrimSpace(e.def) == "" {
			t.Errorf("empty glossary entry: %+v", e)
		}
		if seen[e.term] {
			t.Errorf("duplicate glossary term: %q", e.term)
		}
		seen[e.term] = true
	}
	for _, want := range []string{"IC", "OOC", "CM", "WTCE", "HDID", "IPID"} {
		found := false
		for _, e := range glossaryEntries {
			if strings.Contains(e.term, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("glossary missing a core term mentioning %q", want)
		}
	}
}
