package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// The outgoing MS SFX_NAME decision. AO2 starts f_sfx at "1" (courtroom.cpp:2045) and the emote's char.ini sound
// accompanies the PREANIM, so a message sent with Pre unticked carries no sound.
// The receive half is preanimSFXPlays in internal/courtroom.

// icSilentSFX is the wire value for "no sound effect": what AO2 starts f_sfx at (courtroom.cpp:2045) and
// play_sfx early-returns on (:4597-4600). A sentinel, not a filename.
const icSilentSFX = "1"

// outgoingSFXName is the SFX_NAME this message ships, transcribed from
// courtroom.cpp:2044-2116:
//
//	:2045       f_sfx starts at the silence sentinel;
//	:2065-2079  Pre ticked and immediate NOT ticked  → f_sfx = get_char_sfx();
//	:2081-2099  otherwise → still `if (ui_pre->isChecked()) f_sfx = get_char_sfx();`
//	            (so the two arms collapse to the single "Pre decides" rule below);
//	:2102-2104  a custom SFX or any dropdown row other than "Default" overrides
//	            f_sfx REGARDLESS of Pre.
//
// pre is our ui_pre (App.icPreanim); pickIdx/picks are the IC-bar picker, whose index 0 (sfxAutoLabel) is AO2's
// "Default" row, answered with the emote's own sfx name (:5679-5683).
// The emote_mod to PREANIM promotion at :2107-2115 is not part of this rule; see sfxIdlePromotion.
func outgoingSFXName(emote *courtroom.Emote, pre bool, pickIdx int, picks []string) string {
	name := icSilentSFX
	if pre {
		name = emote.SFXName // char.ini [SoundN]/[SoundT]/[SoundL] for this emote
	}
	if pickIdx > 0 && pickIdx < len(picks) {
		name = picks[pickIdx]
	}
	if name == "" {
		return icSilentSFX // get_sfx_name's empty default is the sentinel, not an empty field
	}
	return name
}

// sfxIdlePromotion is AO2's sfx_on_idle rewrite, courtroom.cpp:2102-2116:
//
//	:2102       a hand-picked sound (dropdown index != 0) is on the message;
//	:2107       AND Options::playSelectedSFXOnIdle() AND the emote mod is IDLE or ZOOM
//	:2109-2114  → f_pre = "", f_sfx_delay = 0, and IDLE→PREANIM / ZOOM→PREANIM_ZOOM.
//
// AO2 plays a message's sound off the PREANIM stage, so a line whose mod is idle is silent at every receiver.
// Promoting the mod while blanking the pre name makes the sound heard without an animation.
// Returns the (possibly promoted) mod and whether it applied; on true the caller blanks the pre name and delay.
// prezoom gates the ZOOM arm: EmoteModPreanimZoom is a 2.8 value and NormalizeOutgoingEmoteMod (protocol/ms.go)
// refuses it without the feature. Sending it from here would bypass that guard and a strict receiver drops the message.
func sfxIdlePromotion(mod, pickIdx int, sfxOnIdle, prezoom bool) (int, bool) {
	if !sfxOnIdle || pickIdx <= 0 {
		return mod, false
	}
	switch mod {
	case protocol.EmoteModIdle:
		return protocol.EmoteModPreanim, true
	case protocol.EmoteModZoom:
		if !prezoom {
			return mod, false
		}
		return protocol.EmoteModPreanimZoom, true
	}
	return mod, false
}

// applySFXPick applies a per-message SFX choice from either IC bar's dropdown (classic or themed): the choice,
// the audition and the focus bounce.
// The bounce is canon: on_sfx_dropdown_changed is `custom_sfx = ""; focus_ic_input();` (courtroom.cpp:5498-5505).
// next is trusted only as an index: out-of-range rows land the choice without an audition.
func (a *App) applySFXPick(next int) {
	a.sfxChoiceIdx = next
	if next > 0 && next < len(a.sfxChoices) {
		a.d.Audio.PlaySFX(a.urls.SFX(a.sfxChoices[next]), 0) // preview the picked sound
	}
	a.bounceFocusToIC() // courtroom.cpp:5501
}
