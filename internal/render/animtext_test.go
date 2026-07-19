package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
	"golang.org/x/image/font/gofont/gomono"
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

// openFontBytes opens a second face (no ttf.Init/Quit — the caller's newAnimTestFont owns
// that) so a test can stand in a distinct "fallback face" for the resolver: the resolved
// per-rune font differs from the base, proving mixed fonts coexist. Skips if it won't open.
func openFontBytes(t testing.TB, data []byte, size int) *ttf.Font {
	t.Helper()
	rw, err := sdl.RWFromMem(data)
	if err != nil {
		t.Skipf("rw: %v", err)
	}
	f, err := ttf.OpenFontRW(rw, 1, size)
	if err != nil {
		t.Skipf("open font: %v", err)
	}
	return f
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
	at := RasterizeAnimated(font, text, spans, []sdl.Color{white}, 1000, nil, 0)
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
	at := RasterizeAnimated(font, text, []EffectSpan{{0, len([]rune(text)), EffectRainbow}}, []sdl.Color{{R: 255, G: 255, B: 255, A: 255}}, 1000, nil, 0)
	at.Draw(ren, gc, font, 0, at.total, 10, 10, false) // warm
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at.Draw(ren, gc, font, time.Duration(i)*time.Millisecond, at.total, 10, 10, false)
	}
}

// TestAnimatedTextAnimates pins the frame-pacing census contract (the FX-text-freezes-at-
// idle fix): clock-driven effects report Animates so the chatbox draw keeps frames coming,
// while gradient-only / effect-free layouts and reduce-motion report static (a parked
// chatbox stays free). EffectAnimates is the single source of truth and must agree with
// Draw's switch — the per-id sweep below catches a new effect missing a classification.
func TestAnimatedTextAnimates(t *testing.T) {
	font, fcleanup := newAnimTestFont(t, 18)
	defer fcleanup()
	white := []sdl.Color{{R: 255, G: 255, B: 255, A: 255}}

	rainbow := RasterizeAnimated(font, "abcdef", []EffectSpan{{0, 6, EffectRainbow}}, white, 1000, nil, 0)
	if !rainbow.Animates(false) {
		t.Error("rainbow must animate (it froze between idle=0 redraws)")
	}
	if rainbow.Animates(true) {
		t.Error("reduce-motion renders rainbow static — it must not hold frames")
	}
	if shake := RasterizeAnimated(font, "abcdef", []EffectSpan{{2, 2, EffectShake}}, white, 1000, nil, 0); !shake.Animates(false) {
		t.Error("a partial shake span must animate")
	}
	if gradient := RasterizeAnimated(font, "abcdef", []EffectSpan{{0, 6, EffectGradient}}, white, 1000, nil, 0); gradient.Animates(false) {
		t.Error("gradient is a static band — it must not hold frames")
	}
	if plain := RasterizeAnimated(font, "abcdef", nil, white, 1000, nil, 0); plain.Animates(false) {
		t.Error("an effect-free layout must not hold frames")
	}
	// A span entirely past the visible text tags no rune → static (rune-accurate census).
	if past := RasterizeAnimated(font, "ab", []EffectSpan{{10, 3, EffectShake}}, white, 1000, nil, 0); past.Animates(false) {
		t.Error("a span past the text must not hold frames")
	}
	for e := EffectShake; e <= EffectSparkle; e++ {
		want := e != EffectGradient
		if got := EffectAnimates(e, false); got != want {
			t.Errorf("EffectAnimates(%d) = %v, want %v", e, got, want)
		}
		if EffectAnimates(e, true) {
			t.Errorf("EffectAnimates(%d, reduceMotion) = true, want false (everything renders static)", e)
		}
	}
	if EffectAnimates(EffectNone, false) {
		t.Error("EffectNone must be static")
	}
}

