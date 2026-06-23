package ui

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Dedicated #M5 Text FX button on the IC bar (its own control, not buried in the emoji
// popup). It's a STICKY cycle — Off → Shake → Wave → Rainbow — so it doesn't depend on
// what's currently in the input box (the earlier emoji-popup strip no-op'd on an empty
// box). When set, sendIC wraps the whole message in that effect's markup, so every line you
// send animates for other AsyncAO players while AO2 / webAO see the plain message. Power
// users can still type per-word [shake]…[/shake] inline (which takes precedence — see
// applyStickyEffect). Shared by the classic + themed layouts so they can't drift.

// fxBtnW is the IC-bar Text FX button width (fits "Rainbow" in the chrome font).
const fxBtnW = 74

// fxButton draws the cycle button at r and advances the sticky effect on a click. Accent
// colours when an effect is active, so the on-state is obvious at a glance.
func (a *App) fxButton(r sdl.Rect) {
	c := a.ctx
	label, border, txt := "FX", ColTextDim, ColText
	if a.icEffect != courtroom.TextEffectNone {
		label, border, txt = icEffectLabel(a.icEffect), ColAccent, ColAccent
	}
	if c.ButtonCol(r, label, ColPanel, ColPanelHi, border, txt) {
		a.icEffect = nextICEffect(a.icEffect)
	}
	c.Tooltip(r, "Text FX: click to cycle Off → Shake → Wave → Rainbow. When on, every message you send animates for other AsyncAO players (AO2/webAO see plain text). Or type [shake]…[/shake] inline.")
}

// icEffectLabel is the button label for the active sticky effect.
func icEffectLabel(e uint8) string {
	switch e {
	case courtroom.TextEffectShake:
		return "Shake"
	case courtroom.TextEffectWave:
		return "Wave"
	case courtroom.TextEffectRainbow:
		return "Rainbow"
	default:
		return "FX"
	}
}

// effectTagName maps a sticky effect to its markup tag name ("" for none/unknown).
func effectTagName(e uint8) string {
	switch e {
	case courtroom.TextEffectShake:
		return "shake"
	case courtroom.TextEffectWave:
		return "wave"
	case courtroom.TextEffectRainbow:
		return "rainbow"
	default:
		return ""
	}
}

// nextICEffect cycles Off → Shake → Wave → Rainbow → Off.
func nextICEffect(e uint8) uint8 {
	if e >= courtroom.TextEffectRainbow {
		return courtroom.TextEffectNone
	}
	return e + 1
}

// applyStickyEffect wraps text in the sticky Text FX tag (the dedicated button), so a normal
// message animates without typing markup. No-op when: the button is Off; the message is a
// blankpost (empty / single space); or the user already typed their own [..] markup (inline
// wins, and wrapping it would create unsupported nesting). Run BEFORE ParseTextEffects so
// the wrap is parsed like any markup.
func (a *App) applyStickyEffect(text string) string {
	tag := effectTagName(a.icEffect)
	if tag == "" || text == "" || text == " " || strings.ContainsRune(text, '[') {
		return text
	}
	return "[" + tag + "]" + text + "[/" + tag + "]"
}
