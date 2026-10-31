package ui

import "strings"

// How a user-assigned plain-key bind is spelled on screen. The plain-key namespaces fire on Alt chords (bareBindKey,
// qol.go), so a chip printing the bare letter would advertise a press that just types into the chat box.

// bareBindChordPrefix is the modifier every plain-key bind namespace fires on; shared with bareBindKey's guard.
const bareBindChordPrefix = "Alt+"

// bareBindUnboundLabel is what the cheat sheet reads for a row with no key; chips pass their own wording.
const bareBindUnboundLabel = "(no key)"

// bareBindLabel renders a user-assigned plain-key bind the way it fires: Alt+<KEY>.
// Single characters come from a table: the wardrobe grid paints one per bound cell every frame and concat allocates.
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

// bareBindLabelTableSize covers 7-bit ASCII, every key SDL_GetKeyName spells as a single character.
const bareBindLabelTableSize = 128

// bareBindLabelTable is read-only after package init, so it needs no lock.
var bareBindLabelTable = func() [bareBindLabelTableSize]string {
	var t [bareBindLabelTableSize]string
	for i := range t {
		if b := byte(i); b > ' ' && b < 0x7f {
			t[i] = bareBindChordPrefix + strings.ToUpper(string(rune(b)))
		}
	}
	return t
}()

// bindChipLabel renders the text of a key-bind chip, so no surface prints the bare letter.
// armed (this chip's capture is in flight) wins outright; unbound is the surface's own wording and varies by design.
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
