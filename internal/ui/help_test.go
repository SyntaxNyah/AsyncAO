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

// TestPrivacySections pins the privacy explainer: every section has a heading and
// at least one paragraph, the Privacy tab is registered, and it honestly names
// the three things newcomers worry about (IP, IPID, HDID).
func TestPrivacySections(t *testing.T) {
	if len(privacySections) < 4 {
		t.Fatalf("privacy explainer too short: %d sections", len(privacySections))
	}
	all := ""
	for _, s := range privacySections {
		if strings.TrimSpace(s.heading) == "" || len(s.body) == 0 {
			t.Errorf("empty privacy section: %+v", s)
		}
		for _, p := range s.body {
			if strings.TrimSpace(p) == "" {
				t.Errorf("empty paragraph in section %q", s.heading)
			}
		}
		all += s.heading + " " + strings.Join(s.body, " ")
	}
	for _, want := range []string{"HDID", "IPID", "IP address", "AO2", "SHA-256"} {
		if !strings.Contains(all, want) {
			t.Errorf("privacy explainer should mention %q", want)
		}
	}
	found := false
	for _, n := range helpSectionNames {
		if n == "Privacy" {
			found = true
		}
	}
	if !found {
		t.Error("helpSectionNames must include the Privacy tab")
	}
}
