package courtroom

import "strings"

// Per-sprite texture filtering, ported from AO2-Client.
//
// Issue #21 label 15: a hand-pixelled character drawn through AsyncAO's single
// client-wide linear filter "becomes a smoothie". AO2 doesn't have one filter —
// it resolves one per sprite, from two inputs, in a strict order:
//
//  1. The user's own resize mode (Options::resizeMode). If they picked Smooth or
//     Pixel outright, that WINS and the character's request is ignored entirely.
//  2. Only if the user is on Auto does the character's char.ini [Options]
//     scaling= key get a say (AOApplication::get_scaling,
//     ../AO2-Client/src/text_file_functions.cpp:175-191).
//  3. If it's still Auto after both — no user preference, no char.ini key — the
//     geometry decides: a sprite being ENLARGED is point-sampled, a sprite being
//     shrunk is filtered (AnimationLayer::calculateFrameGeometry,
//     ../AO2-Client/src/animationlayer.cpp:229-273). That is the rule that fixes
//     the reported character without a pref or a char.ini edit, and it is why
//     Auto is the shipped default.
//
// The order matters and is easy to get backwards: char.ini does NOT override the
// user, the user overrides char.ini.
type ScalingMode uint8

const (
	// ScalingAuto defers: to char.ini if it asks, otherwise to the geometry rule.
	ScalingAuto ScalingMode = iota
	// ScalingSmooth filters (SDL linear) — the client's historical behaviour.
	ScalingSmooth
	// ScalingPixel point-samples (SDL nearest) — crisp pixel art.
	ScalingPixel
)

// char.ini [Options] scaling= spellings. "fast" is AO2's Qt-side alias for
// pixel (Qt::FastTransformation) and real char.inis in the wild use it, so it
// is not a synonym we invented — see get_scaling.
const (
	scalingKeyPixel  = "pixel"
	scalingKeyFast   = "fast"
	scalingKeySmooth = "smooth"
)

// ScalingMode resolves this character's char.ini request. Unknown and absent
// values are Auto — AO2 falls through rather than guessing, so a typo'd key
// leaves the automatic rule in charge instead of forcing a filter.
func (c *CharINI) ScalingMode() ScalingMode {
	if c == nil {
		return ScalingAuto
	}
	return ParseScalingMode(c.Scaling)
}

// ParseScalingMode maps a raw char.ini scaling= value to a mode. Case- and
// space-insensitive: char.inis are hand-edited and "Pixel" / " pixel" appear.
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

// scalingFor asks the App what filter a character requested. Called once per
// message when the sprite layers are built — never per render frame.
func (c *Courtroom) scalingFor(charName string) ScalingMode {
	if c.SpriteScaling == nil || charName == "" {
		return ScalingAuto
	}
	return c.SpriteScaling(charName)
}

// ResolveScalingMode applies AO2's precedence: the user's mode unless it is
// Auto, in which case the character's request, and Auto if neither decided.
// The caller settles a remaining Auto with the geometry rule, because only the
// draw site knows the target size.
func ResolveScalingMode(user, char ScalingMode) ScalingMode {
	if user != ScalingAuto {
		return user // an explicit user choice is not overridable by an asset
	}
	return char
}
