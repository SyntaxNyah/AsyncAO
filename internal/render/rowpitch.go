package render

// Raising a built message's row advance to the pitch Qt would use; 0 means the face has none, a no-op.

// UseRowPitch raises this raster's row advance to px, in device pixels.
func (m *MessageRaster) UseRowPitch(px int32) {
	if m == nil || px <= m.lineH {
		return
	}
	m.lineH = px
}

// UseRowPitch raises the row advance to px and moves rows already laid out onto it.
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

// RowPitch is the row advance in device px, so UI geometry reads the number the draw steps by.
func (m *MessageRaster) RowPitch() int32 {
	if m == nil {
		return 0
	}
	return m.lineH
}

// RowPitch is the row advance in logical px, which this path lays out and draws in.
func (at *AnimatedText) RowPitch() int32 {
	if at == nil {
		return 0
	}
	return at.lineH
}
