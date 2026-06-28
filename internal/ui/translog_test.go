package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestDetailedLogLine pins the AO-style transcript line: a bracketed timestamp, then
// "showname (char)" — showname first, falling back to just the character when there's no distinct
// showname — then the message. No server (it's the folder now) or area/pipe columns.
func TestDetailedLogLine(t *testing.T) {
	now := time.Date(2026, 6, 14, 15, 4, 5, 0, time.UTC)
	cases := []struct {
		name string
		msg  *protocol.ChatMessage
		want string
	}{
		{
			"showname + char → showname first",
			&protocol.ChatMessage{CharName: "Phoenix", Showname: "Nick", Message: "Objection!"},
			"[2026-06-14 15:04:05] Nick (Phoenix): Objection!",
		},
		{
			"showname matches char (case-insensitive) → just the name",
			&protocol.ChatMessage{CharName: "Phoenix", Showname: "phoenix", Message: "hi"},
			"[2026-06-14 15:04:05] phoenix: hi",
		},
		{
			"blank showname → character",
			&protocol.ChatMessage{CharName: "Maya", Message: "..."},
			"[2026-06-14 15:04:05] Maya: ...",
		},
		{
			"blank char → showname only (no empty parens)",
			&protocol.ChatMessage{Showname: "Mystery", Message: "?"},
			"[2026-06-14 15:04:05] Mystery: ?",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detailedLogLine(now, tc.msg); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestSanitizeLogFolder pins the per-server folder safety: separators/reserved chars become "_",
// internal dots survive, surrounding dots/spaces trim, and an empty name falls back to "server".
func TestSanitizeLogFolder(t *testing.T) {
	cases := map[string]string{
		"Skrapegropen": "Skrapegropen",
		"miku.pizza":   "miku.pizza",
		`a/b\c:d*e?`:   "a_b_c_d_e_",
		"  spaced  ":   "spaced",
		"":             "server",
		"  . . ":       "server",
	}
	for in, want := range cases {
		if got := sanitizeLogFolder(in); got != want {
			t.Errorf("sanitizeLogFolder(%q) = %q, want %q", in, got, want)
		}
	}
}
