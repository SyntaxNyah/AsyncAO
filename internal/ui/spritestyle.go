package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Sprite Style (#103): a per-speaker visual customization of your OWN character —
// recolour, opacity, glow, and gentle motion — that rides invisibly in your
// message text so other AsyncAO players see your styled sprite, while AO2/webAO
// see a normal character (and unaffected text). The picker is a courtroom modal
// opened from the Extras box; the chosen style is sticky (config) and appended to
// each outgoing message in sendIC.

// styleFromPref / prefFromStyle convert between the persisted config pref and the
// courtroom value the wire codec + renderer use (config can't import courtroom).
func styleFromPref(p config.SpriteStylePref) courtroom.SpriteStyle {
	return courtroom.SpriteStyle{
		Tint: p.Tint, R: p.R, G: p.G, B: p.B,
		Opacity: p.Opacity, Glow: p.Glow, Wobble: p.Wobble, Spin: p.Spin,
	}
}

// mySpriteStyle is the user's current transmitted style, for the send path.
func (a *App) mySpriteStyle() courtroom.SpriteStyle {
	return styleFromPref(a.d.Prefs.SpriteStyle())
}

// openSpriteStyle opens the Sprite Style picker modal (from the Extras box).
func (a *App) openSpriteStyle() { a.showSpriteStyle = true }

// spriteStylePresets are quick "fun colour" tints for the picker.
var spriteStylePresets = []struct {
	name    string
	r, g, b uint8
}{
	{"Red", 255, 70, 70}, {"Orange", 255, 140, 40}, {"Gold", 255, 205, 50},
	{"Lime", 150, 240, 70}, {"Green", 60, 220, 90}, {"Cyan", 60, 220, 230},
	{"Blue", 80, 140, 255}, {"Purple", 180, 90, 245}, {"Pink", 255, 110, 220},
	{"White", 255, 255, 255}, {"Shadow", 70, 70, 95},
}

// drawSpriteStylePanel is the Sprite Style picker modal: tint + colour
// presets/sliders, opacity, glow, and motion, plus the viewer off-switch for
// other players' styles. Reached via drawCourtroomModals like the other popups.
func (a *App) drawSpriteStylePanel(w, h int32) {
	c := a.ctx
	p := a.d.Prefs.SpriteStyle()
	panel := sdl.Rect{X: w/2 - 250, Y: h/2 - 235, W: 500, H: 470}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+10, "Sprite Style", ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 34, Y: panel.Y + 8, W: 26, H: 24}, "X") {
		a.showSpriteStyle = false
	}
	x := panel.X + pad
	y := panel.Y + 44
	c.Label(x, y, "Other AsyncAO players see this on YOUR character; AO2 / webAO see it normally.", ColTextDim)
	y += 26

	// --- Recolour (tint) ---
	if next := c.Checkbox(x, y, "Recolour the sprite (tint)", p.Tint); next != p.Tint {
		p.Tint = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	// Live swatch of the current tint (white when off — no recolour).
	swCol := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	if p.Tint {
		swCol = sdl.Color{R: p.R, G: p.G, B: p.B, A: 255}
	}
	swatch := sdl.Rect{X: panel.X + panel.W - pad - 44, Y: y - 2, W: 44, H: 22}
	c.Fill(swatch, swCol)
	c.Border(swatch, ColPanelHi)
	y += 28

	if p.Tint {
		// Quick fun-colour presets (wrap to the panel width).
		px := x
		for _, pr := range spriteStylePresets {
			const bw = 54
			if px+bw > panel.X+panel.W-pad {
				px = x
				y += btnH + 4
			}
			if c.Button(sdl.Rect{X: px, Y: y, W: bw, H: btnH}, pr.name) {
				p.R, p.G, p.B = pr.r, pr.g, pr.b
				a.d.Prefs.SetSpriteStyle(p)
			}
			px += bw + 4
		}
		y += btnH + 10
		// Fine RGB control.
		nr := a.sliderRow(y, "  Red", int(p.R), 5, 0, 255)
		y += 26
		ng := a.sliderRow(y, "  Green", int(p.G), 5, 0, 255)
		y += 26
		nb := a.sliderRow(y, "  Blue", int(p.B), 5, 0, 255)
		y += 28
		if nr != int(p.R) || ng != int(p.G) || nb != int(p.B) {
			p.R, p.G, p.B = uint8(nr), uint8(ng), uint8(nb)
			a.d.Prefs.SetSpriteStyle(p)
		}
	}

	// --- Opacity (floored so it can't go invisible) ---
	op := int(p.Opacity)
	if op == 0 {
		op = 100
	}
	if nop := a.sliderRow(y, "  Opacity %", op, 5, 25, 100); nop != op {
		if nop >= 100 {
			p.Opacity = 0 // 0 = unset = fully opaque
		} else {
			p.Opacity = uint8(nop)
		}
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 30

	// --- Glow + motion ---
	if next := c.Checkbox(x, y, "Glow (neon)", p.Glow); next != p.Glow {
		p.Glow = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	if next := c.Checkbox(x+150, y, "Wobble", p.Wobble); next != p.Wobble {
		p.Wobble = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	if next := c.Checkbox(x+280, y, "Spin", p.Spin); next != p.Spin {
		p.Spin = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 32

	if c.Button(sdl.Rect{X: x, Y: y, W: 130, H: btnH}, "Clear style") {
		a.d.Prefs.SetSpriteStyle(config.SpriteStylePref{})
	}
	y += btnH + 14

	// Viewer off-switch (also in Settings) — ignore everyone ELSE's styles.
	hide := a.d.Prefs.HideSpriteStylesOn()
	if next := c.Checkbox(x, y, "Hide other players' sprite styles (show everyone normally)", hide); next != hide {
		a.d.Prefs.SetHideSpriteStyles(next)
		if a.room != nil {
			a.room.HideSpriteStyles = next // apply to the running session at once
		}
	}
}
