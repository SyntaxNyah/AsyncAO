package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Sprite Style (#103): a per-speaker visual customization of your OWN character —
// recolour, opacity, glow, and gentle motion — that rides invisibly in your
// message text so other AsyncAO players see your styled sprite, while AO2/webAO
// see a normal character (and unaffected text). The controls live in a floating,
// non-blocking panel (the Sprite Style box, stylebox.go) opened from the Extras
// box, so you can recolour on the fly without leaving the chat. The chosen style
// is sticky (config) and appended to each outgoing message in sendIC.

// styleFromPref converts the persisted config pref into the courtroom value the
// wire codec + renderer use (config can't import courtroom).
func styleFromPref(p config.SpriteStylePref) courtroom.SpriteStyle {
	return courtroom.SpriteStyle{
		Tint: p.Tint, R: p.R, G: p.G, B: p.B,
		Opacity: p.Opacity, Glow: p.Glow, Wobble: p.Wobble, Spin: p.Spin,
		HueCycle: p.HueCycle, FlipH: p.FlipH,
		Invert: p.Invert, Grayscale: p.Grayscale,
		Outline: p.Outline, DropShadow: p.DropShadow, Glitch: p.Glitch,
		Brightness: p.Brightness, Scale: p.Scale, Rotation: p.Rotation,
	}
}

// mySpriteStyle is the user's current transmitted style, for the send path.
func (a *App) mySpriteStyle() courtroom.SpriteStyle {
	return styleFromPref(a.d.Prefs.SpriteStyle())
}

// openSpriteStyle toggles the floating Sprite Style box (the Extras entry).
func (a *App) openSpriteStyle() { a.showStyleBox = !a.showStyleBox }

// spriteStylePresets are quick "fun colour" tints for the picker swatches.
var spriteStylePresets = []struct {
	name    string
	r, g, b uint8
}{
	{"Red", 255, 70, 70}, {"Orange", 255, 140, 40}, {"Gold", 255, 205, 50},
	{"Lime", 150, 240, 70}, {"Green", 60, 220, 90}, {"Cyan", 60, 220, 230},
	{"Blue", 80, 140, 255}, {"Purple", 180, 90, 245}, {"Pink", 255, 110, 220},
	{"White", 255, 255, 255}, {"Shadow", 70, 70, 95},
	// More recolour options (#34): a fuller palette so a mood is one swatch away.
	{"Crimson", 180, 30, 55}, {"Magenta", 235, 50, 190}, {"Indigo", 85, 65, 200},
	{"Teal", 25, 170, 160}, {"Mint", 155, 245, 205}, {"Lavender", 200, 175, 255},
	{"Rose", 255, 150, 175}, {"Brown", 150, 100, 60}, {"Slate", 110, 125, 150},
}
