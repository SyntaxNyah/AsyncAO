package ui

import "github.com/SyntaxNyah/AsyncAO/internal/courtroom"

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
// NOT ported, deliberately: the emote_mod → PREANIM promotion at :2107-2115 that makes a
// hand-picked sound audible on an idle line. It is gated on
// Options::playSelectedSFXOnIdle, whose stock value is FALSE (options.cpp:569-572,
// `config.value("sfx_on_idle", false)`), so on a stock AO2 a pick made with Pre unticked
// is transmitted and silent. AsyncAO does not ship that option yet; adding it is a
// preference plus a Settings row, not a change to this rule.
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
