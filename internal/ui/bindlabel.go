package ui

import "strings"

// How a user-assigned plain-key bind is SPELLED on screen. One file, because the
// spelling changed under every surface at once: the six plain-key namespaces
// (characters, emotes, macros, shownames, IC phrases, the jukebox, style presets
// and the voice push-to-talk) moved off bare letters onto Alt chords when the IC
// input claimed the bare-letter space (bareBindKey, qol.go), and a chip still
// printing "M" advertises a press that now just types an "m" into the chat box.
//
// bareBindLabel was written for the cheat sheet "so the cheat sheet and the
// Settings rows cannot drift apart" — and then only the cheat sheet called it,
// which is exactly the drift it was meant to prevent. It lives here now, with the
// chip renderer beside it, so the sanctioned path is visible from every call site.

// bareBindChordPrefix is the modifier every plain-key bind namespace moved onto.
// Named so the label and bareBindKey's guard are provably the same chord.
const bareBindChordPrefix = "Alt+"

// bareBindUnboundLabel is what the cheat sheet reads for a namespace row whose key
// went missing; chips pass their own wording to bindChipLabel instead.
const bareBindUnboundLabel = "(no key)"

// bareBindLabel renders a user-assigned plain-key bind the way it now FIRES:
// Alt+<KEY>.
//
// The single-character case is TABLE-LOOKED-UP, not concatenated, because the
// wardrobe grid paints one of these per bound cell on every frame the char-select
// screen is up and `"Alt+" + s` allocates every time. Multi-character SDL key names
// ("space", "f5") fall through to the concat: no draw path repeats one per cell.
func bareBindLabel(key string) string {
	if key == "" {
		return bareBindUnboundLabel
	}
	if len(key) == 1 && key[0] < bareBindLabelTableSize {
		if s := bareBindLabelTable[key[0]]; s != "" {
			return s
		}
	}
	return bareBindChordPrefix + strings.ToUpper(key)
}

// bareBindLabelTableSize covers 7-bit ASCII, which is every key SDL_GetKeyName
// spells as a single character (letters, digits, punctuation).
const bareBindLabelTableSize = 128

// bareBindLabelTable is the memo behind bareBindLabel's fast path: read-only after
// package init, so it needs no lock and costs nothing on the render thread.
var bareBindLabelTable = func() [bareBindLabelTableSize]string {
	var t [bareBindLabelTableSize]string
	for i := range t {
		if b := byte(i); b > ' ' && b < 0x7f {
			t[i] = bareBindChordPrefix + strings.ToUpper(string(rune(b)))
		}
	}
	return t
}()

// bindChipLabel renders the text of a key-bind CHIP — the clickable button or badge
// that shows a bind and arms a capture. It is the one place a chip's text is
// decided, so no surface can drift back to printing the bare letter.
//
// armed ("this chip's capture is in flight") wins outright: the user is being asked
// for a keypress right now, and arming is per-chip. unbound is the surface's own
// wording for "nothing bound yet" and deliberately varies — "+key" on a wardrobe
// cell, "(unbound)" in the voice form, "—" in a macro row, "Bind key" on a Settings
// button. Only the CHORD half is shared, because only the chord half is a fact
// about how the bind fires.
func bindChipLabel(bound string, armed bool, arming, unbound string) string {
	switch {
	case armed:
		return arming
	case bound == "":
		return unbound
	default:
		return bareBindLabel(bound)
	}
}
