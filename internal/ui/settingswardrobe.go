package ui

// Settings ▸ General ▸ Wardrobe: the Wardrobe window's font, text size and icon size. Font and text size are the
// "wardrobe" panel font, so they also show in Theme ▸ Font and size per panel.

import (
	"fmt"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// wardrobePanelFontID is the Wardrobe's key in the per-panel font settings.
const wardrobePanelFontID = "wardrobe"

// wardrobeIconChoices are the icon sizes offered: the 40 px char_icon convention and whole multiples of it, so icons
// scale evenly.
var wardrobeIconChoices = [...]struct {
	px    int
	label string
}{
	{config.WardrobeIconDefaultPx, "Small (40)"},
	{2 * config.WardrobeIconDefaultPx, "Medium (80)"},
	{config.WardrobeIconMaxPx, "Large (120)"},
}

// Settings row geometry.
const (
	wardSetChoiceW int32 = 120
	wardSetGap     int32 = 6
	wardSetRowGap  int32 = 8
	wardSetLabelY  int32 = 4
	// wardSetHexW is the highlight colour's hex field, and wardSetHexY where it sits beside the wheel's swatch.
	wardSetHexW int32 = 100
	wardSetHexY int32 = 42
	// wardSetDefaultW is the highlight colour's Default button.
	wardSetDefaultW int32 = 100
	// wardHexID is the highlight colour's hex field.
	wardHexID = "wardaccenthex"
)

func (a *App) drawWardrobeSettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsDesc(pad, y, "How the Wardrobe window looks. Leave the font and the text size empty to use the theme's or the client's own.", ColTextDim)
	y += wardSetGap
	cur := a.d.Prefs.Load().PanelFontFor(wardrobePanelFontID)

	c.Label(pad, y+wardSetLabelY, "Font", ColText)
	famRect := sdl.Rect{X: pad + panelFontLabelW, Y: y, W: panelFontFamilyW, H: fieldH}
	next, below := a.fontFamilyField("wardsetfont", famRect, cur.Family, a.panelFontPlaceholder(elemWardrobe))
	if next != cur.Family {
		cur.Family = next
		a.d.Prefs.SetPanelFont(wardrobePanelFontID, cur)
		a.applyPanelFontPrefs()
	}
	c.Tooltip(famRect, "Type to see matching fonts, or give the full path to a .ttf or .otf file")
	y += fieldH + wardSetRowGap + below

	c.Label(pad, y+wardSetLabelY, "Text size", ColText)
	const sizeID = "wardsetsize"
	sizeRect := sdl.Rect{X: pad + panelFontLabelW, Y: y, W: panelFontSizeW, H: fieldH}
	shown := a.panelSizeText(sizeID, wardrobePanelFontID, cur.SizePx)
	if next, _ := c.TextField(sizeID, sizeRect, shown, itoa(a.currentPanelSizePx(elemWardrobe))); next != shown {
		a.commitPanelSize(sizeID, wardrobePanelFontID, cur, next)
	}
	c.Label(sizeRect.X+sizeRect.W+wardSetGap, y+wardSetLabelY, "px", ColTextDim)
	y += fieldH + wardSetRowGap

	c.Label(pad, y+wardSetLabelY, "Icon size", ColText)
	x := pad + panelFontLabelW
	iconPx := a.d.Prefs.Load().WardrobeIconPx()
	for _, ch := range wardrobeIconChoices {
		r := sdl.Rect{X: x, Y: y, W: wardSetChoiceW, H: btnH}
		bg, fg := ColPanel, ColText
		if ch.px == iconPx {
			bg, fg = ColAccent, ColBackground
		}
		if c.ButtonCol(r, ch.label, bg, ColPanelHi, ColAccent, fg) && ch.px != iconPx {
			px := ch.px
			a.d.Prefs.Update(func(sn *config.Settings) { sn.WardrobeIconPxVal = px })
		}
		x += wardSetChoiceW + wardSetGap
	}
	c.Tooltip(sdl.Rect{X: pad + panelFontLabelW, Y: y, W: x - pad - panelFontLabelW, H: btnH},
		"Character icons are 40 × 40; the larger sizes double or triple them. The List view keeps them at 40.")
	y += btnH + wardSetRowGap
	return a.drawWardrobeAccentPicker(y)
}

// drawWardrobeAccentPicker is the Wardrobe's highlight colour: selections, the active buttons, focused fields and
// drop outlines. It reuses the log highlight's wheel, slider and hex field.
func (a *App) drawWardrobeAccentPicker(y int32) int32 {
	c := a.ctx
	pad := a.formX
	set := func(rgb int) { a.d.Prefs.Update(func(sn *config.Settings) { sn.WardrobeAccentVal = rgb }) }
	cur := a.d.Prefs.Load().WardrobeAccentRGB()
	c.Label(pad, y, "Highlight colour: selections, active buttons, focused fields and drop outlines", ColText)
	wy := y + fieldH
	nextY, hx := a.drawWheelPicker(pad, wy, cur, set)
	if c.focusID != wardHexID {
		settings.wardHex = fmt.Sprintf("%06x", cur) // follow the wheel while the field isn't being typed in
	}
	if next, _ := c.TextField(wardHexID, sdl.Rect{X: hx, Y: wy + wardSetHexY, W: wardSetHexW, H: fieldH}, settings.wardHex, "RRGGBB"); next != settings.wardHex {
		settings.wardHex = next
		if col, ok := parse.HexColor(next); ok {
			set(packRGB(col.R, col.G, col.B))
		}
	}
	defR := sdl.Rect{X: hx, Y: wy + wardSetHexY + fieldH + wardSetRowGap, W: wardSetDefaultW, H: btnH}
	if c.Button(defR, "Default") {
		set(0)
	}
	c.Tooltip(defR, "Back to the client's own blue")
	return nextY
}
