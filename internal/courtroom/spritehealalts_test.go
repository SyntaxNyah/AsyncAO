package courtroom

import (
	"reflect"
	"testing"
)

// TestSpriteHealAltsMatchesEmoteAlts pins SpriteHealAlts' contract: reconstructed
// from an Emote() base, it must equal EmoteAlts' own output byte-for-byte, for both
// EmoteKinds, a nested character identity (issue Crystalwarrior found 2026-08-09),
// and folder-only-emote-name cases. This is the fix for GH #70's self-heal gap —
// keepSceneAssetsWarm and healSpriteLayer only ever hold the built active URL, not
// (character, emote, kind), so they reconstruct through this function instead of
// calling EmoteAlts directly; if the reconstruction ever drifts from EmoteAlts'
// real output, a T1 eviction heal walks a DIFFERENT chain than the one that
// resolved the sprite in the first place.
func TestSpriteHealAltsMatchesEmoteAlts(t *testing.T) {
	u := NewURLBuilder("https://cdn/base/")
	cases := []struct {
		name  string
		char  string
		emote string
		kind  EmoteKind
	}{
		{"idle, flat char", "Phoenix", "normal", EmoteIdle},
		{"talk, flat char", "Phoenix", "happy", EmoteTalk},
		{"nested char identity", nestedIdentity, "suprised", EmoteTalk},
		{"name needing escape", "kanon neo", "lazy", EmoteIdle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			active := u.Emote(c.char, c.emote, c.kind)
			want := u.EmoteAlts(c.char, c.emote, c.kind)
			got := SpriteHealAlts(active)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("SpriteHealAlts(%q) = %q, want %q (EmoteAlts(%q,%q,%v))", active, got, want, c.char, c.emote, c.kind)
			}
		})
	}
}

// TestSpriteHealAltsNoOpsOnUnprefixedBase pins the preanim/already-bare case:
// a base with no leading "(a)"/"(b)" on its final segment (a preanim, or a sprite
// already resolved to its bare spelling) reconstructs to itself for BOTH links, so
// PrefetchChain just retries the same URL twice — harmless given the per-alt 404
// cache, and matches bareSpriteBase's pre-existing no-op behavior exactly.
func TestSpriteHealAltsNoOpsOnUnprefixedBase(t *testing.T) {
	cases := []string{
		"https://h/base/characters/phoenix/cross_preanim", // preanim, no prefix
		"https://h/base/characters/phoenix/normal",        // already bare
		"foo", // no slash and no prefix at all
	}
	for _, active := range cases {
		got := SpriteHealAlts(active)
		want := []string{active, active}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("SpriteHealAlts(%q) = %q, want %q", active, got, want)
		}
	}
}

// TestFolderSpriteBaseShape pins folderSpriteBase's URL shape directly (the
// reconstruction bareSpriteBase never had a sibling for before this fix), against
// representative bases including a "(b)" prefix and a nested character path.
func TestFolderSpriteBaseShape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://h/base/characters/phoenix/(a)normal", "https://h/base/characters/phoenix/(a)/normal"},
		{"https://h/base/characters/phoenix/(b)happy", "https://h/base/characters/phoenix/(b)/happy"},
		{"https://h/base/characters/phoenix/normal", "https://h/base/characters/phoenix/normal"},               // already bare — no-op
		{"https://h/base/characters/phoenix/cross_preanim", "https://h/base/characters/phoenix/cross_preanim"}, // preanim — no-op
		{"(a)foo", "(a)/foo"}, // no slash at all — the whole string IS the segment, prefix still splits out
	}
	for _, c := range cases {
		if got := folderSpriteBase(c.in); got != c.want {
			t.Errorf("folderSpriteBase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
