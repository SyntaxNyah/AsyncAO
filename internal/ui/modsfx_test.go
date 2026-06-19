package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestClassifyModAction is the spec for the #60 OOC mod-action classifier: the
// keyword set, the un-/negation exclusions, the fixed first-match precedence
// (ban → kick → mute), and the server-origin gate.
func TestClassifyModAction(t *testing.T) {
	tests := []struct {
		name, text string
		want       render.ModAction
		ok         bool
	}{
		{"server", "Bob was kicked from the area.", render.ModKick, true},
		{"server", "Bob was banned.", render.ModBan, true},
		{"server", "Bob was muted.", render.ModMute, true},
		{"", "Bob was kicked.", render.ModKick, true},              // empty sender = server
		{"SERVER", "Bob was muted.", render.ModMute, true},         // case-insensitive
		{"server", "Bob was unbanned.", 0, false},                  // un- exclusion
		{"server", "Bob was unmuted.", 0, false},                   // un- exclusion
		{"server", "You are not muted.", 0, false},                 // negation exclusion
		{"server", "Banned Bob for kicking.", render.ModBan, true}, // precedence: ban wins
		{"Phoenix", "i think bob should be banned", 0, false},      // not server-origin
		{"Bandit", "hello", 0, false},                              // player name, no scan
		{"server", "Welcome to the courtroom!", 0, false},          // no verb
		{"server", "", 0, false},                                   // empty
	}
	for _, tc := range tests {
		got, ok := classifyModAction(tc.name, tc.text)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("classifyModAction(%q, %q) = (%v, %v), want (%v, %v)",
				tc.name, tc.text, got, ok, tc.want, tc.ok)
		}
	}
}
