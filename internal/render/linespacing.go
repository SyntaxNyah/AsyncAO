package render

import "github.com/veandco/go-sdl2/ttf"

// The ONE line-advance metric every message raster stacks wrapped rows by.
//
// WHY IT IS NOT font.Height(). AO2's chatbox message is a QTextEdit
// (../AO2-Client/src/courtroom.h:626, constructed at src/courtroom.cpp:97), so its
// wrapped rows are laid out by Qt's text engine, whose baseline-to-baseline step is
// the font's LINE SPACING. Qt states the identity outright: QFontMetrics::lineSpacing()
// "Returns the distance from one base line to the next. This value is always equal to
// leading()+height()", and QFontMetrics::height() "is always equal to ascent()+descent()"
// (Qt 6 docs, qfontmetrics.html — lineSpacing() / height() / leading()). The leading is
// the font designer's own inter-line gap and it is NOT part of height().
//
// SDL_ttf spells the same pair TTF_FontHeight / TTF_FontLineSkip, exposed by go-sdl2 as
// Font.Height() ("the maximum pixel height of all glyphs") and Font.LineSkip() ("the
// recommended pixel height of a rendered line of text", ttf/sdl_ttf.go:274-276). Every
// raster in this package used to stack rows by Height(), i.e. by the glyph box with the
// leading thrown away — which is exactly the reported symptom: two wrapped IC lines
// almost touching, ascenders of row 2 running into descenders of row 1.
//
// It is a function, not a field, so the THEME font path gets it for free: the metric is
// read off whichever *ttf.Font the caller opened, including a theme's bound TTF.
//
// HOW MUCH THIS MOVES, MEASURED. For every face reachable from this repository the answer
// is ZERO PIXELS, because none of them authors an hhea lineGap. Through SDL_ttf at 18 pt:
// Go Regular 21/21, Go Bold 21/21, Go Mono 21/21, internal/ui/fonts/OpenDyslexic-Regular
// 33/33 — Height == LineSkip in every case, so max(Height, LineSkip) returns what
// Height() returned before. Of the system faces a theme would plausibly bind, Arial,
// Segoe UI, Verdana, Tahoma, Comic Sans and Trebuchet are also 0; Times is +1, Calibri
// and Consolas +4, and the bundled Twemoji +2.
//
// So this is the correct METRIC, not a fix for the reported "wrapped IC rows sit too
// close" case on its own: that report was measured at a 24 px baseline pitch carrying
// ~23 px of ink, and unless the theme's own TTF carries a non-zero lineGap this function
// returns exactly the same 24. What it does buy is that a face which DOES author one is
// no longer ignored, and that there is one place to change if the chatbox ever needs
// extra leading beyond what the designer specified (that would be a new, deliberate
// addition here — a row pitch of pitch+N — not a side effect of picking a metric).
//
// Device-exactness (#77 crisp text) is untouched: the value is a DEVICE-pixel metric of
// the device-scaled face the caller opened, exactly like the Height() it replaces, and
// MessageRaster.linePitch keeps folding it through the same logical pitch. Nothing here
// changes the renderer scale or introduces a fractional step.
//
// Callers MUST route through this function — a raw font.Height() row pitch is what this
// exists to prevent, and TestEveryRasterStacksRowsByLineSpacing (linespacing_test.go)
// fails on a new one. EXPORTED because internal/ui derives geometry that has to agree
// with the raster it wraps around (the flat chatbox's grow-to-fit maths, the animated
// message's block highlight, the no-raster fallback label): a second opinion about the
// row pitch there re-opens the same gap one layer up.
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
