package ui

import (
	"os"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// TestChromeLabelTakesMissingGlyphsFromTheFallbackFace pins per-glyph fallback, the
// way Qt resolves fonts: a chrome label whose text the chrome face only partly has
// draws as ONE string through the chrome set's first face, and the glyph that face
// lacks comes from a fallback face. The bundled colour Twemoji face stands in for a
// system fallback font: its star is the only coloured thing that can appear, so a
// coloured pixel in the label proves the glyph came from the fallback and not from
// the chrome face's .notdef box.
func TestChromeLabelTakesMissingGlyphsFromTheFallbackFace(t *testing.T) {
	const (
		label = "Phoenix ⭐ Wright"
		star  = '⭐'
	)
	os.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	defer ttf.Quit()

	var buf sfnt.Buffer
	if coverHasRune(parseCover(goregular.TTF), &buf, star) {
		t.Fatal("the embedded face has the star — the label would not need a fallback")
	}
	if !coverHasRune(parseCover(twemojiTTF), &buf, star) {
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
	c := &Ctx{
		font:       font,
		fontBig:    fontBig,
		textCache:  map[textKey]cachedText{},
		widthCache: map[string]int32{},
	}
	defer c.Destroy()

	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	if hasColouredPixel(t, font, label, white) {
		t.Fatal("the chrome face alone drew a coloured pixel — the check below proves nothing")
	}
	twemoji, err := memFont(twemojiTTF, UIFontSize)
	if err != nil {
		t.Fatalf("open the Twemoji face: %v", err)
	}
	colourDrawn := hasColouredPixel(t, twemoji, string(star), white)
	twemoji.Close()
	if !colourDrawn {
		t.Skip("this SDL_ttf build draws no colour glyphs, so the star cannot be told from a box")
	}

	c.SetFallbackFonts([][]byte{twemojiTTF})
	face := c.chromeFaceFor(label)
	if face == nil {
		t.Fatal("chromeFaceFor returned no face")
	}
	if !c.coversFace(face, label) {
		t.Error("the chain has every rune of the label, but the pick says it does not")
	}
	if !hasColouredPixel(t, face, label, white) {
		t.Error("the label drew the star as a box: the chrome face did not take it from the fallback face")
	}

	// A fallback load bumps the chain generation and rebuilds the set; the new
	// first face must carry the chain again and the old one must not be reused.
	c.SetFallbackFonts([][]byte{twemojiTTF})
	if rebuilt := c.chromeFaceFor(label); !hasColouredPixel(t, rebuilt, label, white) {
		t.Error("after a chain rebuild the label drew the star as a box")
	}
}

// hasColouredPixel renders text in col (white) through f and reports whether any
// visible pixel has a hue: a text glyph or a .notdef box comes out grey, a colour
// emoji does not.
func hasColouredPixel(t *testing.T, f *ttf.Font, text string, col sdl.Color) bool {
	t.Helper()
	const (
		minAlpha = 128 // ignore the anti-aliased fringe
		minHue   = 64  // channel spread that a grey pixel never reaches
	)
	surf, err := f.RenderUTF8Blended(text, col)
	if err != nil {
		t.Fatalf("render %q: %v", text, err)
	}
	defer surf.Free()
	for y := 0; y < int(surf.H); y++ {
		for x := 0; x < int(surf.W); x++ {
			r, g, b, a := surf.At(x, y).RGBA()
			r, g, b, a = r>>8, g>>8, b>>8, a>>8
			if a < minAlpha {
				continue
			}
			if max(r, g, b)-min(r, g, b) >= minHue {
				return true
			}
		}
	}
	return false
}
