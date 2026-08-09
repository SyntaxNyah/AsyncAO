package render

// Raising a built message's row advance to the pitch Qt would have used.
//
// SEPARATE FROM linespacing.go ON PURPOSE. That file answers "what does THIS
// FACE say its line advance is", reading SDL_ttf's own metrics off an open
// *ttf.Font. This one answers a different question — "what does the SECOND
// opinion, the one AO2's Qt actually renders by, say" — and it cannot live on the
// same seam because the number does not come from SDL_ttf at all. SDL_ttf exposes
// no OS/2 data, so the value has to be computed from the face FILE
// (internal/theme.QtRowPitch) and handed in.
//
// WHY IT IS A max() AND NOT A REPLACEMENT. FontLineSpacing already documents its
// floor: "LineSkip is a recommendation, Height is a measurement, and the
// measurement is the hard floor." Qt's number is a third recommendation, and
// taking the larger of the two means this can only ever ADD leading. That is
// deliberate, and it is what makes the change safe to ship:
//
//   - The theme faces that are actually broken get fixed. DangitV3IC (the DRRA /
//     KFO family's `message_font`) at 24 px: SDL_ttf 24, Qt 35 → 35, which is the
//     pitch measured off the AO2 side of the side-by-side capture.
//   - Every face where Qt is the SMALLER number keeps exactly the pitch it has
//     today. Arial at 40 px: SDL_ttf 46, Qt 44 → 46, unchanged. So no shipped
//     look gets TIGHTER, and the client's own faces (which have no OS/2 quarrel
//     with their hhea at all) do not move by a pixel.
//
// Callers pass 0 — the zero QtRowPitch's answer — when the face has no Qt
// opinion, and both functions below are then a no-op by construction.

// UseRowPitch raises this raster's row advance to px, in the same DEVICE pixels
// lineH is kept in.
//
// SAFE TO CALL AT ANY POINT BETWEEN CONSTRUCTION AND THE FIRST Draw/Height,
// which is why it is a post-construction setter rather than another parameter on
// four constructors: MessageRaster's lineH is a DRAW-time quantity only. The
// wrap, the per-line textures, the advance tables and the rune ranges are all
// horizontal, and none of them reads it — linePitch (the draw step), Height() and
// LineH() (the selection/auto-grow geometry) are its only consumers, and all
// three derive from the field every time they run. There is no baked layout to
// invalidate.
func (m *MessageRaster) UseRowPitch(px int32) {
	if m == nil || px <= m.lineH {
		return
	}
	m.lineH = px
}

// UseRowPitch raises the animated message's row advance to px and RE-STRIDES the
// rows that were already laid out onto it.
//
// The re-stride is what the plain raster does not need. RasterizeAnimated bakes a
// per-glyph y at layout time (`baseY := int32(li) * at.lineH`), so changing the
// field alone would leave every row past the first sitting on the old step. The
// row index is recoverable exactly — y is an exact multiple of the old pitch by
// construction — so this divides it back out and multiplies by the new one.
// Nothing horizontal is touched: the pen positions, the kerned run offsets and
// the per-rune faces are all independent of the row advance.
func (at *AnimatedText) UseRowPitch(px int32) {
	if at == nil || at.lineH <= 0 || px <= at.lineH {
		return
	}
	old := at.lineH
	for i := range at.runes {
		at.runes[i].y = at.runes[i].y / old * px
	}
	at.lineH = px
}

// RowPitch is the row advance in effect — DEVICE px for the message raster, the
// same units UseRowPitch takes. Exported so a test can assert the override landed
// without reaching into the struct, and so the UI's own geometry (the chatbox
// auto-grow, the selection highlight) can read the ONE number the draw will
// actually step by instead of recomputing a second opinion from the face.
func (m *MessageRaster) RowPitch() int32 {
	if m == nil {
		return 0
	}
	return m.lineH
}

// RowPitch is the animated message's row advance, in the LOGICAL pixels that path
// lays out and draws in (see renderAnimated's note on why the animated path stays
// on the logical face).
func (at *AnimatedText) RowPitch() int32 {
	if at == nil {
		return 0
	}
	return at.lineH
}
