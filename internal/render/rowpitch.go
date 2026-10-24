package render

// Raising a built message's row advance to the pitch Qt would have used.
// Callers pass 0 — the zero QtRowPitch's answer — when the face has no Qt
// opinion, and both functions below are then a no-op by construction.

// UseRowPitch raises this raster's row advance to px, in the same DEVICE pixels
// lineH is kept in.
func (m *MessageRaster) UseRowPitch(px int32) {
	if m == nil || px <= m.lineH {
		return
	}
	m.lineH = px
}

// UseRowPitch raises the animated message's row advance to px and RE-STRIDES the
// rows that were already laid out onto it.
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
