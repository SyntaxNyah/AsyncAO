package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// defaultKitColors snapshots the stock palette above so a theme switch (or
// picking "none") restores it exactly — themes override, never mutate-forever.
var defaultKitColors = [...]sdl.Color{
	ColBackground, ColPanel, ColPanelHi, ColAccent, ColText, ColTextDim, ColDanger,
}

// activeKitColors is the BASE palette applyThemePalette restores to — the stock dark by
// default, or the user's chosen chrome preset (#M3 AsyncAO chrome themes). An AO2 theme's
// stylesheet colours still overlay it; with no theme colours (the common case) the preset
// IS the client chrome. Set on the render thread (theme/preset change), so no lock.
var activeKitColors = defaultKitColors

// Palette derivation ratios for slots QSS doesn't define directly: the
// window background sits a step darker than panels and dim text a step
// darker than text, preserving the kit's depth cues under theme colors.
const (
	paletteBackgroundPct = 70  // background = panel × 70%
	paletteRaisePct      = 130 // derived PanelHi = panel × 130%
	paletteDimPct        = 64  // dim text = text × 64%
)

// scaleColor multiplies a color's channels by pct/100, clamped.
func scaleColor(c sdl.Color, pct int) sdl.Color {
	mul := func(v uint8) uint8 {
		n := int(v) * pct / 100
		if n > 255 {
			n = 255
		}
		return uint8(n)
	}
	return sdl.Color{R: mul(c.R), G: mul(c.G), B: mul(c.B), A: c.A}
}

// blendCol linearly mixes two colors (t=0 → a, t=1 → b), alpha from a.
func blendCol(a, b sdl.Color, t float64) sdl.Color {
	mix := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return sdl.Color{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: a.A}
}

// cardColor is the Settings/About "card" surface: a step between the page
// background and the panel colour, so panel-coloured widgets still read clearly
// on top of it. Derived from the live palette, so chrome presets recolour it.
func cardColor() sdl.Color { return blendCol(ColBackground, ColPanel, 0.55) }

// paletteDarkText is the dark ink the readability floor falls back to on
// LIGHT theme panels (light panels with unreadable sheet text).
var paletteDarkText = sdl.Color{R: 20, G: 20, B: 24, A: 255}

// paletteLightPanelLuma splits "dark panel → light text" from "light
// panel → dark text" in the readability floor.
const paletteLightPanelLuma = 128

// sliderIdleThumbColor is the un-hovered fill for Slider's and VScrollbar's
// thumb (GH #50, "black slider ball"). ColPanelHi's contrast is only ever
// proven against ColBackground — its real job is a raised BUTTON surface
// (see the comment on its assignment below) — so reusing it unchecked here
// puts a small thumb directly on ColPanel instead, a tighter context
// nothing had contrast-checked. Every built-in chrome preset fails the
// kit's own minInkSkinContrast floor for that pair (dark/soft/midnight
// Δ16, warm Δ14, light Δ20, even "High contrast" Δ28 — all under 48), so
// idle the thumb reads as one flat block fused to the track. This reuses
// that SAME floor and the SAME two proven-safe ink extremes
// applyThemePalette's text-readability guard already falls back to below
// (light ink on a dark surface, dark ink on a light one) instead of a
// second contrast system or a constant hand-tuned only for the stock
// defaults — so any preset, custom scheme, or AO2-derived palette gets a
// readable thumb, not just the six shipped today.
func sliderIdleThumbColor() sdl.Color {
	if absInt(colLuma(ColPanelHi)-colLuma(ColPanel)) >= minInkSkinContrast {
		return ColPanelHi // already clears the floor — untouched, today and for any future palette that does too
	}
	if colLuma(ColPanel) < paletteLightPanelLuma {
		return defaultKitColors[4] // stock light ink (ColText's slot), safe on a dark track
	}
	return paletteDarkText // dark ink, safe on a light track
}

// applyThemePalette restores the stock kit palette, then lays the theme's
// courtroom_stylesheets.css colors over it. Slots the stylesheet doesn't
// define keep stock values or derive from defined ones (background from
// panel, dim text from text) so partial stylesheets stay coherent.
func applyThemePalette(p theme.Palette) {
	ColBackground, ColPanel, ColPanelHi, ColAccent, ColText, ColTextDim, ColDanger =
		activeKitColors[0], activeKitColors[1], activeKitColors[2],
		activeKitColors[3], activeKitColors[4], activeKitColors[5],
		activeKitColors[6]
	if p.Empty() {
		return
	}
	if p.Panel != nil {
		ColPanel = sdl.Color{R: p.Panel.R, G: p.Panel.G, B: p.Panel.B, A: 255}
		ColBackground = scaleColor(ColPanel, paletteBackgroundPct)
		// Raised surfaces brighten the panel unless the sheet styles
		// buttons itself.
		ColPanelHi = scaleColor(ColPanel, paletteRaisePct)
	}
	if p.PanelHi != nil {
		ColPanelHi = sdl.Color{R: p.PanelHi.R, G: p.PanelHi.G, B: p.PanelHi.B, A: 255}
	}
	if p.Text != nil {
		ColText = sdl.Color{R: p.Text.R, G: p.Text.G, B: p.Text.B, A: 255}
		ColTextDim = scaleColor(ColText, paletteDimPct)
	}
	if p.Accent != nil {
		ColAccent = sdl.Color{R: p.Accent.R, G: p.Accent.G, B: p.Accent.B, A: 255}
	}
	if p.Danger != nil {
		ColDanger = sdl.Color{R: p.Danger.R, G: p.Danger.G, B: p.Danger.B, A: 255}
	}
	// Readability floor — the chatbox ink guard's rule, applied to the
	// whole kit (playtest: GrayGarden rendered Settings black-on-black).
	// Labels draw on panels and raised surfaces, so text must clear the
	// contrast floor against BOTH; when the sheet's combination fails,
	// re-derive ink from the panel's lightness instead of trusting it.
	textL := colLuma(ColText)
	if absInt(textL-colLuma(ColPanel)) < minInkSkinContrast ||
		absInt(textL-colLuma(ColPanelHi)) < minInkSkinContrast {
		if colLuma(ColPanel) < paletteLightPanelLuma {
			ColText = defaultKitColors[4] // stock light ink on dark panels
		} else {
			ColText = paletteDarkText
		}
		ColTextDim = scaleColor(ColText, paletteDimPct)
	}
}
