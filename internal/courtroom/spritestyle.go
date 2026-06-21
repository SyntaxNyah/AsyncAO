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
	Tint    bool  // multiply the sprite by (R,G,B) — recolour / brightness
	R, G, B uint8 // the multiply colour (only meaningful when Tint)
	// Opacity is a percent 1..100 (0 = unset = fully opaque). Applied via
	// SetTextureAlphaMod, clamped to a visible floor so a received style can't
	// make a sprite invisible.
	Opacity uint8
	Glow    bool // additive blend (neon glow)
	Wobble  bool // gentle continuous sway (viewer ReduceMotion can suppress it)
	Spin    bool // slow continuous rotation (viewer ReduceMotion can suppress it)
}

// minVisibleOpacity floors a received opacity so nobody can post a fully (or
// near-fully) invisible sprite at others — clamp applied at render time.
const minVisibleOpacity = 25

// Active reports whether the style does anything (so the encoder skips it and the
// renderer leaves the blit byte-identical when there's nothing to do).
func (s SpriteStyle) Active() bool {
	return s.Tint || s.Glow || s.Wobble || s.Spin || (s.Opacity != 0 && s.Opacity != 100)
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

	styleFlagTint   = 1 << 0
	styleFlagGlow   = 1 << 1
	styleFlagWobble = 1 << 2
	styleFlagSpin   = 1 << 3

	spriteStyleBytes = 5 // R,G,B,Opacity,Flags
	spriteStyleBits  = spriteStyleBytes * 8
)

// payloadBytes packs the style into its fixed 5-byte wire form.
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
	return [spriteStyleBytes]byte{s.R, s.G, s.B, s.Opacity, flags}
}

// styleFromBytes is the inverse of payloadBytes.
func styleFromBytes(b [spriteStyleBytes]byte) SpriteStyle {
	flags := b[4]
	return SpriteStyle{
		Tint:    flags&styleFlagTint != 0,
		R:       b[0],
		G:       b[1],
		B:       b[2],
		Opacity: b[3],
		Glow:    flags&styleFlagGlow != 0,
		Wobble:  flags&styleFlagWobble != 0,
		Spin:    flags&styleFlagSpin != 0,
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
	return strings.IndexRune(text, zwStart) >= 0
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
