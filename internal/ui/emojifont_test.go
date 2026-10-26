package ui

import (
	"testing"

	"golang.org/x/image/font/sfnt"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// The bundled colour-emoji face is a committed binary, so these are the tests
// that stand in for reading it. Every one of them guards a failure mode that is
// SILENT in the running client: colorFontRenderable rejects a bad font and quietly
// falls back, so a wrong build ships as "emoji stopped working" with nothing in
// any log. Rebuild the font with scripts/build-emoji-font.ps1 and re-run these.

// TestBundledEmojiFontIsRenderable pins the hard constraint. SDL_ttf cannot render
// COLRv1 — that is the whole reason Windows' own 11.9 MB Segoe UI Emoji is passed
// over for a 1.7 MB bundled face — so the bundled font MUST be COLRv0 (or a
// bitmap strike). If this fails, every emoji in the client is an empty box.
func TestBundledEmojiFontIsRenderable(t *testing.T) {
	if !colorFontRenderable(twemojiTTF) {
		t.Fatal("the bundled emoji font is not renderable by SDL_ttf — it must be COLR version 0 (or CBDT/sbix). " +
			"nanoemoji defaults to COLRv1; check color_format = \"glyf_colr_0\" in _emojibuild/twemoji.toml")
	}
}

// TestBundledEmojiFontParsesForCoverage pins the OTHER consumer of the font: the
// coverage probe reads it through x/image/font/sfnt, which supports only cmap
// formats 0, 4, 6 and 12. A font whose cmap the probe cannot read would report
// every emoji as missing and send the census chasing fonts it does not need.
func TestBundledEmojiFontParsesForCoverage(t *testing.T) {
	if f := parseCover(twemojiTTF); f == nil {
		t.Fatal("sfnt cannot parse the bundled emoji font — the coverage probe would report every emoji missing")
	}
}

// emojiEra is one codepoint plus the Emoji release that introduced it.
type emojiEra struct {
	ver  string
	name string
	r    rune
}

// modernEmoji are single codepoints added AFTER the font this replaced was built
// (it was frozen at Emoji 14.0, 2021). Each of these was measured drawing as a
// tofu box before the rebuild.
var modernEmoji = []emojiEra{
	{"15.0", "shaking face", 0x1FAE8},
	{"15.0", "jellyfish", 0x1FABC},
	{"15.0", "wing", 0x1FABD},
	{"15.0", "goose", 0x1FABF},
	{"15.0", "pink heart", 0x1FA77},
	{"16.0", "face with bags under eyes", 0x1FAE9},
	{"16.0", "harp", 0x1FA89},
	{"16.0", "shovel", 0x1FA8F},
	{"16.0", "splatter", 0x1FADF},
	{"16.0", "fingerprint", 0x1FAC6},
}

// classicEmoji are the old favourites. They are here so a build that somehow
// gained the new codepoints while LOSING the old ones cannot pass.
var classicEmoji = []emojiEra{
	{"1.0", "grinning face", 0x1F600},
	{"1.0", "rocket", 0x1F680},
	{"1.0", "regional indicator A", 0x1F1E6},
	{"2.0", "skin tone modifier", 0x1F3FB},
	{"5.0", "shrug", 0x1F937},
	{"11.0", "pleading face", 0x1F97A},
	{"13.0", "smiling face with tear", 0x1F972},
	{"14.0", "melting face", 0x1FAE0},
	{"mahjong", "red dragon", 0x1F004},
}

// TestBundledEmojiFontCoversModernEmoji is the regression for the actual
// complaint: emoji added since 2021 drew as boxes because the bundled font was a
// 2022 build of a 2021 emoji set.
func TestBundledEmojiFontCoversModernEmoji(t *testing.T) {
	f := parseCover(twemojiTTF)
	if f == nil {
		t.Fatal("cannot parse the bundled emoji font")
	}
	var buf sfnt.Buffer
	for _, e := range append(append([]emojiEra{}, modernEmoji...), classicEmoji...) {
		idx, err := f.GlyphIndex(&buf, e.r)
		if err != nil || idx == 0 {
			t.Errorf("Emoji %s %s (U+%04X) is missing from the bundled font — it will draw as a box", e.ver, e.name, e.r)
		}
	}
}

// TestModernEmojiRouteToTheEmojiFace pins that the codepoints above actually take
// the emoji path. isEmojiBase is bounded to U+1F000..U+1FAFF, and several Emoji
// 15/16 additions sit in the U+1FA70.. block near that ceiling — a range that was
// a hair too small would send them to a text face that has no glyph for them.
func TestModernEmojiRouteToTheEmojiFace(t *testing.T) {
	for _, e := range append(append([]emojiEra{}, modernEmoji...), classicEmoji...) {
		if !render.AssignEmoji([]rune{e.r})[0] {
			t.Errorf("Emoji %s %s (U+%04X) does not route to the colour-emoji face", e.ver, e.name, e.r)
		}
	}
}

// TestBundledEmojiFontKeepsSequenceLigatures is the check nothing else can make.
//
// Emoji 15.1 added ONE HUNDRED AND EIGHTEEN emoji and ZERO new codepoints: every
// one of them is a ZWJ sequence (phoenix is U+1F426 ZWJ U+1F525, not a codepoint
// of its own). Those live in GSUB as ligatures, not in cmap — so a cmap probe
// cannot see them at all, and a build that dropped the ligature table would pass
// every other test here while rendering 🐦\u200d🔥 as a bird, a joiner and a flame.
// The same table is what composes flags, keycaps, skin tones and ZWJ families.
// It measures the real thing rather than reading a table: a sequence that
// ligated occupies ONE glyph's advance, so it is no wider than its first
// component. A sequence that fell apart is a multiple of that.
func TestBundledEmojiFontKeepsSequenceLigatures(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	_ = ren
	const size = 32
	face, err := memFont(twemojiTTF, size)
	if err != nil {
		t.Skipf("cannot open the bundled emoji font at %d px: %v", size, err)
	}
	defer face.Close()

	width := func(s string) int {
		w, _, err := face.SizeUTF8(s)
		if err != nil {
			t.Fatalf("measuring %q: %v", s, err)
		}
		return w
	}
	one := width("\U0001F600") // a single emoji: the width of exactly one glyph
	if one <= 0 {
		t.Fatalf("a single emoji measured %d px wide", one)
	}

	// Every joiner and selector is written as an escape: a literal U+200D or
	// U+FE0F is invisible in a source file, so the difference between a passing
	// and a failing fixture would be unreadable in a diff.
	for _, tc := range []struct{ name, seq string }{
		{"ZWJ family", "\U0001F468\u200d\U0001F469\u200d\U0001F467"},
		{"phoenix (Emoji 15.1, a ZWJ sequence with NO codepoint of its own)", "\U0001F426\u200d\U0001F525"},
		{"flag (regional indicator pair)", "\U0001F1EF\U0001F1F5"},
		{"skin tone", "\U0001F44B\U0001F3FD"},
		{"keycap", "1\ufe0f\u20e3"},
	} {
		if got := width(tc.seq); got > one {
			t.Errorf("%s measured %d px against %d px for one glyph — the sequence did not ligate, "+
				"so it draws as its separate parts", tc.name, got, one)
		}
	}
}
