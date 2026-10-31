package ui

import (
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Text FX on the IC bar: a picked effect is sticky, and sendIC wraps every message in its markup, so AsyncAO players
// see the animation while AO2 / webAO see plain text. Shared by the classic and themed layouts.

// fxBtnW fits "Rainbow" / "Sparkle" in the chrome font.
const fxBtnW = 74

// fxEffectOrder is the picker list order: Off, motion effects, colour effects.
var fxEffectOrder = []uint8{
	courtroom.TextEffectNone,
	courtroom.TextEffectShake, courtroom.TextEffectWave, courtroom.TextEffectBounce,
	courtroom.TextEffectSway, courtroom.TextEffectShiver, courtroom.TextEffectWobble,
	courtroom.TextEffectTremble, courtroom.TextEffectFloat,
	courtroom.TextEffectRainbow, courtroom.TextEffectGradient, courtroom.TextEffectPulse,
	courtroom.TextEffectBlink, courtroom.TextEffectSparkle,
}

// inlineMarkupButtons are the position-based AO codes; clicking one inserts it at the IC caret and closes the picker.
var inlineMarkupButtons = []struct{ label, code string }{
	{"Slow down", "{"},
	{"Speed up", "}"},
	{"Shake", `\s`},
	{"Flash", `\f`},
	{"Pause", `\p`},
	{"Bold", `\b`},
	{"Italic", `\i`},
}

// fxPickerRowCount counts the sticky effects, one divider row and the inline-code section.
func fxPickerRowCount() int { return len(fxEffectOrder) + 1 + len(inlineMarkupButtons) }

// fxButton draws the Text FX button, accent-coloured when active; its recorded rect anchors the picker.
func (a *App) fxButton(r sdl.Rect) {
	c := a.ctx
	label, border, txt := "FX", ColTextDim, ColText
	if a.icEffect != courtroom.TextEffectNone {
		label, border, txt = icEffectLabel(a.icEffect), ColAccent, ColAccent
	}
	a.fxBtnRect = r
	if c.ButtonCol(r, label, ColPanel, ColPanelHi, border, txt) {
		a.showFxPicker = !a.showFxPicker
	}
	c.Tooltip(r, "Text FX: pick an animation (shake, wave, bounce, rainbow, sparkle…). When set, every message you send animates for other AsyncAO players (AO2/webAO see plain text). Or type [shake]…[/shake] inline.")
}

func icEffectLabel(e uint8) string {
	switch e {
	case courtroom.TextEffectShake:
		return "Shake"
	case courtroom.TextEffectWave:
		return "Wave"
	case courtroom.TextEffectRainbow:
		return "Rainbow"
	case courtroom.TextEffectBounce:
		return "Bounce"
	case courtroom.TextEffectSway:
		return "Sway"
	case courtroom.TextEffectShiver:
		return "Shiver"
	case courtroom.TextEffectWobble:
		return "Wobble"
	case courtroom.TextEffectTremble:
		return "Tremble"
	case courtroom.TextEffectFloat:
		return "Float"
	case courtroom.TextEffectPulse:
		return "Pulse"
	case courtroom.TextEffectGradient:
		return "Grad"
	case courtroom.TextEffectBlink:
		return "Blink"
	case courtroom.TextEffectSparkle:
		return "Sparkle"
	default:
		return "FX"
	}
}

// effectTagName maps an effect to its markup tag name ("" for none/unknown).
func effectTagName(e uint8) string {
	switch e {
	case courtroom.TextEffectShake:
		return "shake"
	case courtroom.TextEffectWave:
		return "wave"
	case courtroom.TextEffectRainbow:
		return "rainbow"
	case courtroom.TextEffectBounce:
		return "bounce"
	case courtroom.TextEffectSway:
		return "sway"
	case courtroom.TextEffectShiver:
		return "shiver"
	case courtroom.TextEffectWobble:
		return "wobble"
	case courtroom.TextEffectTremble:
		return "tremble"
	case courtroom.TextEffectFloat:
		return "float"
	case courtroom.TextEffectPulse:
		return "pulse"
	case courtroom.TextEffectGradient:
		return "gradient"
	case courtroom.TextEffectBlink:
		return "blink"
	case courtroom.TextEffectSparkle:
		return "sparkle"
	default:
		return ""
	}
}

const fxPickerRow = int32(20)

// fxPickerRect anchors the picker above the FX button, clamped on-screen; the draw and pointer blocking both use it.
func (a *App) fxPickerRect(w, h int32) sdl.Rect {
	pw := int32(120)
	ph := int32(fxPickerRowCount())*fxPickerRow + 8
	// Opened from the Extras menu on a theme with no FX button, fxBtnRect is zero; anchor near the middle, not the
	// corner.
	if a.fxBtnRect.W == 0 && a.fxBtnRect.H == 0 {
		return sdl.Rect{X: (w - pw) / 2, Y: (h - ph) / 2, W: pw, H: ph}
	}
	x := a.fxBtnRect.X
	if x+pw > w-4 {
		x = w - 4 - pw
	}
	if x < 4 {
		x = 4
	}
	y := a.fxBtnRect.Y - ph - 4 // above the button
	if y < 4 {
		y = 4 // tiny window: clamp to the top rather than run off-screen
	}
	return sdl.Rect{X: x, Y: y, W: pw, H: ph}
}

// drawFxPicker is a non-blocking floating list; Esc or clicking the FX button closes it.
func (a *App) drawFxPicker(w, h int32) {
	c := a.ctx
	if c.escPressed {
		a.showFxPicker = false
		return
	}
	r := a.fxPickerRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	y := r.Y + 4
	for _, e := range fxEffectOrder {
		row := sdl.Rect{X: r.X + 4, Y: y, W: r.W - 8, H: fxPickerRow}
		if c.hovering(row) {
			c.Fill(row, ColPanelHi)
			if c.clicked {
				a.icEffect = e
				a.showFxPicker = false
			}
		}
		name, col := "Off", ColTextDim
		if e != courtroom.TextEffectNone {
			name, col = icEffectLabel(e), ColText
		}
		if e == a.icEffect {
			col = ColAccent // mark the active effect
		}
		c.Label(row.X+4, y+3, name, col)
		y += fxPickerRow
	}
	y += 2
	c.Fill(sdl.Rect{X: r.X + 4, Y: y, W: r.W - 8, H: 1}, ColPanelHi)
	y += 5
	c.Label(r.X+4, y+3, "Type in the middle", ColTextDim)
	y += fxPickerRow
	for _, b := range inlineMarkupButtons {
		row := sdl.Rect{X: r.X + 4, Y: y, W: r.W - 8, H: fxPickerRow}
		if c.hovering(row) {
			c.Fill(row, ColPanelHi)
			if c.clicked {
				a.insertICInline(b.code)
				a.showFxPicker = false
			}
		}
		c.Label(row.X+4, y+3, b.label, ColText)
		c.Tooltip(row, "Inserts "+b.code+" at the caret in your IC message")
		y += fxPickerRow
	}
}

// applyStickyEffect wraps text in the sticky effect's tag, unless the message is a blankpost or already has [..]
// markup (wrapping would create unsupported nesting). Run before ParseTextEffects so the wrap is parsed like markup.
func (a *App) applyStickyEffect(text string) string {
	tag := effectTagName(a.icEffect)
	if tag == "" || text == "" || text == " " || strings.ContainsRune(text, '[') {
		return text
	}
	return "[" + tag + "]" + text + "[/" + tag + "]"
}
