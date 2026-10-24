package render

import "github.com/veandco/go-sdl2/ttf"

// The ONE line-advance metric every message raster stacks wrapped rows by.
// (../AO2-Client/src/courtroom.h:626, constructed at src/courtroom.cpp:97), so its
// wrapped rows are laid out by Qt's text engine, whose baseline-to-baseline step is
// the font's LINE SPACING. Qt states the identity outright: QFontMetrics::lineSpacing()
// recommended pixel height of a rendered line of text", ttf/sdl_ttf.go:274-276). Every
// raster in this package used to stack rows by Height(), i.e. by the glyph box with the
// leading thrown away — which is exactly the reported symptom: two wrapped IC lines
// almost touching, ascenders of row 2 running into descenders of row 1.
// Device-exactness (#77 crisp text) is untouched: the value is a DEVICE-pixel metric of
// the device-scaled face the caller opened, exactly like the Height() it replaces, and
// MessageRaster.linePitch keeps folding it through the same logical pitch. Nothing here
// changes the renderer scale or introduces a fractional step.
// Callers MUST route through this function — a raw font.Height() row pitch is what this
// exists to prevent, and TestEveryRasterStacksRowsByLineSpacing (linespacing_test.go)
func FontLineSpacing(f *ttf.Font) int32 {
	if f == nil {
		return 0
	}
	h := int32(f.Height())
	// Floor at the glyph box. A font whose hhea lineGap is authored short (or a face
	// that reports 0) would otherwise CLIP its own glyphs — LineSkip is a
	// recommendation, Height is a measurement, and the measurement is the hard floor.
	if s := int32(f.LineSkip()); s > h {
		return s
	}
	return h
}
