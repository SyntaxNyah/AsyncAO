package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestRasterizeFallbackBuildsSpans is the headless proof for the emoji fallback
// raster: a mixed text+emoji message builds without error and preserves the rune
// count the typewriter reveal walks. It uses two embedded-font INSTANCES as the
// two faces (distinct pointers, so the segmenter actually splits the emoji rune
// onto the second face), exercising the multi-font layout + baseline path without
// needing the system emoji font. The nil-face call proves the graceful degrade
// (the off-thread load may not have landed for the first emoji message).
func TestRasterizeFallbackBuildsSpans(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()

	textFont, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Skipf("embedded font unavailable: %v", err)
	}
	defer textFont.Close()
	emojiFace, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Skipf("embedded font unavailable: %v", err)
	}
	defer emojiFace.Close()

	const msg = "hi 😀!" // 5 runes; only 😀 routes to the emoji face → 3 runs
	want := len([]rune(msg))
	spans := []render.ColorSpan{{Len: want, Color: sdl.Color{R: 240, G: 240, B: 240, A: 255}}}
	// Per-rune text faces (all the same here): the emoji mask overrides 😀 onto emojiFace.
	textFonts := make([]*ttf.Font, want)
	for i := range textFonts {
		textFonts[i] = textFont
	}

	raster, err := render.RasterizeFallback(ren, textFonts, emojiFace, msg, spans, 400)
	if err != nil {
		t.Fatalf("RasterizeFallback: %v", err)
	}
	defer raster.Destroy()
	if got := raster.TotalRunes(); got != want {
		t.Errorf("TotalRunes = %d, want %d (the reveal walk needs every rune)", got, want)
	}

	// A nil emoji face (not yet loaded) must not crash — it degrades to the text font.
	r2, err := render.RasterizeFallback(ren, textFonts, nil, msg, spans, 400)
	if err != nil {
		t.Fatalf("RasterizeFallback(nil emoji): %v", err)
	}
	if got := r2.TotalRunes(); got != want {
		t.Errorf("nil-face TotalRunes = %d, want %d", got, want)
	}
	r2.Destroy()
}

// TestEmojiRasterCacheGate pins labelEmoji's routing + cache (the showname / IC-log
// colour-emoji fix): a plain label never needs the fallback, an emoji label builds
// a raster and CACHES it (a second call returns the same pointer, not a rebuild), a
// nil emoji face degrades to nil (the single-font tofu path), and purge frees the
// textures + empties the map. Uses two embedded-font instances as the two faces.
func TestEmojiRasterCacheGate(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	textFont, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Skipf("embedded font unavailable: %v", err)
	}
	defer textFont.Close()
	emojiFace, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Skipf("embedded font unavailable: %v", err)
	}
	defer emojiFace.Close()

	c := &Ctx{Ren: ren}
	col := sdl.Color{R: 240, G: 240, B: 240, A: 255}

	// The gate: a plain showname stays on the fast path; one with emoji does not.
	if render.NeedsEmojiFallback("Phoenix Wright") {
		t.Fatal("precondition: a plain showname must not need the emoji fallback")
	}
	const name = "Phoenix 😀"
	if !render.NeedsEmojiFallback(name) {
		t.Fatal("precondition: an emoji showname must need the fallback")
	}

	// Emoji label builds a raster and caches it (same pointer on the second call).
	m1 := c.emojiRaster(name, col, textFont, emojiFace)
	if m1 == nil {
		t.Fatal("emojiRaster returned nil for emoji text with a face")
	}
	if m2 := c.emojiRaster(name, col, textFont, emojiFace); m1 != m2 {
		t.Error("emojiRaster did not cache: the second call rebuilt the raster")
	}
	if n := len(c.emojiCache); n != 1 {
		t.Errorf("emojiCache size = %d, want 1", n)
	}

	// First time, no emoji face yet AND no script fallback (empty Ctx → every rune on
	// the primary): a CACHE-MISS nil-face call degrades to nil (single-font tofu). A
	// fresh string, since the face isn't in the cache key — a hit would return the
	// already-built raster, which is fine but not what this asserts.
	if m := c.emojiRaster("Fresh 😀", col, textFont, nil); m != nil {
		t.Error("emojiRaster with a nil face (cache miss, no script fallback) must degrade to nil")
	}

	// Purge frees the textures and empties the cache.
	c.purgeEmojiCache()
	if n := len(c.emojiCache); n != 0 {
		t.Errorf("after purgeEmojiCache, size = %d, want 0", n)
	}
}
