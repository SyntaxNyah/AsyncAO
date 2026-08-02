package ui

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// censusSamples is one representative codepoint per script that the CURATED
// fallback chain does not cover. Every one of them rendered as a tofu box before
// the census existed; the point of the census is that the machine already had a
// font for each, and nobody had hardcoded its path.
var censusSamples = []struct {
	script string
	r      rune
}{
	{"Thai", 0x0E01}, {"Lao", 0x0E81}, {"Khmer", 0x1780}, {"Myanmar", 0x1000},
	{"Cherokee", 0x13A0}, {"Syriac", 0x0710}, {"Thaana", 0x0780},
	{"Mongolian", 0x1820}, {"Tibetan", 0x0F40}, {"Canadian Syllabics", 0x1401},
	{"Runic", 0x16A0}, {"Ogham", 0x1681}, {"Glagolitic", 0x2C00},
	{"Old Italic", 0x10300}, {"Gothic", 0x10330}, {"Cuneiform", 0x12000},
	{"Egyptian hieroglyph", 0x13000}, {"Linear B", 0x10000},
	{"CJK Ext-B", 0x20000}, {"Yi", 0xA000}, {"Javanese", 0xA980},
	{"Adlam", 0x1E900}, {"Osage", 0x104B0}, {"Vai", 0xA500},
	{"Coptic", 0x2C81}, {"Deseret", 0x10400}, {"Math alphanumeric", 0x1D400},
}

// TestFontCensusCoversTheCuratedGaps is the empirical claim the whole "no more
// tofu" fix rests on: a stock Windows install ALREADY ships a font for every
// script the curated chain misses, so the fix is discovery, not shipping fonts.
//
// Measured on a stock Windows 11 box: 148 files indexed, all 27 scripts resolved,
// 0.2 s for the whole walk. It is skipped off Windows, where the font inventory
// is the distro's business and cannot be asserted — the census still runs there,
// which is the entire reason macOS and Linux stop being permanently Latin-only.
func TestFontCensusCoversTheCuratedGaps(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("font inventory is not knowable off Windows; the census still runs there")
	}
	fc := &fontCensus{}
	defer fc.close()
	fc.index()
	if len(fc.files) == 0 {
		t.Skip("no system fonts indexed (locked-down or containerised host)")
	}

	var missing []string
	for _, s := range censusSamples {
		if fc.find(s.r) == "" {
			missing = append(missing, s.script)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the census found no installed font for %d script(s): %s\n"+
			"(each of these renders as a tofu box; if a font really is absent on this host that is a skip, "+
			"but if the census stopped finding fonts it has regressed)",
			len(missing), strings.Join(missing, ", "))
	}
}

// TestFontCensusMemoizesBothWays pins that a repeated lookup — including one that
// found NOTHING — never re-walks the indexed faces. Without the negative memo, a
// line of unresolvable text would re-probe every installed font on every frame it
// was asked about.
func TestFontCensusMemoizesBothWays(t *testing.T) {
	fc := &fontCensus{}
	defer fc.close()
	fc.index()

	// U+0F0000 is in a private-use plane: no real font claims it, so this exercises
	// the negative path.
	const unassigned rune = 0xF0000
	first := fc.find(unassigned)
	if _, ok := fc.answered[unassigned]; !ok {
		t.Error("a miss must be memoized, or unresolvable text re-walks every font forever")
	}
	if got := fc.find(unassigned); got != first {
		t.Errorf("repeat lookup returned %q, want the memoized %q", got, first)
	}
}

// TestFontCensusPrefersRegularCuts pins the ranking. Bold and italic cuts are
// usually SMALLER files than their regular sibling, so ranking on size alone
// picked "Leelawadee UI Bold" for Thai — right glyphs, wrong weight, sitting next
// to text drawn in a regular face.
func TestFontCensusPrefersRegularCuts(t *testing.T) {
	styled := []string{
		`C:\Windows\Fonts\segoeuib.ttf`, `C:\Windows\Fonts\segoeuii.ttf`,
		`C:\Windows\Fonts\segoeuiz.ttf`, `C:\Windows\Fonts\LeelaUIb.ttf`,
		`C:\Windows\Fonts\LeelUIsl.ttf`, `C:\Windows\Fonts\mmrtextb.ttf`,
		`/usr/share/fonts/NotoSans-Bold.ttf`, `/usr/share/fonts/NotoSans-Italic.ttf`,
		`/usr/share/fonts/NotoSans-Light.ttf`,
	}
	for _, p := range styled {
		if !isStyledCut(p) {
			t.Errorf("isStyledCut(%q) = false, want true (a styled cut must not outrank its regular sibling)", p)
		}
	}
	regular := []string{
		`C:\Windows\Fonts\segoeui.ttf`, `C:\Windows\Fonts\LeelaUI.ttf`,
		`C:\Windows\Fonts\mmrtext.ttf`, `C:\Windows\Fonts\gadugi.ttf`,
		`C:\Windows\Fonts\seguihis.ttf`, `C:\Windows\Fonts\javatext.ttf`,
		`/usr/share/fonts/DejaVuSans.ttf`, `/usr/share/fonts/NotoSans-Regular.ttf`,
	}
	for _, p := range regular {
		if isStyledCut(p) {
			t.Errorf("isStyledCut(%q) = true, want false (a regular face was demoted below its bold cut)", p)
		}
	}
}

