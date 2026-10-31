package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// defaultKitColors snapshots the stock palette so a theme switch or picking none restores it exactly.
var defaultKitColors = [...]sdl.Color{
	ColBackground, ColPanel, ColPanelHi, ColAccent, ColText, ColTextDim, ColDanger,
}

// activeKitColors is the base palette applyThemePalette restores to: stock dark or the user's chrome preset.
// Set on the render thread, so no lock.
var activeKitColors = defaultKitColors

// Derivation ratios for colours QSS does not define, keeping the kit's depth cues under theme colours.
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

// cardColor is the Settings/About card surface, between page background and panel so panel widgets read on it.
func cardColor() sdl.Color { return blendCol(ColBackground, ColPanel, 0.55) }

// paletteDarkText is the dark ink the readability floor falls back to on light theme panels.
var paletteDarkText = sdl.Color{R: 20, G: 20, B: 24, A: 255}

// paletteLightPanelLuma splits dark panels (light text) from light panels (dark text) in the readability floor.
const paletteLightPanelLuma = 128

// sliderIdleThumbColor is the un-hovered thumb fill for Slider and VScrollbar.
// ColPanelHi fails minInkSkinContrast on ColPanel for every preset, so this uses applyThemePalette's floor and inks.
func sliderIdleThumbColor() sdl.Color {
	if absInt(colLuma(ColPanelHi)-colLuma(ColPanel)) >= minInkSkinContrast {
		return ColPanelHi // already clears the floor — untouched, today and for any future palette that does too
	}
	if colLuma(ColPanel) < paletteLightPanelLuma {
		return defaultKitColors[4] // stock light ink (ColText's slot), safe on a dark track
	}
	return paletteDarkText // dark ink, safe on a light track
}

// applyThemePalette restores the stock palette and lays the theme's courtroom_stylesheets.css colours over it.
// Undefined colours keep stock or derive from defined ones, so partial stylesheets stay coherent.
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
		// Raised surfaces brighten the panel unless the sheet styles buttons itself.
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
	// Readability floor: text must clear contrast on panel and raised surface, else derive ink from panel lightness.
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
