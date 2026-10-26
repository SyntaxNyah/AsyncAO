package ui

import "testing"

// TestSearchMatches pins the case-insensitive find: it returns rune ranges for
// every occurrence, and a blank query yields nothing.
func TestSearchMatches(t *testing.T) {
	got := searchMatches("Phoenix: OBJECTION! objection", "objection")
	if len(got) != 2 {
		t.Fatalf("matches = %d, want 2", len(got))
	}
	// "Phoenix: " is 9 runes; "OBJECTION" is the next 9.
	if got[0].start != 9 || got[0].end != 18 {
		t.Errorf("first match = %+v, want [9,18)", got[0])
	}
	if got[1].start <= got[0].end {
		t.Errorf("second match %+v must start after the first", got[1])
	}
}

func TestSearchMatchesBlankQuery(t *testing.T) {
	if got := searchMatches("anything", "   "); got != nil {
		t.Fatalf("blank query = %v, want nil", got)
	}
}

// TestFocusLogSearch pins that the find hotkey arms the log search field.
func TestFocusLogSearch(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.focusLogSearch()
	if a.ctx.focusID != "logsearch" {
		t.Errorf("focusID = %q, want %q", a.ctx.focusID, "logsearch")
	}
}
