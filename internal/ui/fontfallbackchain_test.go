package ui

import (
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// The bundled colour Twemoji face stands in for a system fallback font in these
// tests: the embedded face lacks its star, the star is the only coloured thing that
// can appear in a white label, and its ascent differs from the embedded face's, so
// a glyph placed by the wrong ascent lands on a different row.
const (
	fallbackLabel = "H ⭐ H" // no descender, so the white ink's bottom row is the baseline
	fallbackStar  = '⭐'
)

// newFallbackCtx is a Ctx with the embedded chrome face and Twemoji installed as
// the fallback tier, on SDL's dummy video driver.
func newFallbackCtx(t *testing.T) *Ctx {
	t.Helper()
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	if err := ttf.Init(); err != nil {
		sdl.Quit()
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	var buf sfnt.Buffer
	if coverHasRune(parseCover(goregular.TTF), &buf, fallbackStar) {
		t.Fatal("the embedded face has the star — the label would not need a fallback")
	}
	if !coverHasRune(parseCover(twemojiTTF), &buf, fallbackStar) {
		t.Fatal("the bundled Twemoji face lacks the star — the fixture is broken")
	}
	font, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Fatalf("embedded font: %v", err)
	}
	fontBig, err := loadEmbeddedFont(UIFontSizeBig)
	if err != nil {
		t.Fatalf("embedded heading font: %v", err)
	}
	c := &Ctx{font: font, fontBig: fontBig, textCache: map[textKey]cachedText{}, widthCache: map[string]int32{}}
	c.SetFallbackFonts([][]byte{twemojiTTF})
	t.Cleanup(func() {
		c.Destroy()
		ttf.Quit()
		sdl.Quit()
	})
	return c
}

// TestChromeLabelTakesMissingGlyphsFromTheFallbackFace pins per-glyph fallback, the
// way Qt resolves fonts: a label the chrome face only partly has draws through the
// chrome set's first face, and the glyph that face lacks comes from the fallback
// face. A coloured pixel in the white label proves the star came from Twemoji and
// not from the chrome face's .notdef box.
func TestChromeLabelTakesMissingGlyphsFromTheFallbackFace(t *testing.T) {
	c := newFallbackCtx(t)
	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	if top, _ := colouredRows(t, c.font, fallbackLabel, white); top >= 0 {
		t.Fatal("the chrome face alone drew a coloured pixel — the check below proves nothing")
	}
	twemoji, err := memFont(twemojiTTF, UIFontSize)
	if err != nil {
		t.Fatalf("open the Twemoji face: %v", err)
	}
	top, _ := colouredRows(t, twemoji, string(fallbackStar), white)
	twemoji.Close()
	if top < 0 {
		t.Skip("this SDL_ttf build draws no colour glyphs, so the star cannot be told from a box")
	}

	face := c.chromeFaceFor(fallbackLabel)
	if c.coversFace(face, fallbackLabel) {
		t.Error("the chrome face alone cannot draw the star, but the pick says it covers the label")
	}
	surf, err := c.renderLabel(face, face, fallbackLabel, white, false)
	if err != nil {
		t.Fatalf("renderLabel: %v", err)
	}
	defer surf.Free()
	if top, _ := surfaceColouredRows(surf); top < 0 {
		t.Error("the label drew the star as a box: it did not take the glyph from the fallback face")
	}
}

// TestFallbackGlyphSitsOnTheLabelBaseline pins the vertical half: a glyph from a
// fallback face sits on the same baseline as the primary face's letters. SDL_ttf
// 3.2.2 places it by its own font's ascent instead, which put Segoe UI Symbol
// glyphs a pixel or more low and Japanese ones high. The check is relative — the
// star's top against the H's bottom — so it holds however much room the label
// adds above.
func TestFallbackGlyphSitsOnTheLabelBaseline(t *testing.T) {
	c := newFallbackCtx(t)
	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	face := c.chromeFaceFor(fallbackLabel)
	twemoji, err := memFont(twemojiTTF, UIFontSize)
	if err != nil {
		t.Fatalf("open the Twemoji face: %v", err)
	}
	defer twemoji.Close()
	if twemoji.Ascent() == face.Ascent() {
		t.Fatal("the two faces share an ascent — a glyph placed by the wrong one would land on the same row")
	}
	starTop, _ := colouredRows(t, twemoji, string(fallbackStar), white)
	if starTop < 0 {
		t.Skip("this SDL_ttf build draws no colour glyphs, so the star cannot be located")
	}
	_, hBottom := whiteRows(t, face, "H", white)

	// Where baseline alignment puts the star relative to the H, from each glyph's
	// own font: the star's top above its baseline, the H's bottom above its own.
	want := (starTop - twemoji.Ascent()) - (hBottom - face.Ascent())

	surf, err := c.renderLabel(face, face, fallbackLabel, white, false)
	if err != nil {
		t.Fatalf("renderLabel: %v", err)
	}
	defer surf.Free()
	gotStarTop, _ := surfaceColouredRows(surf)
	_, gotHBottom := surfaceWhiteRows(surf)
	if got := gotStarTop - gotHBottom; got != want {
		t.Errorf("star top is %d rows from the H's bottom, want %d — the fallback glyph is off the baseline", got, want)
	}
}

// colouredRows renders text through f and returns the first and last rows holding a
// coloured pixel, or -1, -1 when there is none.
func colouredRows(t *testing.T, f *ttf.Font, text string, col sdl.Color) (top, bottom int) {
	t.Helper()
	surf, err := f.RenderUTF8Blended(text, col)
	if err != nil {
		t.Fatalf("render %q: %v", text, err)
	}
	defer surf.Free()
	return surfaceColouredRows(surf)
}

// whiteRows is colouredRows for the grey/white ink of ordinary text.
func whiteRows(t *testing.T, f *ttf.Font, text string, col sdl.Color) (top, bottom int) {
	t.Helper()
	surf, err := f.RenderUTF8Blended(text, col)
	if err != nil {
		t.Fatalf("render %q: %v", text, err)
	}
	defer surf.Free()
	return surfaceWhiteRows(surf)
}

func surfaceColouredRows(surf *sdl.Surface) (top, bottom int) {
	return surfaceRows(surf, func(spread uint32) bool { return spread >= minHue })
}

func surfaceWhiteRows(surf *sdl.Surface) (top, bottom int) {
	return surfaceRows(surf, func(spread uint32) bool { return spread < minHue })
}

const (
	minAlpha = 128 // ignore the anti-aliased fringe
	minHue   = 64  // channel spread that a grey pixel never reaches
)

// surfaceRows returns the first and last rows with a visible pixel whose channel
// spread (max−min) satisfies keep, or -1, -1 when there is none.
func surfaceRows(surf *sdl.Surface, keep func(spread uint32) bool) (top, bottom int) {
	top, bottom = -1, -1
	for y := 0; y < int(surf.H); y++ {
		for x := 0; x < int(surf.W); x++ {
			r, g, b, a := surf.At(x, y).RGBA()
			r, g, b, a = r>>8, g>>8, b>>8, a>>8
			if a < minAlpha || !keep(max(r, g, b)-min(r, g, b)) {
				continue
			}
			if top < 0 {
				top = y
			}
			bottom = y
			break
		}
	}
	return top, bottom
}
