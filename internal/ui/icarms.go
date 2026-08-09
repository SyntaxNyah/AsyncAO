package ui

// The IC bar's ONE-SHOT ARMS — realization and screenshake. They ride the NEXT
// message and clear when it is built (courtroom.cpp:2327-2329), which is why they
// are toggles rather than fire buttons.
//
// This file exists because the realization toggle is NOT just a boolean, and
// treating it as one is exactly why "the Realization button does nothing" was
// reported. AO2's Courtroom::on_realization_clicked (courtroom.cpp:6158-6179)
// does FOUR things on arm:
//
//	:6162       realization_state = 1                      — the wire flag
//	:6163-6166  effects_dropdown_find_and_set("realization")
//	            + on_effects_dropdown_changed(...)         — THE VISIBLE HALF
//	:6168       ui_realization->setImage("realization_pressed")
//	:6178       focus_ic_input()
//
// and on disarm zeroes the flag, puts the effects dropdown back to index 0 and
// restores the art (:6172-6175).
//
// AsyncAO shipped only the first of those. The flag went out on the wire, but the
// thing a viewer actually SEES — the white flash overlay — is driven by the
// effects field, which the button never touched, so on any modern server the
// button looked inert. The effects-dropdown rework is what moved that half out
// from under it; this puts it back, at the one place both call sites share.
//
// Render thread; no allocation beyond the effects-choice slice the picker already
// caches.

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// toggleRealization is on_realization_clicked, transcribed.
//
// The disarm branch resetting the effects pick to "None" UNCONDITIONALLY is
// canon, not an oversight of ours: courtroom.cpp:6173 calls setCurrentIndex(0)
// without asking what is selected, so un-arming realization also drops an
// unrelated effect the user had picked. Faithfulness wins here — the button's
// two states have to be exact inverses of each other or the arm/disarm pair stops
// round-tripping, which is a worse bug than the one it would paper over.
func (a *App) toggleRealization() {
	a.icRealize = !a.icRealize
	if a.icRealize {
		a.selectOverlayEffectByName(courtroomRealizationEffectName)
	} else {
		a.overlayChoiceIdx = 0
	}
	a.bounceFocusToIC() // :6178 focus_ic_input
}

// toggleScreenshake is on_screenshake_clicked (courtroom.cpp:6181-6196). Unlike
// realization it does NOT touch the effects dropdown — the shake is a viewport
// motion the receiver derives from the wire flag alone — so this really is just
// the boolean, plus the same focus bounce its sibling does.
func (a *App) toggleScreenshake() {
	a.icShake = !a.icShake
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
//
// Case-insensitive where AO2 compares exactly. Deliberate, and it follows the
// precedent already in this codebase: effectspicker.go's own realization
// special-case (:577) uses EqualFold because effects.ini capitalisation varies in
// the wild, and one comparison in the pair being stricter than the other would
// mean the button armed a row the sound lookup then failed to recognise.
func (a *App) selectOverlayEffectByName(name string) {
	for i, choice := range a.overlayChoices() {
		if strings.EqualFold(strings.TrimSpace(choice), name) {
			a.overlayChoiceIdx = i
			return
		}
	}
}
