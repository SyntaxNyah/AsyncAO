package ui

import "testing"

// TestBareSpriteBase pins the prefix→bare reconstruction healSpriteLayer relies
// on: stripping a leading "(a)"/"(b)" from the final path segment must turn a
// URLBuilder.Emote base back into the EmoteBare spelling, and leave preanim /
// already-bare bases untouched (so the heal's fallback retries the same URL).
func TestBareSpriteBase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://h/base/characters/phoenix/(a)normal", "https://h/base/characters/phoenix/normal"},
		{"https://h/base/characters/phoenix/(b)happy", "https://h/base/characters/phoenix/happy"},
		{"https://h/base/characters/phoenix/normal", "https://h/base/characters/phoenix/normal"},               // already bare
		{"https://h/base/characters/phoenix/cross_preanim", "https://h/base/characters/phoenix/cross_preanim"}, // preanim, no prefix
		{"(a)foo", "foo"}, // no slash at all
	}
	for _, c := range cases {
		if got := bareSpriteBase(c.in); got != c.want {
			t.Errorf("bareSpriteBase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
