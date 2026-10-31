package ui

import (
	_ "embed"
	"strings"
)

// openDyslexicOTF is the bundled OpenDyslexic Regular (SIL OFL 1.1, fonts/OpenDyslexic-LICENSE-OFL.txt), so the
// dyslexia toggle needs no install. It applies to IC/OOC chat and log text through the manual font-path override
// chain, leaving chrome metrics alone unless "font everywhere" (SetChromeFont) is on.
//
//go:embed fonts/OpenDyslexic-Regular.otf
var openDyslexicOTF []byte

// dyslexiaFontName labels the embedded chain in the Settings status line.
const dyslexiaFontName = "OpenDyslexic"

// fontSource is which override the prefs select for the IC/OOC font chain; one pure place so launch and the live
// toggle agree.
type fontSource int

const (
	fontSourceBuiltin  fontSource = iota // embedded chrome font only
	fontSourceDyslexia                   // bundled OpenDyslexic (wins over a manual path)
	fontSourceManual                     // a user-supplied font-path chain
)

// fontChainSource resolves precedence: dyslexia toggle, then manual font path, then the built-in font. Pure.
func fontChainSource(dyslexiaOn bool, fontPaths string) fontSource {
	if dyslexiaOn {
		return fontSourceDyslexia
	}
	if strings.TrimSpace(fontPaths) != "" {
		return fontSourceManual
	}
	return fontSourceBuiltin
}
