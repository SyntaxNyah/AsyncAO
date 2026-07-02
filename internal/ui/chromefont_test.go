package ui

import (
	"os"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// TestSetChromeFontSwapAndLastResort pins the whole-UI font swap ("font
// everywhere"): installing override bytes swaps the chrome faces, purges the
// pointer-keyed caches, and STOPS the chat/log sets sharing c.font as their
// embedded last resort (a custom chrome must never stand in for goregular's
// coverage — the cover entry in that slot is the embedded cmap). Clearing
// restores the shared fast path, and re-installing the same slice is a no-op.
func TestSetChromeFontSwapAndLastResort(t *testing.T) {
	os.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	defer ttf.Quit()

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

	// Embedded default: the 100% chat set SHARES the chrome font (the
	// no-duplicate-rasters fast path).
	if got := c.ChatFont(DefaultScalePct); got != c.font {
		t.Fatal("with the embedded chrome, the 100% chat set must share c.font")
	}
	embedded := c.font
	c.widthCache["probe"] = 42

	if !c.SetChromeFont(openDyslexicOTF) {
		t.Fatal("SetChromeFont(openDyslexicOTF) must succeed")
	}
	if c.font == embedded {
		t.Error("chrome font must have swapped to the override face")
	}
	if _, stale := c.widthCache["probe"]; stale {
		t.Error("the width memo must purge on a chrome swap (metrics changed)")
	}
	if got := c.ChatFont(DefaultScalePct); got == c.font {
		t.Error("a CUSTOM chrome font must not double as the chat set's embedded last resort")
	}

	// Idempotence: the same slice again must be a free no-op (applyFontConfig
	// re-runs on unrelated settings changes).
	before := c.font
	if !c.SetChromeFont(openDyslexicOTF) {
		t.Error("same-bytes SetChromeFont must report success")
	}
	if c.font != before {
		t.Error("same-bytes SetChromeFont must not rebuild the chrome faces")
	}

	// Clearing restores the embedded font and the shared fast path.
	if !c.SetChromeFont(nil) {
		t.Fatal("SetChromeFont(nil) must succeed")
	}
	if got := c.ChatFont(DefaultScalePct); got != c.font {
		t.Error("with the chrome back on embedded, the 100% chat set must share c.font again")
	}
}

// TestSetChromeFontRejectsGarbage pins the failure path: bytes that aren't a
// font leave the current chrome (and the caches) completely untouched.
func TestSetChromeFontRejectsGarbage(t *testing.T) {
	os.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	defer ttf.Quit()

	font, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Fatalf("embedded font: %v", err)
	}
	c := &Ctx{font: font, textCache: map[textKey]cachedText{}, widthCache: map[string]int32{}}
	c.widthCache["keep"] = 7

	if c.SetChromeFont([]byte("this is not a font file at all")) {
		t.Fatal("garbage bytes must be rejected")
	}
	if c.font != font || c.chromeData != nil {
		t.Error("a rejected install must leave the chrome untouched")
	}
	if _, ok := c.widthCache["keep"]; !ok {
		t.Error("a rejected install must not purge the caches")
	}
}
