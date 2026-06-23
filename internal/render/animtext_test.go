package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
	"golang.org/x/image/font/gofont/goregular"
)

func newAnimTestFont(t testing.TB, size int) (*ttf.Font, func()) {
	t.Helper()
	if err := ttf.Init(); err != nil {
		t.Skipf("ttf init: %v", err)
	}
	rw, err := sdl.RWFromMem(goregular.TTF)
	if err != nil {
		ttf.Quit()
		t.Fatalf("rw: %v", err)
	}
	font, err := ttf.OpenFontRW(rw, 1, size)
	if err != nil {
		ttf.Quit()
		t.Skipf("open font: %v", err)
	}
	return font, func() { font.Close(); ttf.Quit() }
}

// TestAnimatedTextDrawZeroAllocs is THE gate for #M5: the animated draw path is GATED out
// of BenchmarkRenderFrame (that renders plain text), so this is the only test that proves
// "no performance degradation" for the feature — drawing an animated message must allocate
// NOTHING per frame on a warm glyph cache. All three effects run so every branch is covered.
func TestAnimatedTextDrawZeroAllocs(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	font, fcleanup := newAnimTestFont(t, 18)
	defer fcleanup()
	gc := NewGlyphCache(512)
	defer gc.Purge()

	text := "The quick brown fox jumps over the lazy dog!"
	n := len([]rune(text))
	third := n / 3
	spans := []EffectSpan{
		{0, third, EffectShake},
		{third, third, EffectWave},
		{2 * third, n - 2*third, EffectRainbow},
	}
	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	at := RasterizeAnimated(font, text, spans, []sdl.Color{white}, 1000)
	at.Draw(ren, gc, font, 0, at.total, 10, 10, false) // warm the glyph cache

	if allocs := testing.AllocsPerRun(200, func() {
		at.Draw(ren, gc, font, 123*time.Millisecond, at.total, 10, 10, false)
	}); allocs != 0 {
		t.Errorf("AnimatedText.Draw allocated %.1f/op, want 0 — the per-frame path must not allocate", allocs)
	}
	// reduce-motion path must also be allocation-free.
	if allocs := testing.AllocsPerRun(200, func() {
		at.Draw(ren, gc, font, 123*time.Millisecond, at.total, 10, 10, true)
	}); allocs != 0 {
		t.Errorf("AnimatedText.Draw (reduce-motion) allocated %.1f/op, want 0", allocs)
	}
}

// BenchmarkAnimatedTextDraw is the per-frame cost of one animated message.
func BenchmarkAnimatedTextDraw(b *testing.B) {
	ren, cleanup := newHeadlessRenderer(b)
	defer cleanup()
	font, fcleanup := newAnimTestFont(b, 18)
	defer fcleanup()
	gc := NewGlyphCache(512)
	defer gc.Purge()
	text := "The quick brown fox jumps over the lazy dog!"
	at := RasterizeAnimated(font, text, []EffectSpan{{0, len([]rune(text)), EffectRainbow}}, []sdl.Color{{R: 255, G: 255, B: 255, A: 255}}, 1000)
	at.Draw(ren, gc, font, 0, at.total, 10, 10, false) // warm
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at.Draw(ren, gc, font, time.Duration(i)*time.Millisecond, at.total, 10, 10, false)
	}
}

// TestRasterizeAnimatedLayout pins the layout: rune count, per-span effect assignment, and
// left-to-right x positions.
func TestRasterizeAnimatedLayout(t *testing.T) {
	font, fcleanup := newAnimTestFont(t, 18)
	defer fcleanup()
	at := RasterizeAnimated(font, "abcdef", []EffectSpan{{2, 2, EffectWave}}, []sdl.Color{{R: 255, G: 255, B: 255, A: 255}}, 1000)
	if at.TotalRunes() != 6 {
		t.Fatalf("TotalRunes = %d, want 6", at.TotalRunes())
	}
	want := []uint8{EffectNone, EffectNone, EffectWave, EffectWave, EffectNone, EffectNone}
	for i := range want {
		if at.runes[i].effect != want[i] {
			t.Errorf("rune %d effect = %d, want %d", i, at.runes[i].effect, want[i])
		}
	}
	for i := 1; i < len(at.runes); i++ {
		if at.runes[i].x < at.runes[i-1].x {
			t.Errorf("rune %d x=%d < prev x=%d (must be left to right)", i, at.runes[i].x, at.runes[i-1].x)
		}
	}
}

// TestRasterizeAnimatedColors pins colour composition (#M5 finish): each glyph takes its
// per-rune colour so \cN composes with shake/wave; a single-element slice paints uniformly;
// a short slice clamps the wrapped/overflow tail to the last colour.
func TestRasterizeAnimatedColors(t *testing.T) {
	font, fcleanup := newAnimTestFont(t, 18)
	defer fcleanup()
	red := sdl.Color{R: 255, A: 255}
	grn := sdl.Color{G: 255, A: 255}
	blu := sdl.Color{B: 255, A: 255}

	cols := []sdl.Color{red, red, grn, grn, blu, blu}
	at := RasterizeAnimated(font, "abcdef", nil, cols, 1000)
	for i, want := range cols {
		if at.runes[i].color != want {
			t.Errorf("rune %d colour = %v, want %v", i, at.runes[i].color, want)
		}
	}

	at = RasterizeAnimated(font, "abc", nil, []sdl.Color{grn}, 1000) // single → uniform
	for i := range at.runes {
		if at.runes[i].color != grn {
			t.Errorf("uniform rune %d colour = %v, want green", i, at.runes[i].color)
		}
	}

	at = RasterizeAnimated(font, "abcde", nil, []sdl.Color{red, grn}, 1000) // short → tail clamps
	if at.runes[4].color != grn {
		t.Errorf("clamped tail colour = %v, want green", at.runes[4].color)
	}
}
