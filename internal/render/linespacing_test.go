package render

import (
	"encoding/binary"
	"testing"

	"golang.org/x/image/font/gofont/goregular"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// The chatbox row pitch. Two wrapped IC lines were drawn almost touching because
// every raster here stacked rows by TTF_FontHeight (ascent+descent, the glyph box)
// while AO2's QTextEdit chatbox steps by the font's LINE SPACING — Qt's
// QFontMetrics::lineSpacing() == leading() + height() (qfontmetrics.html), i.e. the
// glyph box PLUS the designer's inter-line gap. FontLineSpacing (linespacing.go) is
// the one place that decision is made now.

// gapUnitsPerEmSample is the hhea lineGap, in the font's own design units, that
// leadingFace authors into its copy of Go Regular. Go Regular's unitsPerEm is 2048, so
// this is ~24% of an em and lands as +5 device px at the 18 pt used here — far enough
// above the ±1 px of hinting rounding that a test can assert on it, and close enough to
// a real design (Calibri's is ~22%) to be an honest stand-in.
const gapUnitsPerEmSample = 500

// leadingFace opens a face that HAS a designer's inter-line gap, by rewriting the hhea
// table's lineGap field in a copy of Go Regular. That is the exact quantity the defect
// was about: FreeType derives FT_Face.height (what TTF_FontLineSkip reports) as
// ascender - descender + lineGap, while TTF_FontHeight is ascender - descender alone, so
// authoring a lineGap is the minimal, honest way to produce LineSkip > Height.
// It is built here rather than borrowed from internal/ui/fonts (Twemoji does have +2 px)
func leadingFace(t testing.TB, size int) *ttf.Font {
	t.Helper()
	data := append([]byte(nil), goregular.TTF...)
	// sfnt offset table: numTables at +4, then 16-byte records of {tag, checkSum,
	// offset, length} from +12. hhea's lineGap is the int16 at hhea+8 (after version,
	// ascender, descender).
	const (
		numTablesOff  = 4
		tableRecsOff  = 12
		tableRecSize  = 16
		tableOffField = 8
		hheaLineGap   = 8
	)
	n := int(binary.BigEndian.Uint16(data[numTablesOff : numTablesOff+2]))
	patched := false
	for i := 0; i < n; i++ {
		rec := tableRecsOff + i*tableRecSize
		if string(data[rec:rec+4]) != "hhea" {
			continue
		}
		off := int(binary.BigEndian.Uint32(data[rec+tableOffField : rec+tableOffField+4]))
		binary.BigEndian.PutUint16(data[off+hheaLineGap:off+hheaLineGap+2], uint16(int16(gapUnitsPerEmSample)))
		patched = true
		break
	}
	if !patched {
		t.Fatal("fixture: Go Regular has no hhea table — the leading fixture cannot be built")
	}
	f := openFontBytes(t, data, size)
	if int32(f.LineSkip()) <= int32(f.Height()) {
		f.Close()
		t.Fatalf("fixture: patched face still reports LineSkip=%d <= Height=%d; it cannot tell a "+
			"line-spacing pitch from a glyph-box pitch", f.LineSkip(), f.Height())
	}
	return f
}

// TestFontLineSpacingIsTheBaselineToBaselineStep pins the metric itself against the two
// SDL_ttf primitives, on BOTH a zero-leading face (where the "a short/zero LineSkip may
// never clip the glyphs" floor is the live rule) and a leading-bearing one (where the
// leading must actually be added).
func TestFontLineSpacingIsTheBaselineToBaselineStep(t *testing.T) {
	plainFace, cleanup := newAnimTestFont(t, 18)
	defer cleanup()
	leading := leadingFace(t, 18) // needs newAnimTestFont's ttf.Init, so open it second
	defer leading.Close()

	for _, tc := range []struct {
		name string
		f    *ttf.Font
	}{{"zero-leading (Go Regular)", plainFace}, {"leading-bearing", leading}} {
		got := FontLineSpacing(tc.f)
		h, skip := int32(tc.f.Height()), int32(tc.f.LineSkip())
		want := h
		if skip > h {
			want = skip
		}
		if got != want {
			t.Errorf("%s: FontLineSpacing = %d, want max(Height=%d, LineSkip=%d) = %d", tc.name, got, h, skip, want)
		}
		if got < h {
			t.Errorf("%s: FontLineSpacing (%d) below the glyph box (%d) would clip its own rows", tc.name, got, h)
		}
		t.Logf("%s 18pt: Height=%d LineSkip=%d → pitch %d (%+d px of leading per row)", tc.name, h, skip, got, got-h)
	}
	// The whole point of the leading face: the two primitives must DISAGREE on it, and
	// the mint must return the larger. Without this the test above is satisfied by
	// `return int32(f.Height())`.
	if got, box := FontLineSpacing(leading), int32(leading.Height()); got <= box {
		t.Errorf("leading-bearing face: pitch %d == glyph box %d — FontLineSpacing dropped the "+
			"designer's leading, which is the whole defect (Qt: lineSpacing == leading + height)", got, box)
	}
	if FontLineSpacing(nil) != 0 {
		t.Error("a nil face must measure 0, not panic")
	}
}

// TestWrappedMessageRowsClearTheFontsLineSpacing is the DEFECT test: it drives the real
// raster (plain, styled and animated) on a message forced to wrap, with a face whose
// LineSkip and Height DIFFER, and asserts the row advance is the line spacing — never the
// bare glyph box. The leading face is what makes it a test: on Go Regular the two are
// equal, so reverting text.go to font.Height() keeps every number here green and only the
// AST census below reddens.
func TestWrappedMessageRowsClearTheFontsLineSpacing(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	base, fcleanup := newAnimTestFont(t, 18) // owns ttf.Init/Quit for this test
	defer fcleanup()
	// State the reason this test does not simply use `base`: on the default face the two
	// metrics are equal, so every assertion below would hold for a raster that threw the
	// leading away. If Go Regular ever ships a lineGap this stops being true and the
	// fixture can be simplified — say so rather than leaving the choice unexplained.
	if int32(base.LineSkip()) != int32(base.Height()) {
		t.Logf("note: the default test face now HAS a leading (Height=%d LineSkip=%d); leadingFace "+
			"could be retired", base.Height(), base.LineSkip())
	}
	font := leadingFace(t, 18)
	defer font.Close()

	// Narrow enough that this text cannot fit on one line at 18pt. The string itself is
	// arbitrary filler — all this test needs of it is that it wraps to two or more rows,
	// which the fixture check below enforces rather than assumes.
	const wrapW = 120
	const text = "the quick brown fox jumps over the lazy dog and naps in the warm afternoon sun"
	pitch := FontLineSpacing(font)
	box := int32(font.Height())
	if pitch <= box {
		t.Fatalf("fixture: pitch %d must exceed the glyph box %d or this test cannot fail", pitch, box)
	}

	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	plain, err := Rasterize(ren, font, text, wrapW, white, DefaultDevScale)
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	defer plain.Destroy()
	styled, err := RasterizeStyled(ren, font, text, []ColorSpan{{Len: len([]rune(text)), Color: white}}, wrapW, DefaultDevScale)
	if err != nil {
		t.Fatalf("RasterizeStyled: %v", err)
	}
	defer styled.Destroy()

	for _, tc := range []struct {
		name string
		m    *MessageRaster
	}{{"plain", plain}, {"styled", styled}} {
		if n := tc.m.Lines(); n < 2 {
			t.Fatalf("%s: fixture did not wrap (%d line(s)) — the two-line case is the whole point", tc.name, n)
		}
		if got := tc.m.LineH(); got != pitch {
			t.Errorf("%s: row advance = %d, want the font's line spacing %d (AO2 QTextEdit steps by "+
				"QFontMetrics::lineSpacing); the glyph box is %d, and stacking by it is what drew two "+
				"IC lines almost touching", tc.name, got, pitch, box)
		}
		if got, want := tc.m.Height(), int32(tc.m.Lines())*tc.m.LineH(); got != want {
			t.Errorf("%s: block height = %d, want rows×pitch = %d (a box sized from this must fit what Draw stacks)", tc.name, got, want)
		}
	}

	// The animated-text raster is the third row-stacking surface (FX chat text) and
	// must agree, or a message gains/loses a leading the moment an effect tag is used.
	at := RasterizeAnimated(font, text, []EffectSpan{{Start: 0, Len: 4, Effect: EffectShake}}, []sdl.Color{white}, wrapW, nil, 0)
	if at.lineH != pitch {
		t.Errorf("AnimatedText row advance = %d, want %d — FX text must not re-space the chatbox", at.lineH, pitch)
	}
}