// TestRasterizeAnimatedLayout pins the layout: rune count, per-span effect assignment, and
// left-to-right x positions.
func TestRasterizeAnimatedLayout(t *testing.T) {
	font, fcleanup := newAnimTestFont(t, 18)
	defer fcleanup()
	at := RasterizeAnimated(font, "abcdef", []EffectSpan{{2, 2, EffectWave}}, []sdl.Color{{R: 255, G: 255, B: 255, A: 255}}, 1000, nil, 0)
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
	at := RasterizeAnimated(font, "abcdef", nil, cols, 1000, nil, 0)
	for i, want := range cols {
		if at.runes[i].color != want {
			t.Errorf("rune %d colour = %v, want %v", i, at.runes[i].color, want)
		}
	}

	at = RasterizeAnimated(font, "abc", nil, []sdl.Color{grn}, 1000, nil, 0) // single → uniform
	for i := range at.runes {
		if at.runes[i].color != grn {
			t.Errorf("uniform rune %d colour = %v, want green", i, at.runes[i].color)
		}
	}

	at = RasterizeAnimated(font, "abcde", nil, []sdl.Color{red, grn}, 1000, nil, 0) // short → tail clamps
	if at.runes[4].color != grn {
		t.Errorf("clamped tail colour = %v, want green", at.runes[4].color)
	}
}

// TestRasterizeAnimatedPerRuneFonts is the Task-E core: with a resolver present, each rune
// stores the face the resolver returned (a fallback face for the runes it lacks, the emoji
// face for an emoji rune) instead of the single base font — so emoji stop being tofu and CJK
// stops getting one base-font advance for every rune. gomono stands in for a "fallback face";
// the emoji face is flagged no-tint. When resolve is nil every rune keeps the base font.
func TestRasterizeAnimatedPerRuneFonts(t *testing.T) {
	base, cleanup := newAnimTestFont(t, 18)
	defer cleanup()
	fallback := openFontBytes(t, gomono.TTF, 18)
	defer fallback.Close()
	emojiFace := openFontBytes(t, gomono.TTF, 22) // distinct pointer + size = a stand-in emoji face
	defer emojiFace.Close()

	// Runes 2,3 route to the fallback face; rune 4 is a flagged "emoji"; the rest stay base.
	text := "aabbe"
	resolve := func(gi int, _ rune) (*ttf.Font, bool) {
		switch gi {
		case 2, 3:
			return fallback, false
		case 4:
			return emojiFace, true
		}
		return base, false
	}
	at := RasterizeAnimated(base, text, nil, []sdl.Color{{R: 255, G: 255, B: 255, A: 255}}, 1000, resolve, 7)

	if at.ChainGen() != 7 {
		t.Errorf("ChainGen = %d, want 7 (font-chain stamp for lazy-tier re-raster)", at.ChainGen())
	}
	if at.TotalRunes() != 5 {
		t.Fatalf("TotalRunes = %d, want 5", at.TotalRunes())
	}
	wantFont := []*ttf.Font{base, base, fallback, fallback, emojiFace}
	for i, wf := range wantFont {
		if at.runes[i].font != wf {
			t.Errorf("rune %d font = %p, want %p (per-rune resolution, not the base font)", i, at.runes[i].font, wf)
		}
	}
	// The fallback runes MUST NOT be on the base font — that's exactly the CJK/emoji-as-base
	// bug this fixes.
	if at.runes[2].font == base {
		t.Error("a resolved fallback rune stayed on the base font — the tofu/uniform-advance bug")
	}
	// Colour-emoji rune is flagged (Draw skips its tint); text runes are not.
	if !at.runes[4].emoji {
		t.Error("the emoji rune is not flagged — rainbow/gradient would discolour the emoji artwork")
	}
	for i := 0; i < 4; i++ {
		if at.runes[i].emoji {
			t.Errorf("text rune %d flagged emoji", i)
		}
	}

	// nil resolver → every rune keeps the base font (pre-fallback behaviour, single-font path).
	plain := RasterizeAnimated(base, text, nil, []sdl.Color{{R: 255, G: 255, B: 255, A: 255}}, 1000, nil, 0)
	for i := range plain.runes {
		if plain.runes[i].font != nil && plain.runes[i].font != base {
			t.Errorf("nil-resolver rune %d resolved to %p, want base/nil", i, plain.runes[i].font)
		}
	}
}

