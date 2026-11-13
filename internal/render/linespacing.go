package render

import "github.com/SyntaxNyah/AsyncAO/internal/ttf"

// FontLineSpacing is every message raster's row advance: AO2's Qt message box steps by QFontMetrics::lineSpacing
// (../AO2-Client/src/courtroom.h:626, src/courtroom.cpp:97); callers must use it, as Height() drops the leading.
func FontLineSpacing(f *ttf.Font) int32 {
	if f == nil {
		return 0
	}
	h := int32(f.Height())
	// Floor at the glyph box: a short or zero hhea lineGap would clip the font's own glyphs.
	if s := int32(f.LineSkip()); s > h {
		return s
	}
	return h
}
