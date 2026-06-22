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
// The style travels as a run of INVISIBLE characters appended to the message text:
// a sentinel rune (zwStart) frames the run, then the fixed payload is packed THREE
// bits per rune across an alphabet of eight zero-width symbols, so AsyncAO can
// locate + decode + strip it while AO2/webAO render nothing (the only channel that
// survives an arbitrary server is the message text — the same reason \cN colours
// ride there). Three bits per rune (instead of one) keeps the invisible tail SHORT:
// other clients typewriter the invisible run and blip on each character, so a long
// run was audible blip-spam to webAO listeners (a real playtest complaint). The
// payload is version-tagged; a benign failure mode (a mangled run just yields no
// style) keeps a length-truncating server from corrupting the visible text.
//
// The codec runes are written as hex code points (NOT literal glyphs) so the source
// stays visible and can't be mangled by an editor or a codepage pipe.
const (
	zwStart rune = 0x2060 // WORD JOINER — payload sentinel (proven to pass the wire)

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
)

// octalSyms maps an octal digit (3 bits) to one invisible code point. All eight are
// format (Cf) characters that render nothing on AO2/webAO and pass the wire: the
// classic zero-width trio (200B/C/D), the invisible math operators (siblings of the
// proven zwStart sentinel — same Unicode block + Cf category, so anywhere it passes
// they pass), and the zero-width no-break space. stripZeroWidth drops all of them
// plus zwStart, so it also cleans an OLDER client's 1-bit marker (200B/200C are in
// this set), leaking no invisible runes into the log during a mixed-build playtest.
var octalSyms = [8]rune{
	0x200B, // ZERO WIDTH SPACE
	0x200C, // ZERO WIDTH NON-JOINER
	0x200D, // ZERO WIDTH JOINER
	0x2061, // FUNCTION APPLICATION
	0x2062, // INVISIBLE TIMES
	0x2063, // INVISIBLE SEPARATOR
	0x2064, // INVISIBLE PLUS
	0xFEFF, // ZERO WIDTH NO-BREAK SPACE
}

// octalIndex is the inverse of octalSyms: a rune's 0..7 value, or -1 when it isn't a
// codec data symbol (which ends a run).
func octalIndex(r rune) int {
	for i, s := range octalSyms {
		if r == s {
			return i
		}
	}
	return -1
}

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
	return s.encodeMarker()
}

// encodeMarker emits the zero-width run for s REGARDLESS of Active(). The
// send-on-change path uses it to transmit a CLEAR: an inactive style encodes to the
// default payload, which tells receivers to stop styling that speaker.
func (s SpriteStyle) encodeMarker() string {
	pb := s.payloadBytes()
	var sb strings.Builder
	sb.Grow((spriteStyleBytes*8/3 + 2) * 3) // ~24 data runes + sentinel, 3 UTF-8 bytes each
	sb.WriteRune(zwStart)
	acc, nbits := 0, 0
	for _, by := range pb {
		acc = acc<<8 | int(by)
		nbits += 8
		for nbits >= 3 { // emit whole octal digits, MSB first
			nbits -= 3
			sb.WriteRune(octalSyms[(acc>>uint(nbits))&0x7])
		}
	}
	if nbits > 0 { // pad a trailing partial digit (no-op while spriteStyleBytes*8 is /3)
		sb.WriteRune(octalSyms[(acc<<uint(3-nbits))&0x7])
	}
	return sb.String()
}

// EncodeChangeMarker returns the wire run to append to an outgoing message given prev
// (the style this speaker last TRANSMITTED): "" when nothing changed, the new active
// style's marker when it changed, or a CLEAR marker (decodes to the default → receivers
// stop styling this speaker) when an active style was turned off. So the invisible
// marker rides only the style-CHANGE messages, not every line — other clients
// typewriter the run and blip on each character, so a marker on every line was audible
// spam. Senders track the last-sent style and pass it here; receivers remember each
// speaker's last style and reapply it to the messages that carry no marker.
func (s SpriteStyle) EncodeChangeMarker(prev SpriteStyle) string {
	if s == prev {
		return ""
	}
	if s.Active() {
		return s.encodeMarker()
	}
	if prev.Active() {
		return SpriteStyle{}.encodeMarker() // turned a style off → transmit a clear
	}
	return "" // one inactive variant to another: no visible change, send nothing
}

// HasSpriteMarker reports whether text carries a sprite-style marker — used by the
// recorder to tell a style UPDATE from a no-marker line that inherits the speaker's
// last style (send-on-change), so it can keep recordings self-contained.
func HasSpriteMarker(text string) bool { return hasMarker(text) }

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
	var pb [spriteStyleBytes]byte
	acc, nbits, nbytes := 0, 0, 0
	for _, r := range text[i+len(string(zwStart)):] {
		idx := octalIndex(r)
		if idx < 0 {
			break // first non-symbol rune ends the run
		}
		acc = acc<<3 | idx
		nbits += 3
		if nbits >= 8 {
			nbits -= 8
			pb[nbytes] = byte((acc >> uint(nbits)) & 0xFF)
			nbytes++
			if nbytes == spriteStyleBytes {
				break
			}
		}
	}
	if nbytes < spriteStyleBytes {
		return SpriteStyle{} // short/corrupt run → no style (benign)
	}
	return styleFromBytes(pb)
}

// stripZeroWidth removes every rune in the codec's private alphabet, yielding the
// visible-only text. Only reached when a sentinel is present.
func stripZeroWidth(text string) string {
	return strings.Map(func(r rune) rune {
		if r == zwStart || octalIndex(r) >= 0 {
			return -1 // drop the sentinel + every codec data symbol
		}
		return r
	}, text)
}

// --- per-speaker style memory (send-on-change) -------------------------------

// maxRememberedStyles bounds the per-speaker style memory (hard rule §17.4): the
// distinct char-slot count is finite, but cap it so a malformed stream can't grow the
// map without bound. A clear frees its entry, so it stays near the active-styler count.
const maxRememberedStyles = 512

// rememberStyle records this speaker's transmitted style from an explicit marker. An
// INACTIVE style is a clear — it frees the entry. charID < 0 (system / spectator, no
// stable slot) is not persisted.
func (c *Courtroom) rememberStyle(charID int, s SpriteStyle) {
	if charID < 0 {
		return
	}
	if !s.Active() {
		delete(c.styleByChar, charID)
		return
	}
	if c.styleByChar == nil {
		c.styleByChar = map[int]SpriteStyle{}
	}
	if _, had := c.styleByChar[charID]; !had && len(c.styleByChar) >= maxRememberedStyles {
		return // at the cap — don't admit a new speaker
	}
	c.styleByChar[charID] = s
}

// RecalledStyle returns a speaker's last transmitted style (zero value = none) — used
// for a message that carries no marker (send-on-change) and by the App's recorder to
// keep recordings self-contained.
func (c *Courtroom) RecalledStyle(charID int) SpriteStyle {
	if charID < 0 {
		return SpriteStyle{}
	}
	return c.styleByChar[charID]
}
