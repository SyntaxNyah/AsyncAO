package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// TestColorFontRenderable pins the COLRv1 detection driving the emoji fallback (the "empty boxes"
// fix): the bundled Twemoji is COLRv0 (renderable); a COLRv1-only font — Windows' 2025 Segoe UI
// Emoji — is NOT; a COLRv0 or a CBDT-bitmap font is.
func TestColorFontRenderable(t *testing.T) {
	if !colorFontRenderable(twemojiTTF) {
		t.Error("bundled Twemoji (COLRv0) must be renderable")
	}
	if colorFontRenderable(synthSfnt("COLR", 1)) {
		t.Error("a COLRv1-only font must read as NOT renderable (SDL_ttf draws it as boxes)")
	}
	if !colorFontRenderable(synthSfnt("COLR", 0)) {
		t.Error("a COLRv0 font must be renderable")
	}
	if !colorFontRenderable(synthSfnt("CBDT", 0)) {
		t.Error("a CBDT bitmap colour font must be renderable")
	}
}

// synthSfnt builds a 1-table sfnt blob: the tag's directory record points at a 2-byte body holding
// val (read as the COLR version by colorFontRenderable).
func synthSfnt(tag string, val uint16) []byte {
	const dir = 12 + 16 // sfnt header + one 16-byte table record
	b := make([]byte, dir+2)
	b[5] = 1 // numTables = 1
	copy(b[12:16], tag)
	b[12+8], b[12+9], b[12+10], b[12+11] = byte(dir>>24), byte(dir>>16), byte(dir>>8), byte(dir) // table offset
	b[dir], b[dir+1] = byte(val>>8), byte(val)
	return b
}

// TestBundledEmojiRendersInColour is the headless proof the fix actually works: SDL_ttf renders
// the bundled Twemoji as COLOUR (the bug was monochrome tofu boxes). It loads the embedded face,
// renders an emoji, and asserts the surface has a chromatic pixel (R≠G or G≠B) — a tofu box is
// flat/monochrome.
func TestBundledEmojiRendersInColour(t *testing.T) {
	_, cleanup := newCaptureHarness(t) // SDL + ttf init (dummy driver)
	defer cleanup()

	rw, err := sdl.RWFromMem(twemojiTTF)
	if err != nil {
		t.Fatalf("RWFromMem: %v", err)
	}
	face, err := ttf.OpenFontRW(rw, 1, 64)
	if err != nil {
		t.Skipf("open Twemoji face: %v", err)
	}
	defer face.Close()

	surf, err := face.RenderUTF8Blended("\U0001F600", sdl.Color{R: 255, G: 255, B: 255, A: 255}) // 😀
	if err != nil {
		t.Fatalf("render emoji glyph: %v", err)
	}
	defer surf.Free()

	chromatic := false
	for y := 0; y < int(surf.H) && !chromatic; y++ {
		for x := 0; x < int(surf.W); x++ {
			r, g, b, a := surf.At(x, y).RGBA()
			if a == 0 {
				continue // transparent padding around the glyph
			}
			if r != g || g != b {
				chromatic = true
				break
			}
		}
	}
	if !chromatic {
		t.Error("bundled emoji rendered with NO colour — SDL_ttf isn't drawing Twemoji in colour, so the box-fix doesn't hold")
	}
}
