package ui

import (
	"os"
	"testing"

	"github.com/veandco/go-sdl2/ttf"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
)

// sfntCovers reports whether the parsed face has a real glyph (cmap index != 0) for r
// — the reliable coverage check the PICK uses, since SDL_ttf's GlyphMetrics returns
// .notdef metrics WITHOUT error for missing glyphs in this build and so can't tell.
func sfntCovers(t *testing.T, f *sfnt.Font, r rune) bool {
	t.Helper()
	var buf sfnt.Buffer
	idx, err := f.GlyphIndex(&buf, r)
	return err == nil && idx != 0
}

// TestFontCoverageMatrix is the empirical ground truth the broad-Unicode fix rests on,
// using the SAME sfnt cmap check the renderer's pick uses:
//   - the embedded goregular face covers Latin/Greek/Cyrillic but NOT Tifinagh/CJK
//     (so a covering fallback is genuinely required, and the cheap load-trigger gate
//     is reasoned about correctly), and
//   - Ebrima really covers the user's Tifinagh sample, Segoe UI covers Cyrillic.
//
// Each system font is skipped if not installed (CI / non-Windows), but the embedded
// assertions always run.
func TestFontCoverageMatrix(t *testing.T) {
	const (
		tifinaghYath = 0x2D5C // ⵜ — from the reported ⵜⵉⴼⴻⵔⴰ
		cyrillicA    = 0x0410 // А
		devanagariA  = 0x0905 // अ
		cjkYi        = 0x4E00 // 一
	)

	emb := parseCover(goregular.TTF)
	if emb == nil {
		t.Fatal("parseCover(goregular) returned nil — sfnt can't read the embedded font")
	}
	// What the embedded font MUST and MUST NOT cover — the basis for the whole fix.
	if !sfntCovers(t, emb, 'A') || !sfntCovers(t, emb, cyrillicA) {
		t.Error("embedded font should cover Latin + Cyrillic")
	}
	if sfntCovers(t, emb, tifinaghYath) {
		t.Error("embedded font unexpectedly covers Tifinagh — the fallback would be pointless")
	}
	if sfntCovers(t, emb, cjkYi) {
		t.Error("embedded font unexpectedly covers CJK — coverage assumption wrong")
	}

	// Per-candidate coverage claims (skip the absent ones).
	want := map[string][]rune{
		`C:\Windows\Fonts\segoeui.ttf`: {cyrillicA},    // broad European
		`C:\Windows\Fonts\ebrima.ttf`:  {tifinaghYath}, // THE reported Tifinagh gap
		`C:\Windows\Fonts\Nirmala.ttc`: {devanagariA},  // Indic (a .ttc collection)
	}
	for path, runes := range want {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Logf("skip %s: not installed", path)
			continue
		}
		f := parseCover(b)
		if f == nil {
			t.Errorf("parseCover(%s) returned nil", path)
			continue
		}
		for _, r := range runes {
			if !sfntCovers(t, f, r) {
				t.Errorf("%s should cover U+%04X but doesn't", path, r)
			}
		}
	}
}

// TestPickFontSelectsCoveringFace pins the actual fix end-to-end: given a set ordered
// [embedded, ebrima, last] with aligned sfnt covers, the picker keeps Latin/Cyrillic on
// the EMBEDDED face (its look is preserved) and routes a Tifinagh rune to EBRIMA — the
// exact behaviour the reported ⵜⵉⴼⴻⵔⴰ tofu needs. Skips if Ebrima isn't installed.
func TestPickFontSelectsCoveringFace(t *testing.T) {
	if err := ttf.Init(); err != nil {
		t.Skipf("ttf init: %v", err)
	}
	defer ttf.Quit()

	ebrimaBytes, err := os.ReadFile(`C:\Windows\Fonts\ebrima.ttf`)
	if err != nil {
		t.Skip("Ebrima not installed")
	}
	const sz = 18
	embedded, err := loadEmbeddedFont(sz)
	if err != nil {
		t.Fatalf("embedded: %v", err)
	}
	defer embedded.Close()
	ebrima, err := memFont(ebrimaBytes, sz)
	if err != nil {
		t.Fatalf("ebrima: %v", err)
	}
	defer ebrima.Close()

	// Order mirrors a real set: embedded, then the broad fallback, then the
	// unconditional last entry. Covers aligned by index.
	fonts := []*ttf.Font{embedded, ebrima, embedded}
	cover := []*sfnt.Font{parseCover(goregular.TTF), parseCover(ebrimaBytes), nil}
	var buf sfnt.Buffer

	cases := []struct {
		name string
		text string
		want *ttf.Font
	}{
		{"latin stays embedded", "Hello", embedded},
		{"cyrillic stays embedded", "Привет", embedded},
		{"tifinagh routes to ebrima", "ⵜⵉⴼⴻⵔⴰ", ebrima},
	}
	for _, tc := range cases {
		if got := pickFont(fonts, cover, &buf, tc.text); got != tc.want {
			t.Errorf("%s: pickFont picked the wrong face", tc.name)
		}
	}
}
