package ui

import "testing"

// TestEmojiRasterNilResultCached pins the negative-cache contract: while the
// colour-emoji face hasn't loaded, emojiRaster degrades to nil — and that nil
// MUST be cached, or every visible emoji label (the IC bar's 🙂 button) re-runs
// []rune + coverRunes per frame and trips the whole-screen 0-alloc gate (the
// wave-2c catch). The entry self-invalidates when the face lands (the key
// embeds the emoji-face pointer; SetEmojiFont also purges the cache).
func TestEmojiRasterNilResultCached(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	if m := c.emojiRaster("🙂", ColText, c.font, nil); m != nil {
		t.Skip("emoji face unexpectedly available; the degraded path isn't reachable")
	}
	if n := testing.AllocsPerRun(100, func() { _ = c.emojiRaster("🙂", ColText, c.font, nil) }); n != 0 {
		t.Fatalf("repeat emojiRaster with no emoji face allocates %.1f/op, want 0 (nil result must be cached)", n)
	}
}
