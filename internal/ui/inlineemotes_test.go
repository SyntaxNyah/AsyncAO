package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestExpandInlineEmotes pins the #18 slice-1 substitution: known shortcodes become emoji,
// unknown tokens and stray colons (URLs, times) stay exactly as typed, and several can sit
// in one line (adjacent included).
func TestExpandInlineEmotes(t *testing.T) {
	joy := inlineEmotes["joy"]
	fire := inlineEmotes["fire"]
	cases := []struct{ in, want string }{
		{"plain text, no colons", "plain text, no colons"},
		{"hi :joy: there", "hi " + joy + " there"},
		{":fire::fire:", fire + fire},                                // adjacent
		{"nope :unknown: nope", "nope :unknown: nope"},               // unknown stem left literal
		{"see http://example.com now", "see http://example.com now"}, // URL colon untouched
		{"meet at 12:30 sharp", "meet at 12:30 sharp"},               // time colon untouched
		{"a:b", "a:b"}, // no closing colon
		{"::", "::"},   // empty stem
		{":joy:", joy}, // whole string
	}
	for _, tc := range cases {
		if got := expandInlineEmotes(tc.in); got != tc.want {
			t.Errorf("expandInlineEmotes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestExpandInlineEmotesNoAlloc pins the perf gate: the common cases — no colon at all, or
// colons that never form a KNOWN shortcode — must not allocate (this runs per IC log line).
func TestExpandInlineEmotesNoAlloc(t *testing.T) {
	for _, s := range []string{"a perfectly normal message", "ratio 16:9 at http://host:8080/x"} {
		if allocs := testing.AllocsPerRun(100, func() {
			if expandInlineEmotes(s) != s {
				t.Fatalf("non-substituting input changed: %q", s)
			}
		}); allocs != 0 {
			t.Errorf("expandInlineEmotes(%q) allocated %.1f/op, want 0", s, allocs)
		}
	}
}

// TestInlineEmotesRouteToEmojiFont guards the registry against tofu: every emoji must be
// detected as needing the colour-emoji font (a BMP symbol needs its U+FE0F selector), same
// rule the picker is held to.
func TestInlineEmotesRouteToEmojiFont(t *testing.T) {
	for stem, e := range inlineEmotes {
		if !render.NeedsEmojiFallback(e) {
			t.Errorf("inline emote :%s: = %q (% x) won't reach the colour-emoji font — a BMP symbol needs a U+FE0F selector", stem, e, []byte(e))
		}
	}
}
