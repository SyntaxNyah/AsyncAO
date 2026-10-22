package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// The outgoing MS SFX_NAME decision — one concern, one place.
// the first one AO2 writes: `QString f_sfx = "1";` (../AO2-Client/src/courtroom.cpp:2045).
// The emote's char.ini sound accompanies the PREANIM in AO2, so a message sent with Pre
// unticked carries no sound at all; AsyncAO sent the emote's sound on every line, which
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
// (../AO2-Client/src/courtroom.cpp:5498-5505). Picking a sound is a step in
func (a *App) applySFXPick(next int) {
	a.sfxChoiceIdx = next
	if next > 0 && next < len(a.sfxChoices) {
		a.d.Audio.PlaySFX(a.urls.SFX(a.sfxChoices[next]), 0) // preview the picked sound
	}
	a.bounceFocusToIC() // courtroom.cpp:5501
}
