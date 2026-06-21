package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

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

	raster, err := render.RasterizeFallback(ren, textFont, emojiFace, msg, spans, 400)
	if err != nil {
		t.Fatalf("RasterizeFallback: %v", err)
	}
	defer raster.Destroy()
	if got := raster.TotalRunes(); got != want {
		t.Errorf("TotalRunes = %d, want %d (the reveal walk needs every rune)", got, want)
	}

	// A nil emoji face (not yet loaded) must not crash — it degrades to the text font.
	r2, err := render.RasterizeFallback(ren, textFont, nil, msg, spans, 400)
	if err != nil {
		t.Fatalf("RasterizeFallback(nil emoji): %v", err)
	}
	if got := r2.TotalRunes(); got != want {
		t.Errorf("nil-face TotalRunes = %d, want %d", got, want)
	}
	r2.Destroy()
}
