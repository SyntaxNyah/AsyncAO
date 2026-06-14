package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestDetailedLogLine pins the transcript line format (detailed logging): a
// fixed timestamp, server, area, "Char (Showname)" (parens dropped when the
// showname is blank or matches the character), then the message.
func TestDetailedLogLine(t *testing.T) {
	now := time.Date(2026, 6, 14, 15, 4, 5, 0, time.UTC)
	cases := []struct {
		name         string
		server, area string
		msg          *protocol.ChatMessage
		want         string
	}{
		{
			"char + showname",
			"Skrapegropen", "Courtroom 1",
			&protocol.ChatMessage{CharName: "Phoenix", Showname: "Nick", Message: "Objection!"},
			"2026-06-14 15:04:05 | Skrapegropen | Courtroom 1 | Phoenix (Nick) | Objection!",
		},
		{
			"showname matches char (case-insensitive) → no parens",
			"S", "A",
			&protocol.ChatMessage{CharName: "Phoenix", Showname: "phoenix", Message: "hi"},
			"2026-06-14 15:04:05 | S | A | Phoenix | hi",
		},
		{
			"blank area → dash, blank showname",
			"S", "",
			&protocol.ChatMessage{CharName: "Maya", Message: "..."},
			"2026-06-14 15:04:05 | S | - | Maya | ...",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detailedLogLine(now, tc.server, tc.area, tc.msg); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}
