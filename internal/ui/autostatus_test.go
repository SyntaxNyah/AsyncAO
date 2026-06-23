package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestMatchAutoStatus pins the #M1 typed-word → status matcher: whole-word,
// case-insensitive, punctuation-tolerant, last match wins, off when disabled.
func TestMatchAutoStatus(t *testing.T) {
	pref := config.AutoStatusPref{
		Enabled:    true,
		ClearWords: "back",
		AFKWords:   "brb, afk, away",
		BusyWords:  "busy",
	}
	cases := []struct {
		text string
		want courtroom.Status
		ok   bool
	}{
		{"objection!", courtroom.StatusNone, false},    // no trigger
		{"brb dinner", courtroom.StatusAFK, true},      // word match
		{"BRB!", courtroom.StatusAFK, true},            // case + trailing punctuation
		{"ok im back now", courtroom.StatusNone, true}, // clear word
		{"brb... ok back", courtroom.StatusNone, true}, // last match wins (back clears brb)
		{"i am busy", courtroom.StatusBusy, true},
		{"remembering things", courtroom.StatusNone, false}, // substring is not a whole word
	}
	for _, tc := range cases {
		got, ok := matchAutoStatus(tc.text, pref)
		if got != tc.want || ok != tc.ok {
			t.Errorf("matchAutoStatus(%q) = (%v,%v), want (%v,%v)", tc.text, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := matchAutoStatus("brb", config.AutoStatusPref{Enabled: false, AFKWords: "brb"}); ok {
		t.Error("disabled auto-status should never match")
	}
}
