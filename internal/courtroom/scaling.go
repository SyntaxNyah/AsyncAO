package courtroom

import "strings"

// Per-sprite texture filtering, ported from AO2-Client: the user's mode overrides char.ini, not the other way round.
type ScalingMode uint8

const (
	ScalingAuto ScalingMode = iota
	// ScalingSmooth filters with SDL linear.
	ScalingSmooth
	// ScalingPixel point-samples with SDL nearest.
	ScalingPixel
)

// char.ini [Options] scaling= spellings; "fast" is AO2's Qt alias for pixel (see get_scaling).
const (
	scalingKeyPixel  = "pixel"
	scalingKeyFast   = "fast"
	scalingKeySmooth = "smooth"
)

// ScalingMode resolves the char.ini request; unknown and absent are Auto, as AO2 falls through.
func (c *CharINI) ScalingMode() ScalingMode {
	if c == nil {
		return ScalingAuto
	}
	return ParseScalingMode(c.Scaling)
}

// ParseScalingMode maps a raw scaling= value to a mode, ignoring case and spaces because char.inis are hand-edited.
func ParseScalingMode(raw string) ScalingMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case scalingKeyPixel, scalingKeyFast:
		return ScalingPixel
	case scalingKeySmooth:
		return ScalingSmooth
	default:
		return ScalingAuto
	}
}

// scalingFor asks the App what filter a character requested; call once per message, never per frame.
func (c *Courtroom) scalingFor(charName string) ScalingMode {
	if c.Assets == nil || charName == "" {
		return ScalingAuto
	}
	return c.Assets.SpriteScaling(charName)
}

// ResolveScalingMode applies AO2's precedence; the caller settles Auto, as only the draw site knows the target size.
func ResolveScalingMode(user, char ScalingMode) ScalingMode {
	if user != ScalingAuto {
		return user // an explicit user choice is not overridable by an asset
	}
	return char
}
