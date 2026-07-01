package ui

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// #M5 Text FX on the IC bar. Clicking the FX button opens a small PICKER (a floating list) — with
// 13 effects a sticky cycle-through-all was too many clicks. Pick one and it becomes the STICKY
// effect: sendIC wraps every message in that effect's markup, so your lines animate for other
// AsyncAO players while AO2 / webAO see the plain text. Power users can still type per-word
// [shake]…[/shake] inline (which takes precedence — see applyStickyEffect). Shared by the classic
// + themed layouts so they can't drift.

// fxBtnW is the IC-bar Text FX button width (fits "Rainbow" / "Sparkle" in the chrome font).
const fxBtnW = 74

// fxEffectOrder is the picker list order (Off, then motion effects, then colour effects).
var fxEffectOrder = []uint8{
	courtroom.TextEffectNone,
	courtroom.TextEffectShake, courtroom.TextEffectWave, courtroom.TextEffectBounce,
	courtroom.TextEffectSway, courtroom.TextEffectShiver, courtroom.TextEffectWobble,
	courtroom.TextEffectTremble, courtroom.TextEffectFloat,
	courtroom.TextEffectRainbow, courtroom.TextEffectGradient, courtroom.TextEffectPulse,
	courtroom.TextEffectBlink, courtroom.TextEffectSparkle,
}

// fxButton draws the IC-bar Text FX button; clicking it toggles the picker. Accent-coloured when
// an effect is active so the on-state reads at a glance. Records its rect so the picker anchors
// to it (and the pointer fence can find the picker).
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

// icEffectLabel is the short display name for an effect (button + picker rows).
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

// fxPickerRow is one picker row's height.
const fxPickerRow = int32(20)

// fxPickerRect is the picker's floating rect, anchored just ABOVE the FX button (the IC bar sits
// near the bottom), clamped on-screen. Derived from the remembered button rect so the draw and the
// pointer fence agree.
func (a *App) fxPickerRect(w, h int32) sdl.Rect {
	pw := int32(120)
	ph := int32(len(fxEffectOrder))*fxPickerRow + 8
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

// drawFxPicker paints the effect list; clicking a row sets the sticky effect and closes. A
// non-blocking floating list (chat stays live behind it); Esc / clicking the FX button closes it.
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
}

// applyStickyEffect wraps text in the sticky Text FX tag (the FX button's pick), so a normal
// message animates without typing markup. No-op when: the picker is Off; the message is a
// blankpost (empty / single space); or the user already typed their own [..] markup (inline wins,
// and wrapping it would create unsupported nesting). Run BEFORE ParseTextEffects so the wrap is
// parsed like any markup.
func (a *App) applyStickyEffect(text string) string {
	tag := effectTagName(a.icEffect)
	if tag == "" || text == "" || text == " " || strings.ContainsRune(text, '[') {
		return text
	}
	return "[" + tag + "]" + text + "[/" + tag + "]"
}
