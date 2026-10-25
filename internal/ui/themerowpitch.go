package ui

// Binding a theme face's Qt row pitch to the size it is actually drawn at.
// It is its own file because it is its own concern. themefonts.go answers "which
// family, which size, which colour does this element get"; this answers "and how
// far apart does AO2 put its rows", which is a question about the FILE's vertical

import (
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// faceEmPx is the pixel em a fontSet opens its faces at for (pct, devPct).
// It MIRRORS buildSet (ui.go) deliberately and must keep mirroring it: the Qt
// pitch is only the right number if it is computed against the very size the
// glyphs were rasterised at, and a face opened one pixel smaller than this
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

// themeFaceRowPitch is the Qt baseline-to-baseline step for interned face slot
// `face` at emPx pixels, or 0 when the slot names no theme face (face < 0, the
// undressed element) or the face carried no usable OS/2 metrics.
// A bounds-checked slice read and integer arithmetic — no lock, no map, no file.
func (c *Ctx) themeFaceRowPitch(face int, emPx int32) int32 {
	if face < 0 || face >= len(c.themeFaceQt) {
		return 0
	}
	return c.themeFaceQt[face].Pitch(emPx)
}

// themeRowPitchDevice is element el's Qt row pitch in DEVICE pixels — the units
// MessageRaster keeps its lineH in, because the raster rasterises its line
// textures with the device-scaled sibling face (#77).
func (a *App) themeRowPitchDevice(el themeFontElem, pct int) int32 {
	return a.ctx.themeFaceRowPitch(a.skin.themeFonts.e[el].faceIdx(), faceEmPx(pct, int(a.ctx.textDevPct)))
}

// themeRowPitchLogical is the same number in LOGICAL pixels, for the animated
// per-glyph path — which stays on the logical face by design (renderAnimated's
// #77 Part A note) and would be spaced for the wrong size by the device figure.
func (a *App) themeRowPitchLogical(el themeFontElem, pct int) int32 {
	return a.ctx.themeFaceRowPitch(a.skin.themeFonts.e[el].faceIdx(), faceEmPx(pct, DefaultScalePct))
}

// messageRowPitchLogical is the row advance a chatbox MESSAGE actually gets in
// logical pixels: the face's own SDL_ttf line spacing, raised to AO2's Qt pitch.
func (a *App) messageRowPitchLogical(pct int, f *ttf.Font) int32 {
	h := render.FontLineSpacing(f)
	if q := a.themeRowPitchLogical(elemMessage, pct); q > h {
		return q
	}
	return h
}
