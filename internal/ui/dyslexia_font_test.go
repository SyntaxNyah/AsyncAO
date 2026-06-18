package ui

import (
	"bytes"
	"os"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// TestFontChainSource pins the IC/OOC font precedence that the launch path and
// the live Settings toggle share (both resolve through applyFontConfig, which
// switches on this): the dyslexia toggle wins over a manual font path, which
// wins over the built-in font. A whitespace-only path is not a chain.
func TestFontChainSource(t *testing.T) {
	const manual = `C:\Windows\Fonts\meiryo.ttc`
	cases := []struct {
		dyslexia bool
		paths    string
		want     fontSource
	}{
		{false, "", fontSourceBuiltin},
		{false, "   ", fontSourceBuiltin}, // blank path keeps the built-in font
		{false, manual, fontSourceManual},
		{true, "", fontSourceDyslexia},
		{true, manual, fontSourceDyslexia}, // dyslexia overrides a saved manual path
	}
	for _, c := range cases {
		if got := fontChainSource(c.dyslexia, c.paths); got != c.want {
			t.Errorf("fontChainSource(%v, %q) = %d, want %d", c.dyslexia, c.paths, got, c.want)
		}
	}
}

// TestOpenDyslexicEmbedded verifies the bundled dyslexia font is present, is an
// OpenType file, and that SDL_ttf can actually open it. The byte/magic checks
// always run; the open is the real runtime risk (a missing or unreadable embed
// would otherwise only surface when a user flips the toggle in the live client).
func TestOpenDyslexicEmbedded(t *testing.T) {
	if len(openDyslexicOTF) < 50000 {
		t.Fatalf("embedded OpenDyslexic looks wrong: %d bytes (expected ~175 KB)", len(openDyslexicOTF))
	}
	// OpenType magic: "OTTO" (CFF outlines) or 0x00010000 (TrueType outlines).
	if !bytes.HasPrefix(openDyslexicOTF, []byte("OTTO")) &&
		!bytes.HasPrefix(openDyslexicOTF, []byte{0x00, 0x01, 0x00, 0x00}) {
		t.Fatalf("embedded font has no OpenType magic: % x", openDyslexicOTF[:4])
	}
	os.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	defer ttf.Quit()
	f, err := memFont(openDyslexicOTF, 16)
	if err != nil {
		t.Fatalf("SDL_ttf could not open the embedded OpenDyslexic: %v", err)
	}
	defer f.Close()
	if h := f.Height(); h <= 0 {
		t.Fatalf("opened font has non-positive height %d", h)
	}
}