// TestRasterizeAnimatedNonUniformAdvance pins that the pen advances by each rune's OWN font:
// a proportional base + a fixed-width fallback produce a non-uniform run of x positions, so
// CJK (which used to get one uniform base advance per rune) lays out at real widths. Also
// pins left-to-right monotonicity across the mixed faces.
func TestRasterizeAnimatedNonUniformAdvance(t *testing.T) {
	base, cleanup := newAnimTestFont(t, 18)
	defer cleanup()
	fallback := openFontBytes(t, gomono.TTF, 18)
	defer fallback.Close()

	// "iWmix" on the PROPORTIONAL base already varies per rune; route the tail to the mono
	// face so more than one font contributes to the advances.
	text := "iWmix"
	resolve := func(gi int, _ rune) (*ttf.Font, bool) {
		if gi >= 3 {
			return fallback, false
		}
		return base, false
	}
	at := RasterizeAnimated(base, text, nil, []sdl.Color{{R: 255, G: 255, B: 255, A: 255}}, 1000, resolve, 0)
	if at.TotalRunes() != 5 {
		t.Fatalf("TotalRunes = %d, want 5", at.TotalRunes())
	}
	// Per-rune advances (x deltas) — must not all be equal (the uniform-advance regression).
	var deltas []int32
	for i := 1; i < len(at.runes); i++ {
		if at.runes[i].x < at.runes[i-1].x {
			t.Errorf("rune %d x=%d < prev %d (must be left to right)", i, at.runes[i].x, at.runes[i-1].x)
		}
		deltas = append(deltas, at.runes[i].x-at.runes[i-1].x)
	}
	uniform := true
	for _, d := range deltas[1:] {
		if d != deltas[0] {
			uniform = false
			break
		}
	}
	if uniform {
		t.Errorf("all advances identical (%v) — CJK/mixed text would render monospaced/stretched", deltas)
	}
}

// TestRasterizeAnimatedBaseline pins the shared-baseline align: with faces of differing
// ascents on one line, each rune's y + yOff + face-ascent (its baseline) lands on the SAME
// line within a small tolerance, so CJK/emoji don't ride high or low next to Latin.
func TestRasterizeAnimatedBaseline(t *testing.T) {
	base, cleanup := newAnimTestFont(t, 18)
	defer cleanup()
	tall := openFontBytes(t, gomono.TTF, 30) // a taller face → a larger ascent than base
	defer tall.Close()

	if int32(tall.Ascent()) == int32(base.Ascent()) {
		t.Skip("test faces have equal ascent — nothing to align (baseline test is a no-op)")
	}
	text := "AbCd"
	resolve := func(gi int, _ rune) (*ttf.Font, bool) {
		if gi%2 == 1 {
			return tall, false // alternate runes onto the taller face
		}
		return base, false
	}
	at := RasterizeAnimated(base, text, nil, []sdl.Color{{R: 255, G: 255, B: 255, A: 255}}, 1000, resolve, 0)
	// Baseline of rune i = y + yOff + face.Ascent(). All same-line runes must share it.
	faceOf := func(i int) *ttf.Font {
		if i%2 == 1 {
			return tall
		}
		return base
	}
	const tol = 1 // int rounding across faces
	want := at.runes[0].y + at.runes[0].yOff + int32(base.Ascent())
	for i := range at.runes {
		got := at.runes[i].y + at.runes[i].yOff + int32(faceOf(i).Ascent())
		if d := got - want; d < -tol || d > tol {
			t.Errorf("rune %d baseline = %d, want %d±%d — mixed faces ride high/low", i, got, want, tol)
		}
	}
}
