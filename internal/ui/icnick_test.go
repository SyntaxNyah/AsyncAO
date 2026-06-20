package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestICLogLineDisplay pins the #82 IC-log nickname: a plain line when there's no
// nickname, "nick (showname): msg" with the SPEAKER field kept as the real name
// (so pairing + per-speaker colour still work), and the nickname suppressed in
// force-char (anti-impersonation) mode in favour of the character name.
func TestICLogLineDisplay(t *testing.T) {
	m := &protocol.ChatMessage{Showname: "PhoenixRP", CharName: "Phoenix", Message: "Objection!"}

	if line, spk := icLogLineDisplay(m, false, ""); line != "PhoenixRP: Objection!" || spk != "PhoenixRP" {
		t.Fatalf("no nickname = %q / %q, want \"PhoenixRP: Objection!\" / \"PhoenixRP\"", line, spk)
	}
	if line, spk := icLogLineDisplay(m, false, "Bird"); line != "Bird (PhoenixRP): Objection!" || spk != "PhoenixRP" {
		t.Fatalf("nickname = %q / %q, want \"Bird (PhoenixRP): Objection!\" / \"PhoenixRP\" (speaker stays real)", line, spk)
	}
	if line, spk := icLogLineDisplay(m, true, "Bird"); line != "Phoenix: Objection!" || spk != "Phoenix" {
		t.Fatalf("force-char = %q / %q, want the character name with no nickname", line, spk)
	}
}
