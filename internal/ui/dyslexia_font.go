package ui

import (
	_ "embed"
	"strings"
)

// openDyslexicOTF is the bundled OpenDyslexic Regular (SIL OFL 1.1 — see
// fonts/OpenDyslexic-LICENSE-OFL.txt). Embedding it makes the "dyslexia-friendly
// font" toggle work for every user with no separate install: OpenDyslexic's
// weighted letter bottoms and distinct b/d/p/q shapes cut the letter-flip
// confusion many dyslexic readers hit. We apply it to the IC/OOC chat + log text
// (the heavy reading surface) — the same override chain the manual font-path
// setting feeds — so it never disturbs chrome widget metrics; the opt-in
// "font everywhere" toggle (SetChromeFont) extends it to the chrome too.
// SDL_ttf (FreeType) reads the OTF/CFF outlines directly.
//
//go:embed fonts/OpenDyslexic-Regular.otf
var openDyslexicOTF []byte

// dyslexiaFontName labels the embedded chain in the Settings status line.
const dyslexiaFontName = "OpenDyslexic"

// fontSource is which override the current prefs select for the IC/OOC font
// chain. Resolving it in one pure place means the launch path and the live
// toggle can never disagree on precedence.
type fontSource int

const (
	fontSourceBuiltin  fontSource = iota // embedded chrome font only
	fontSourceDyslexia                   // bundled OpenDyslexic (wins over a manual path)
	fontSourceManual                     // a user-supplied font-path chain
)

// fontChainSource resolves the precedence: the dyslexia toggle wins over a
// manual font path, which wins over the built-in font. Pure, so a test can pin
// the whole truth table (including what launch will install).
func fontChainSource(dyslexiaOn bool, fontPaths string) fontSource {
	if dyslexiaOn {
		return fontSourceDyslexia
	}
	if strings.TrimSpace(fontPaths) != "" {
		return fontSourceManual
	}
	return fontSourceBuiltin
}
