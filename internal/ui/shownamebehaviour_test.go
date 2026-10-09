package ui

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestLiveLogHonoursTheShowname drives an IC message through the REAL handler
// (handleSessionEvents) and asserts the rendered log line opens with the
// char.ini showname — the behavioural proof that the live log honours the same
// name chain as the plate.
func TestLiveLogHonoursTheShowname(t *testing.T) {
	a := testTabApp(t)
	a.urls = courtroom.NewURLBuilder("http://assets.example.test/base/")
	a.charMetaCache = map[string]charMeta{a.charINIURL("Phoenix"): {showname: "Nick", done: true}}

	a.handleSessionEvents([]courtroom.Event{{
		Kind:    courtroom.EventMessage,
		Message: &protocol.ChatMessage{CharName: "Phoenix", Message: "Objection!"},
	}})

	if n := len(a.icLog); n == 0 {
		t.Fatal("no IC entry after the event")
	}
	got := a.icLog[len(a.icLog)-1]
	if got.speaker != "Nick" {
		t.Fatalf("log speaker = %q, want char.ini showname %q", got.speaker, "Nick")
	}
	if !strings.HasPrefix(got.text, "Nick: ") {
		t.Fatalf("log line = %q, want it to open with the showname %q", got.text, "Nick: ")
	}
}
