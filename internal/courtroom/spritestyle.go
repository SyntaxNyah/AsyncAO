package courtroom

import "strings"

// SpriteStyle is a speaker's per-message visual customization of their own
// character sprite — recolour, opacity, glow, and gentle motion. It is the
// TRANSMITTED cousin of render.SpriteFX (the viewer's local wash): the speaker
// picks a style, it rides invisibly in the message text (see the zero-width
// codec below), and every AsyncAO viewer renders it on that speaker's sprite —
// while AO2/webAO see a normal, unstyled character (the marker is zero-width, so
// their chat text is unaffected too).
//
// It is plain data (no pointers, SDL-free): the renderer reads it off the
// SpriteLayer and feeds it into the existing SetColorMod/SetBlendMode bracket, so
// applying a style costs nothing on the render hot path. Slice 1 carries only the
// "free" draw-time effects; per-pixel effects (invert/grayscale) are a planned
// follow-up that builds cached variant textures.
type SpriteStyle struct {
	Tint    bool  // multiply the sprite by (R,G,B) — recolour
	R, G, B uint8 // the multiply colour (only meaningful when Tint)
	// Opacity is a percent 1..100 (0 = unset = fully opaque). Applied via
	// SetTextureAlphaMod, clamped to a visible floor so a received style can't
	// make a sprite invisible.
	Opacity  uint8
	Glow     bool // additive blend (neon glow)
	Wobble   bool // gentle continuous sway (viewer ReduceMotion can suppress it)
	Spin     bool // slow continuous rotation (viewer ReduceMotion can suppress it)
	HueCycle bool // transmitted rainbow: cycle the tint hue over time
	FlipH    bool // mirror horizontally
	// Brightness/Scale are percents with 0 = unset (= 100%). Rotation is a fixed
	// tilt: 0 = none, else degrees via Rotation*360/256 (full circle).
	Brightness uint8
	Scale      uint8
	Rotation   uint8
}

// minVisibleOpacity floors a received opacity so nobody can post a fully (or
// near-fully) invisible sprite at others — clamp applied at render time.
const minVisibleOpacity = 25

// Style field bounds (percent ranges; 0 always means "unset" = neutral).
const (
	minStyleBrightness = 20
	maxStyleBrightness = 200
	minStyleScale      = 50
	maxStyleScale      = 150
)

// Active reports whether the style does anything (so the encoder skips it and the
// renderer leaves the blit byte-identical when there's nothing to do).
func (s SpriteStyle) Active() bool {
	return s.Tint || s.Glow || s.Wobble || s.Spin || s.HueCycle || s.FlipH ||
		(s.Opacity != 0 && s.Opacity != 100) ||
		(s.Brightness != 0 && s.Brightness != 100) ||
		(s.Scale != 0 && s.Scale != 100) ||
		s.Rotation != 0
}

// AlphaMod resolves Opacity to an 0..255 SetTextureAlphaMod value, treating 0 as
// opaque and flooring an explicit value at minVisibleOpacity.
func (s SpriteStyle) AlphaMod() uint8 {
	if s.Opacity == 0 || s.Opacity >= 100 {
		return 255
	}
	pct := s.Opacity
	if pct < minVisibleOpacity {
		pct = minVisibleOpacity
	}
	return uint8(int(pct) * 255 / 100)
}

// BrightnessPct / ScalePct resolve their 0-as-unset fields to a usable percent,
// clamped to their range. RotationDeg maps the tilt byte to degrees.
func (s SpriteStyle) BrightnessPct() int {
	return resolvePct(s.Brightness, minStyleBrightness, maxStyleBrightness)
}
func (s SpriteStyle) ScalePct() int { return resolvePct(s.Scale, minStyleScale, maxStyleScale) }
func (s SpriteStyle) RotationDeg() float64 {
	return float64(int(s.Rotation)) * 360.0 / 256.0
}

func resolvePct(v uint8, lo, hi int) int {
	if v == 0 {
		return 100
	}
	if int(v) < lo {
		return lo
	}
	if int(v) > hi {
		return hi
	}
	return int(v)
}

// --- zero-width wire codec --------------------------------------------------
//
// The style travels as a run of INVISIBLE characters appended to the message
// text. Two zero-width symbols encode bits and a sentinel rune frames the run, so
// AsyncAO can locate + decode + strip it while AO2/webAO render nothing (the only
// channel that survives an arbitrary server is the message text — the same reason
// \cN colours ride there — and zero-width keeps standard clients truly unaffected,
// unlike a visible marker). The payload is a fixed 5 bytes: R, G, B, Opacity,
// Flags; a benign failure mode (a mangled run just yields no style) keeps a
// length-truncating server from ever corrupting the visible message.
//
// The codec runes are written as hex code points (NOT literal glyphs) so the
// source stays visible and can't be mangled by an editor or a codepage pipe.
const (
	zwBit0  rune = 0x200B // ZERO WIDTH SPACE      → bit 0
	zwBit1  rune = 0x200C // ZERO WIDTH NON-JOINER → bit 1
	zwStart rune = 0x2060 // WORD JOINER           → payload sentinel

	styleFlagTint     = 1 << 0
	styleFlagGlow     = 1 << 1
	styleFlagWobble   = 1 << 2
	styleFlagSpin     = 1 << 3
	styleFlagHueCycle = 1 << 4
	styleFlagFlipH    = 1 << 5

	// spriteStyleVersion tags the payload so a later field change is detectable —
	// a decoder that doesn't recognise the version yields no style (benign).
	spriteStyleVersion = 1
	spriteStyleBytes   = 9 // version, flags, R, G, B, opacity, brightness, scale, rotation
	spriteStyleBits    = spriteStyleBytes * 8
)

