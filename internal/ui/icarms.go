package ui

// The IC bar's one-shot arms: realization and screenshake. They ride the next message and clear when it is built
// (courtroom.cpp:2327-2329), so they are toggles rather than fire buttons.
// The realization toggle is more than a boolean. AO2's Courtroom::on_realization_clicked (courtroom.cpp:6158-6179)
// does four things on arm:
// 	:6162       realization_state = 1                      (the wire flag)
// 	:6163-6166  effects_dropdown_find_and_set("realization")
// 	            + on_effects_dropdown_changed(...)         (the visible half)
// 	:6168       ui_realization->setImage("realization_pressed")
// 	:6178       focus_ic_input()
// and on disarm zeroes the flag, resets the effects dropdown to index 0 and restores the art (:6172-6175).
// The white flash overlay is driven by the effects field, so setting only the wire flag made the button look
// inert on modern servers; this sets both, at the one place both call sites share.
// Render thread; no allocation beyond the effects-choice slice the picker already caches.

import (
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// toggleRealization is on_realization_clicked, transcribed. The disarm branch resets the effects pick to "None"
// unconditionally, as canon does (courtroom.cpp:6173 calls setCurrentIndex(0) without checking), so un-arming also
// drops an unrelated effect. The two states must stay exact inverses so arm and disarm round-trip.
func (a *App) toggleRealization() {
	a.icRealize = !a.icRealize
	if a.icRealize {
		a.selectOverlayEffectByName(courtroomRealizationEffectName)
	} else {
		a.overlayChoiceIdx = 0
	}
	a.bounceFocusToIC() // :6178 focus_ic_input
}

// toggleShout is on_hold_it_clicked / on_objection_clicked / on_take_that_clicked / on_custom_objection_clicked
// (courtroom.cpp:6048-6127), four copies of one handler:
//
//	if (objection_state == N) { restore this button's art; objection_state = 0 }
//	else                      { restore the OTHER three; select this one;
//	                            objection_state = N }
//	focus_ic_input()
//
// A shout button does not send. It arms; the interjection rides the next message's MS objection field, built from
// objection_state at courtroom.cpp:2136-2147 and cleared at :2327. The "reset the other three" arms need no code:
// one int cannot hold two states.
func (a *App) toggleShout(mod int) {
	if a.icShout == mod {
		a.icShout = 0 // clicking the armed shout disarms it (:6050-6054)
	} else {
		a.icShout = mod
	}
	a.bounceFocusToIC() // :6065 / :6085 / :6105 / :6125 focus_ic_input
}

// toggleScreenshake is on_screenshake_clicked (courtroom.cpp:6181-6196). Unlike realization it doesn't touch the
// effects dropdown (the shake derives from the wire flag alone), so it is the boolean plus the focus bounce.
func (a *App) toggleScreenshake() {
	a.icShake = !a.icShake
	a.bounceFocusToIC()
}

// courtroomRealizationEffectName is the effects.ini entry AO2 looks up by name, aliased from the picker's own
// constant so a rename can't leave this file searching for a missing row.
const courtroomRealizationEffectName = overlayRealizationEffect

// armedOverlayAlpha is how strongly the armed tint washes an icon button. AO2 swaps in a second sprite
// ("realization_pressed" / "screenshake_pressed", courtroom.cpp:6168, :6186); those stems aren't in the theme image
// set loaded off-thread. A wash plus the border is the readable substitute at 42x42 and also works on a theme with
// no art for the button (drawThemeButton's text-chip fallback).
const armedOverlayAlpha = 70

// drawArmedOverlay marks a one-shot arm as active: an accent wash plus the accent border. The border alone is one
// logical pixel on a dark icon and read as "hovered". No-op when not armed.
func (a *App) drawArmedOverlay(r sdl.Rect, armed bool) {
	if !armed {
		return
	}
	c := a.ctx
	c.Fill(r, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: armedOverlayAlpha})
	c.Border(r, ColAccent)
}

// selectOverlayEffectByName is effects_dropdown_find_and_set (courtroom.cpp:5659-5671): point the effects pick at
// the row with this name, or leave it when the character's effects pack has none, so a pack without a realization
// effect can still arm the wire flag. Case-insensitive where AO2 compares exactly, matching effectspicker.go's
// EqualFold special-case (:577), since effects.ini capitalisation varies.
func (a *App) selectOverlayEffectByName(name string) {
	for i, choice := range a.overlayChoices() {
		if strings.EqualFold(strings.TrimSpace(choice), name) {
			a.overlayChoiceIdx = i
			return
		}
	}
}
