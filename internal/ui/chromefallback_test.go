package ui

import (
	"os"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestChromeLabelFallsBackForNonLatin is the "tofu everywhere" regression.
//
// Plain Label / LabelClipped / Heading rendered straight from ONE face with no
// fallback chain at all, so every non-Latin string outside the chat and log paths
// drew as .notdef boxes no matter how many fallback faces were loaded. That is
// most of the client's text: character names in the picker, area names, tab
// titles, menu entries, tooltips, player names, button captions.
//
// It asserts the pick, not pixels: with a broad face installed, a Cyrillic label
// must resolve to a DIFFERENT face than the embedded Latin one, and ASCII must
// still resolve to the embedded face so the common path is untouched.
func TestChromeLabelFallsBackForNonLatin(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	defer c.Destroy()

	// ASCII must never leave the embedded chrome face — this is the fast path that
	// ~800 call sites take on every frame.
	for _, s := range []string{"", "Objection!", "Settings", "1234567890"} {
		if got := c.chromeFaceFor(s); got != c.font {
			t.Errorf("ASCII %q resolved off the chrome face — the common path must not change", s)
		}
	}

	// Load a real broad face so the chain has something to fall back TO. Skips
	// where none of the curated candidates exist (CI, containers).
	var loaded [][]byte
	for _, p := range fallbackFontCandidates() {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			loaded = append(loaded, b)
		}
	}
	if len(loaded) == 0 {
		t.Skip("no system fallback face installed; nothing to fall back to")
	}
	c.SetFallbackFonts(loaded)

	// The embedded face has no Tifinagh. Before the fix this drew a box; now it
	// must resolve to whichever loaded face covers it.
	const tifinagh = "ⵜⵉⴼ" // ⵜⵉⴼ
	if got := c.chromeFaceFor(tifinagh); got == c.font {
		t.Error("a Tifinagh chrome label still resolves to the embedded Latin face — it will draw tofu")
	}

	// And the measurement must follow the same face, or a label is laid out on one
	// face's advances and painted with another's.
	if w := c.TextWidth(tifinagh); w <= 0 {
		t.Errorf("TextWidth(%q) = %d — a fallback label measured as nothing would collapse its layout", tifinagh, w)
	}

	// Drawing must not panic and must report a positive width.
	if w := c.Label(4, 4, tifinagh, sdl.Color{R: 255, G: 255, B: 255, A: 255}); w <= 0 {
		t.Errorf("Label drew %q at width %d, want > 0", tifinagh, w)
	}
}

// TestChromeFallbackLeavesPanelFacesAlone pins that a panel-dressed face is never
// second-guessed: the whole point of a panel font is that the user chose that
// family for that panel, so the chrome fallback must not silently swap it out.
func TestChromeFallbackLeavesPanelFacesAlone(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	defer c.Destroy()

	prev := c.pushPanelFont(c.fontBig)
	defer c.popPanelFont(prev)
	if got := c.chromeFaceFor("ⵜⵉⴼ"); got != c.fontBig {
		t.Error("a panel-dressed face was swapped by the chrome fallback")
	}
}

// TestUncoveredChromeLabelAsksTheCensus pins the hand-off that makes the chrome
// fallback self-healing rather than a one-shot.
//
// The chrome path is the ONE path that could not feed the census: it resolves
// through a single-face pick, not the per-glyph raster, so a character name in a
// script nothing loaded covers drew boxes and never asked for a font. It only came
// right by accident — if some MESSAGE elsewhere in the session happened to use the
// same script and pulled the face in.
func TestUncoveredChromeLabelAsksTheCensus(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	defer c.Destroy()

	// A chain with something in it, so the pick has more than one face to consider
	// (a single-face set short-circuits before the pick runs at all).
	var loaded [][]byte
	for _, p := range fallbackFontCandidates() {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			loaded = append(loaded, b)
		}
	}
	if len(loaded) == 0 {
		t.Skip("no system fallback face installed")
	}
	c.SetFallbackFonts(loaded)

	// U+13000 (Egyptian hieroglyph) is not in the embedded face or in the curated
	// broad tier — precisely the case the census exists for.
	const hieroglyph = "\U00013000"
	c.missingN = 0
	c.chromeFaceFor(hieroglyph)
	if c.missingN == 0 {
		t.Error("an uncoverable chrome label queued nothing — the census will never be asked, so it draws a box forever")
	}

	// And the SECOND frame must queue nothing: the pick is memoized, so this must
	// not become per-frame work.
	c.missingN = 0
	c.chromeFaceFor(hieroglyph)
	if c.missingN != 0 {
		t.Errorf("the same label queued %d rune(s) again — the census hand-off leaked onto the per-frame path", c.missingN)
	}
}

// TestChromeFallbackIsFreeForASCII pins the perf contract: the gate in front of
// every chrome fallback decision is one byte pass, so a pure-ASCII label — the
// overwhelming majority, drawn hundreds of times per frame — must allocate
// nothing and never touch the chain.
func TestChromeFallbackIsFreeForASCII(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	defer c.Destroy()

	const label = "Present Evidence"
	c.chromeFaceFor(label) // warm anything warmable
	if n := testing.AllocsPerRun(500, func() {
		_ = c.chromeFaceFor(label)
	}); n != 0 {
		t.Errorf("the ASCII chrome-label gate allocates %v per call, want 0", n)
	}
	if !hasNonASCII("é") || hasNonASCII("plain ascii 123") {
		t.Error("hasNonASCII misclassified its inputs")
	}
}
