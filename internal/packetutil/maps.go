// Package packetutil holds the small set of AsyncAO-side conveniences that
// aolib-go intentionally keeps unexported: the FantaCode primitive helpers and
// the wire-int enum maps. Everything canonical is delegated to aolib.
package packetutil

import (
	"strconv"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// Itoa is strconv.Itoa (aolib keeps its own private).
func Itoa(n int) string { return strconv.Itoa(n) }

// AtoiOrZero parses a base-10 integer, returning 0 on empty/malformed input.
func AtoiOrZero(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// GetStr returns body[i] or "" when i is out of range.
func GetStr(body []string, i int) string {
	if i < len(body) {
		return body[i]
	}
	return ""
}

// BoolToWire encodes a boolean as "1"/"0".
func BoolToWire(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// WireToBool decodes a "1"/"0" token to a boolean.
func WireToBool(s string) bool { return s == "1" }

// IntsToStrs maps an int slice to its decimal string form.
func IntsToStrs(ns []int) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = strconv.Itoa(n)
	}
	return out
}

// StrsToInts maps a string slice to ints (lenient).
func StrsToInts(ss []string) []int {
	out := make([]int, len(ss))
	for i, s := range ss {
		out[i] = AtoiOrZero(s)
	}
	return out
}

// DeskModifierToWire maps a DeskModifier to its legacy wire integer.
var DeskModifierToWire = map[aolib.DeskModifier]int{
	aolib.DeskModifierHidden:                      0,
	aolib.DeskModifierShown:                       1,
	aolib.DeskModifierHideDuringPreanim:           2,
	aolib.DeskModifierShowDuringPreanim:           3,
	aolib.DeskModifierHideAndCenterDuringPreanim:  4,
	aolib.DeskModifierShowDuringPreanimThenCenter: 5,
}

// DeskModifierFromWire maps a legacy wire integer to its DeskModifier.
var DeskModifierFromWire = map[int]aolib.DeskModifier{
	0: aolib.DeskModifierHidden,
	1: aolib.DeskModifierShown,
	2: aolib.DeskModifierHideDuringPreanim,
	3: aolib.DeskModifierShowDuringPreanim,
	4: aolib.DeskModifierHideAndCenterDuringPreanim,
	5: aolib.DeskModifierShowDuringPreanimThenCenter,
}

// EmoteModifierToWire maps an EmoteModifier to its legacy wire integer.
var EmoteModifierToWire = map[aolib.EmoteModifier]int{
	aolib.EmoteModifierNoPreanim:           0,
	aolib.EmoteModifierPreanim:             1,
	aolib.EmoteModifierPreanimAndObjection: 2,
	aolib.EmoteModifierUnused3:             3,
	aolib.EmoteModifierUnused4:             4,
	aolib.EmoteModifierZoom:                5,
	aolib.EmoteModifierObjectionZoom:       6,
}

// EmoteModifierFromWire maps a legacy wire integer to its EmoteModifier.
var EmoteModifierFromWire = map[int]aolib.EmoteModifier{
	0: aolib.EmoteModifierNoPreanim,
	1: aolib.EmoteModifierPreanim,
	2: aolib.EmoteModifierPreanimAndObjection,
	3: aolib.EmoteModifierUnused3,
	4: aolib.EmoteModifierUnused4,
	5: aolib.EmoteModifierZoom,
	6: aolib.EmoteModifierObjectionZoom,
}

// FlipToWire maps a Flip to its legacy wire integer.
var FlipToWire = map[aolib.Flip]int{
	aolib.FlipNone:                  0,
	aolib.FlipHorizontal:            1,
	aolib.FlipVertical:              2,
	aolib.FlipHorizontalAndVertical: 3,
}

// FlipFromWire maps a legacy wire integer to its Flip.
var FlipFromWire = map[int]aolib.Flip{
	0: aolib.FlipNone,
	1: aolib.FlipHorizontal,
	2: aolib.FlipVertical,
	3: aolib.FlipHorizontalAndVertical,
}

// ShoutModifierToWire maps a ShoutModifier to its legacy wire integer.
var ShoutModifierToWire = map[aolib.ShoutModifier]int{
	aolib.ShoutModifierNone:      0,
	aolib.ShoutModifierHoldIt:    1,
	aolib.ShoutModifierObjection: 2,
	aolib.ShoutModifierTakeThat:  3,
	aolib.ShoutModifierCustom:    4,
}

// ShoutModifierFromWire maps a legacy wire integer to its ShoutModifier.
var ShoutModifierFromWire = map[int]aolib.ShoutModifier{
	0: aolib.ShoutModifierNone,
	1: aolib.ShoutModifierHoldIt,
	2: aolib.ShoutModifierObjection,
	3: aolib.ShoutModifierTakeThat,
	4: aolib.ShoutModifierCustom,
}

// PenaltyBarToWire maps a PenaltyBar to its legacy wire integer.
var PenaltyBarToWire = map[aolib.PenaltyBar]int{
	aolib.PenaltyBarDefense:     1,
	aolib.PenaltyBarProsecution: 2,
}

// PenaltyBarFromWire maps a legacy wire integer to its PenaltyBar.
var PenaltyBarFromWire = map[int]aolib.PenaltyBar{
	1: aolib.PenaltyBarDefense,
	2: aolib.PenaltyBarProsecution,
}

// TextColorToWire maps a TextColor to its legacy wire integer.
var TextColorToWire = map[aolib.TextColor]int{
	aolib.TextColorWhite:   0,
	aolib.TextColorGreen:   1,
	aolib.TextColorRed:     2,
	aolib.TextColorOrange:  3,
	aolib.TextColorBlue:    4,
	aolib.TextColorYellow:  5,
	aolib.TextColorPink:    6,
	aolib.TextColorCyan:    7,
	aolib.TextColorGrey:    8,
	aolib.TextColorRainbow: 9,
}

// TextColorFromWire maps a legacy wire integer to its TextColor.
var TextColorFromWire = map[int]aolib.TextColor{
	0: aolib.TextColorWhite,
	1: aolib.TextColorGreen,
	2: aolib.TextColorRed,
	3: aolib.TextColorOrange,
	4: aolib.TextColorBlue,
	5: aolib.TextColorYellow,
	6: aolib.TextColorPink,
	7: aolib.TextColorCyan,
	8: aolib.TextColorGrey,
	9: aolib.TextColorRainbow,
}
