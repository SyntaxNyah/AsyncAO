package ui

// Binds a theme face's Qt row pitch (read by internal/theme, applied by internal/render) to the pixel em it is drawn
// at.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// faceEmPx is the pixel em a fontSet opens its faces at for (pct, devPct).
// It must mirror buildSet (ui.go), floor included: the Qt pitch is right only at the size the glyphs were rasterised.
func faceEmPx(pct, devPct int) int32 {
	if devPct <= 0 {
		devPct = DefaultScalePct
	}
	size := UIFontSize * pct * devPct / (DefaultScalePct * DefaultScalePct)
	if size < 1 {
		size = 1
	}
	return int32(size)
}

// themeFaceRowPitch is the Qt baseline-to-baseline step for face index `face` at emPx pixels,
// or 0 when it names no theme face (face < 0) or carried no usable OS/2 metrics.
func (c *Ctx) themeFaceRowPitch(face int, emPx int32) int32 {
	if face < 0 || face >= len(c.themeFaceQt) {
		return 0
	}
	return c.themeFaceQt[face].Pitch(emPx)
}

// themeRowPitchDevice is el's Qt row pitch in device pixels, because the raster uses the device-scaled face.
// When a line routes to a fallback face the pitch stays the theme's, as Qt uses the widget's metrics;
// UseRowPitch only raises the advance, so the worst case is slightly more leading.
func (a *App) themeRowPitchDevice(el themeFontElem, pct int) int32 {
	return a.ctx.themeFaceRowPitch(a.themeFonts.e[el].faceIdx(), faceEmPx(pct, int(a.ctx.textDevPct)))
}

// themeRowPitchLogical is the same in logical pixels, for the animated per-glyph path, which stays on the logical face.
func (a *App) themeRowPitchLogical(el themeFontElem, pct int) int32 {
	return a.ctx.themeFaceRowPitch(a.themeFonts.e[el].faceIdx(), faceEmPx(pct, DefaultScalePct))
}

// messageRowPitchLogical is a chatbox message's row advance in logical pixels: SDL_ttf line spacing raised to AO2's Qt
// pitch. It matches render.UseRowPitch's max(), so the geometry around the raster cannot under-grow the panel.
func (a *App) messageRowPitchLogical(pct int, f *ttf.Font) int32 {
	h := render.FontLineSpacing(f)
	if q := a.themeRowPitchLogical(elemMessage, pct); q > h {
		return q
	}
	return h
}
