package ui

import "testing"

// TestHealSpriteCallSitesUseSpriteHealAlts is the encapsulation test for GH #70's
// fix (subfoldered "(a)/X" emotes flashing missingno after a T1 eviction of a
// live sprite). keepSceneAssetsWarm's char-sprite warm arm and healSpriteLayer
// must both build their PrefetchChain alts from courtroom.SpriteHealAlts — the
// SAME bare-then-folder reconstruction of EmoteAlts' chain, so a heal walks
// exactly the chain that resolved the sprite in the first place.
//
// WHY A SOURCE GATE. This package used to carry its own private bareSpriteBase
// helper (2-link: glued→bare only, missing the folder spelling) — it doesn't
// anymore; courtroom.SpriteHealAlts is the only source of sprite heal alts left
// in this file. That alone stops a copy-paste regression, but nothing stops a
// FUTURE call site from hand-rolling a fresh 1- or 2-element []string literal
// instead of calling the shared helper (exactly how 0039f83 introduced this bug:
// it copied a stale helper into a second call site two weeks after the 3-link
// chain became standard everywhere else, and nothing caught it). This gate
// parses the two functions' own source and fails loudly the moment either one
// stops calling courtroom.SpriteHealAlts, so the omission can't quietly return.
func TestHealSpriteCallSitesUseSpriteHealAlts(t *testing.T) {
	for _, fn := range []string{"keepSceneAssetsWarm", "healSpriteLayer"} {
		body := funcBodySource(t, "app.go", fn)
		if !containsCall(body, "SpriteHealAlts") {
			t.Errorf("%s no longer builds its char-sprite heal chain from courtroom.SpriteHealAlts — it can silently regress to a chain shorter than the one that resolved the sprite", fn)
		}
	}
}
