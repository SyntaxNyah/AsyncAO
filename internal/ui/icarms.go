package ui

// The IC bar's ONE-SHOT ARMS — realization and screenshake. They ride the NEXT
// message and clear when it is built (courtroom.cpp:2327-2329), which is why they
// are toggles rather than fire buttons.
// This file exists because the realization toggle is NOT just a boolean, and
// treating it as one is exactly why "the Realization button does nothing" was
// reported. AO2's Courtroom::on_realization_clicked (courtroom.cpp:6158-6179)

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// toggleRealization is on_realization_clicked, transcribed.
// The disarm branch resetting the effects pick to "None" UNCONDITIONALLY is
// canon, not an oversight of ours: courtroom.cpp:6173 calls setCurrentIndex(0)
func (a *App) toggleRealization() {
	a.compose.icRealize = !a.compose.icRealize
	if a.compose.icRealize {
		a.selectOverlayEffectByName(courtroomRealizationEffectName)
	} else {
		a.emotepick.overlayChoiceIdx = 0
	}
	a.bounceFocusToIC() // :6178 focus_ic_input
}

// toggleShout is on_hold_it_clicked / on_objection_clicked /
// on_take_that_clicked / on_custom_objection_clicked (courtroom.cpp:6048-6127),
// which are four transcriptions of ONE handler:
// courtroom.cpp:2136-2147 and cleared at :2327. AsyncAO's three shout buttons
// The "reset the other three" arms need no code here: one int cannot hold two
// states, which is the same reason canon's four handlers are equivalent to a
// single assignment.
func (a *App) toggleShout(mod int) {
	if a.compose.icShout == mod {
		a.compose.icShout = 0 // clicking the armed shout disarms it (:6050-6054)
	} else {
		a.compose.icShout = mod
	}
	a.bounceFocusToIC() // :6065 / :6085 / :6105 / :6125 focus_ic_input
}

// toggleScreenshake is on_screenshake_clicked (courtroom.cpp:6181-6196). Unlike
// realization it does NOT touch the effects dropdown — the shake is a viewport
// motion the receiver derives from the wire flag alone — so this really is just
// the boolean, plus the same focus bounce its sibling does.
func (a *App) toggleScreenshake() {
	a.compose.icShake = !a.compose.icShake
	a.bounceFocusToIC()
}

// courtroomRealizationEffectName is the effects.ini entry AO2 looks up by name.
// Aliased from the picker's own constant so a rename cannot leave this file
// searching for a row that no longer exists.
const courtroomRealizationEffectName = overlayRealizationEffect

// armedOverlayAlpha is how strongly the ARMED tint washes an icon button. AO2
// signals the same state by swapping in a whole second sprite —
// "realization_pressed" / "screenshake_pressed" (courtroom.cpp:6168, :6186) — and
// AsyncAO cannot: those stems are not in the theme image set it loads off-thread,
// and adding a stem tier is shared theme-art machinery, not IC-bar work. A wash
// plus the border is the readable substitute at 42x42, and it works on a theme
// that ships no art for the button at all (drawThemeButton's text-chip fallback),
// which the pressed sprite would not.
const armedOverlayAlpha = 70

// drawArmedOverlay marks a one-shot arm as ACTIVE: an accent wash over the button
// plus the accent border. The old signal was the border alone, which is one
// logical pixel on a dark icon and read as "hovered", not "armed" — part of why
// the button looked like it did nothing. No-op when not armed, so an idle IC bar
// draws exactly what it drew before.
func (a *App) drawArmedOverlay(r sdl.Rect, armed bool) {
	if !armed {
		return
	}
	c := a.ctx
	c.Fill(r, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: armedOverlayAlpha})
	c.Border(r, ColAccent)
}

// selectOverlayEffectByName is effects_dropdown_find_and_set
// (courtroom.cpp:5659-5671): point the effects pick at the row with this name, or
// leave it alone when the character's effects pack has no such row — AO2 checks
// the bool for exactly that reason, and a theme/pack without a realization effect
// must still be able to arm the wire flag.
func (a *App) selectOverlayEffectByName(name string) {
	for i, choice := range a.overlayChoices() {
		if strings.EqualFold(strings.TrimSpace(choice), name) {
			a.emotepick.overlayChoiceIdx = i
			return
		}
	}
}