// payloadBytes packs the style into its fixed wire form (version-tagged).
func (s SpriteStyle) payloadBytes() [spriteStyleBytes]byte {
	var flags byte
	if s.Tint {
		flags |= styleFlagTint
	}
	if s.Glow {
		flags |= styleFlagGlow
	}
	if s.Wobble {
		flags |= styleFlagWobble
	}
	if s.Spin {
		flags |= styleFlagSpin
	}
	if s.HueCycle {
		flags |= styleFlagHueCycle
	}
	if s.FlipH {
		flags |= styleFlagFlipH
	}
	return [spriteStyleBytes]byte{spriteStyleVersion, flags, s.R, s.G, s.B, s.Opacity, s.Brightness, s.Scale, s.Rotation}
}

// styleFromBytes is the inverse of payloadBytes.
func styleFromBytes(b [spriteStyleBytes]byte) SpriteStyle {
	if b[0] != spriteStyleVersion {
		return SpriteStyle{} // unknown version → no style
	}
	flags := b[1]
	return SpriteStyle{
		Tint:       flags&styleFlagTint != 0,
		Glow:       flags&styleFlagGlow != 0,
		Wobble:     flags&styleFlagWobble != 0,
		Spin:       flags&styleFlagSpin != 0,
		HueCycle:   flags&styleFlagHueCycle != 0,
		FlipH:      flags&styleFlagFlipH != 0,
		R:          b[2],
		G:          b[3],
		B:          b[4],
		Opacity:    b[5],
		Brightness: b[6],
		Scale:      b[7],
		Rotation:   b[8],
	}
}

// EncodeMarker returns the invisible zero-width run that carries this style,
// suitable for appending to an outgoing message's text. Returns "" for an
// inactive style (nothing to send).
func (s SpriteStyle) EncodeMarker() string {
	if !s.Active() {
		return ""
	}
	pb := s.payloadBytes()
	var sb strings.Builder
	sb.Grow((spriteStyleBits + 1) * 3) // each rune is 3 bytes UTF-8
	sb.WriteRune(zwStart)
	for _, by := range pb {
		for bit := 7; bit >= 0; bit-- { // MSB first
			if by&(1<<uint(bit)) != 0 {
				sb.WriteRune(zwBit1)
			} else {
				sb.WriteRune(zwBit0)
			}
		}
	}
	return sb.String()
}

// hasMarker is a fast guard: the common message has no style, so the strip path
// must not allocate or scan-rebuild when there's no sentinel present.
func hasMarker(text string) bool {
	return strings.ContainsRune(text, zwStart)
}

// DecodeSpriteStyle pulls the style out of a message's text and returns the style
// plus the text with ALL of the codec's zero-width runes removed (so the
// typewriter, blankpost test, IC log, and callword matcher all see clean text).
// No marker → the zero-value style and the original text (no allocation).
func DecodeSpriteStyle(text string) (SpriteStyle, string) {
	if !hasMarker(text) {
		return SpriteStyle{}, text
	}
	return decodeFirstMarker(text), stripZeroWidth(text)
}

// StripSpriteStyle returns just the cleaned text (for callers that only need to
// drop the marker — callword matching, IC log storage). Zero-alloc when absent.
func StripSpriteStyle(text string) string {
	if !hasMarker(text) {
		return text
	}
	return stripZeroWidth(text)
}

// decodeFirstMarker reads the first complete zero-width payload after a sentinel.
// A short or corrupt run yields the zero-value style (benign: no style applied).
func decodeFirstMarker(text string) SpriteStyle {
	i := strings.IndexRune(text, zwStart)
	if i < 0 {
		return SpriteStyle{}
	}
	var bits [spriteStyleBits]byte
	n := 0
	for _, r := range text[i+len(string(zwStart)):] {
		if r == zwBit0 {
			bits[n] = 0
		} else if r == zwBit1 {
			bits[n] = 1
		} else {
			break // first non-bit rune ends the run
		}
		n++
		if n == spriteStyleBits {
			break
		}
	}
	if n < spriteStyleBits {
		return SpriteStyle{}
	}
	var pb [spriteStyleBytes]byte
	for bi := 0; bi < spriteStyleBytes; bi++ {
		var v byte
		for k := 0; k < 8; k++ {
			v = v<<1 | bits[bi*8+k]
		}
		pb[bi] = v
	}
	return styleFromBytes(pb)
}

// stripZeroWidth removes every rune in the codec's private alphabet, yielding the
// visible-only text. Only reached when a sentinel is present.
func stripZeroWidth(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case zwStart, zwBit0, zwBit1:
			return -1 // drop
		default:
			return r
		}
	}, text)
}