// TestInvisibleRunesNeverQueueACensusLookup pins that formatting characters are
// not treated as missing fonts. They are supposed to render as nothing, they are
// rarely in any cmap, and chasing them would spend the bounded census queue (and
// the bounded face slots) on characters that are invisible when they WORK.
func TestInvisibleRunesNeverQueueACensusLookup(t *testing.T) {
	invisible := []rune{
		0x00AD, 0x200B, 0x200C, 0x200D, 0x200E, 0x200F,
		0x202A, 0x202E, 0x2060, 0x2066, 0x2069, 0xFEFF,
		0xFE0E, 0xFE0F, 0xE0020, 0xE007F, 0xE0100,
	}
	for _, r := range invisible {
		if !isInvisibleRune(r) {
			t.Errorf("U+%04X should be treated as invisible formatting, not a missing glyph", r)
		}
	}
	visible := []rune{'A', 0x0410, 0x0E01, 0x4E00, 0x1F600, 0x20000, 0xFFFD}
	for _, r := range visible {
		if isInvisibleRune(r) {
			t.Errorf("U+%04X is real text and must still be able to ask the census for a font", r)
		}
	}
}

// TestMissingRuneQueueIsBoundedAndDedupes pins the render-thread side: a fixed
// array that never allocates, dedupes within a frame, and simply stops accepting
// once full — a paragraph of unknown runes needs one answer, not four hundred.
func TestMissingRuneQueueIsBoundedAndDedupes(t *testing.T) {
	c := &Ctx{}
	for i := 0; i < censusQueueCap*4; i++ {
		c.noteUncovered(rune(0x10000 + i))
	}
	if c.missingN != censusQueueCap {
		t.Errorf("queued %d runes, want the cap %d", c.missingN, censusQueueCap)
	}
	got := c.TakeMissingRunes(nil)
	if len(got) != censusQueueCap {
		t.Errorf("drained %d runes, want %d", len(got), censusQueueCap)
	}
	if c.missingN != 0 {
		t.Errorf("the queue must be empty after a drain, got %d", c.missingN)
	}

	c.noteUncovered('ก')
	c.noteUncovered('ก')
	c.noteUncovered('ก')
	if c.missingN != 1 {
		t.Errorf("the same rune queued %d times in one frame, want 1", c.missingN)
	}

	// The drain reuses the caller's buffer, so it must not allocate.
	buf := make([]rune, 0, censusQueueCap)
	if n := testing.AllocsPerRun(100, func() {
		c.noteUncovered('ก')
		buf = c.TakeMissingRunes(buf)
	}); n != 0 {
		t.Errorf("the missing-rune drain allocates %v per frame, want 0", n)
	}
}

// TestScaledChatboxStillAsksTheCensus pins the census hand-off on the DEVICE
// resolver, not just the 1:1 one.
//
// deviceCoverRunes delegates upward only at exactly 100%; every other UI scale
// runs its own loop. Auto-scale ships ON and a maximised 1080p window lands
// around 130%, so the un-delegated branch is the COMMON case — leaving the
// hand-off out of it meant the IC chatbox, the surface the whole fix was written
// for, never once asked the census for a font it was missing.
func TestScaledChatboxStillAsksTheCensus(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	defer c.Destroy()

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

	const hieroglyph = '\U00013000' // in no curated face — the census's whole reason to exist
	for _, pct := range []int{100, 125, 150, 200} {
		c.SetUIScale(pct)
		primary := c.ChatFontFor(DefaultScalePct, "warm the set")
		if primary == nil {
			t.Skip("no chat face")
		}
		c.missingN = 0
		c.deviceCoverRunes(primary, DefaultScalePct, []rune{hieroglyph})
		if c.missingN == 0 {
			t.Errorf("at %d%% UI scale an uncoverable chatbox rune queued nothing — the census is never asked at this scale", pct)
		}
	}
}

// TestWrapSplitsOnRuneBoundaries is the mojibake regression. Japanese, Chinese and
// Thai put no spaces between words, so strings.Fields hands a whole sentence over
// as ONE word and the hard-split path always runs on them. Splitting on byte
// indices landed off a rune boundary most of the time, and the invalid UTF-8 that
// produced was then measured and drawn.
func TestWrapSplitsOnRuneBoundaries(t *testing.T) {
	// A width function with no SDL behind it: every rune is 10 units wide.
	measure := func(s string) int32 { return int32(utf8.RuneCountInString(s)) * 10 }
	for _, text := range []string{
		"これは日本語のとてもながいぶんしょうです",                         // Japanese, no spaces
		"这是一个很长的中文句子没有空格分隔",                            // Chinese, no spaces
		"นี่คือประโยคภาษาไทยที่ยาวมากและไม่มีช่องว่าง", // Thai, no spaces
		"АаБбВвГгДдЕеЁёЖжЗзИиЙйКкЛлМмНнОоПпРр",         // Cyrillic
		"🙂🙂🙂🙂🙂🙂🙂🙂🙂🙂🙂🙂",                                 // astral runes
	} {
		lines := wrapToWidthMeasured(measure, text, 45, 40)
		if len(lines) == 0 {
			t.Errorf("%q wrapped to nothing", text)
			continue
		}
		var joined strings.Builder
		for i, l := range lines {
			if !utf8.ValidString(l) {
				t.Errorf("wrap produced INVALID UTF-8 on line %d of %q: %q", i, text, l)
			}
			if strings.ContainsRune(l, utf8.RuneError) {
				t.Errorf("wrap produced a replacement char on line %d of %q: %q", i, text, l)
			}
			joined.WriteString(l)
		}
		// Nothing may be lost or duplicated by the split.
		if got := joined.String(); got != text {
			t.Errorf("wrap of %q rejoined to %q — the split lost or duplicated text", text, got)
		}
	}
}
