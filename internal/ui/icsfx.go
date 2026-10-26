package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// The outgoing MS SFX_NAME decision — one concern, one place.
//
// It used to be four lines inline in the IC send path, and the line it was missing is
// the first one AO2 writes: `QString f_sfx = "1";` (../AO2-Client/src/courtroom.cpp:2045).
// The emote's char.ini sound accompanies the PREANIM in AO2, so a message sent with Pre
// unticked carries no sound at all; AsyncAO sent the emote's sound on every line, which
// is the wire half of the "auto SFX fires on non-Pre messages" report (2026-08-08).
//
// Extracted so the rule is drivable on its own (icsfx_test.go) rather than only through
// the whole 200-line send builder — the receive half has its own gate in
// internal/courtroom (preanimSFXPlays) and the two have to be readable side by side.

// icSilentSFX is the AO wire value for "this message plays no sound effect": what AO2
// initialises f_sfx to (courtroom.cpp:2045) and leaves there for a non-preanim message,
// and what play_sfx early-returns on (:4597-4600). Named because it is a wire sentinel,
// not a filename.
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
// pre is our ui_pre (App.icPreanim); pickIdx/picks are the IC-bar picker, whose index 0
// (sfxAutoLabel) is AO2's "Default" row — the row get_char_sfx answers with the emote's
// own get_sfx_name (:5679-5683).
//
// The emote_mod → PREANIM promotion at :2107-2115 is NOT part of this rule: it changes
// the EMOTE MOD, the pre name and the sfx delay, not the sound's name. It lives in
// sfxIdlePromotion below, gated on the matching preference.
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
// The point of the rewrite is that AO2 plays a message's sound off the PREANIM
// stage; a line whose mod says "idle" is silent at every receiver no matter what
// SFX_NAME it carries. Promoting the mod while blanking the pre name is how the
// sound is heard without an animation actually playing.
//
// Returns the (possibly promoted) mod and whether the promotion applied — the
// caller blanks the pre name and zeroes the delay on true, so all three fields
// move together or none do.
//
// prezoom gates the ZOOM arm alone: EmoteModPreanimZoom is a 2.8 value and
// NormalizeOutgoingEmoteMod (protocol/ms.go) already refuses to invent it for a
// server that does not advertise the feature. Sending it anyway from HERE would
// route around that guard, and a strict receiver drops the whole message. The
// IDLE arm needs no feature at all.
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

// applySFXPick lands a per-message SFX choice from either IC bar's dropdown — the
// classic row's and the themed one's — so the three things a pick does cannot
// drift between two copies of the same control:
//
//	the choice itself, the audition, and the focus bounce.
//
// The bounce is canon, not garnish: on_sfx_dropdown_changed's whole body is
// `custom_sfx = ""; focus_ic_input();` plus the remove-button toggle
// (../AO2-Client/src/courtroom.cpp:5498-5505). Picking a sound is a step in
// composing a line, so the keyboard goes back to the line.
//
// next is trusted only as an index: out-of-range rows land the choice without an
// audition, exactly as the two inline copies did.
func (a *App) applySFXPick(next int) {
	a.sfxChoiceIdx = next
	if next > 0 && next < len(a.sfxChoices) {
		a.d.Audio.PlaySFX(a.urls.SFX(a.sfxChoices[next]), 0) // preview the picked sound
	}
	a.bounceFocusToIC() // courtroom.cpp:5501
}
