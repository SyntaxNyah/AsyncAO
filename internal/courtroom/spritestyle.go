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
	// Invert / Grayscale are PER-PIXEL effects (SetColorMod can't do either): the
	// renderer builds a cached variant texture from the sprite's transformed pixels.
	Invert    bool // negate RGB (keep alpha)
	Grayscale bool // luma-weighted desaturate (keep alpha)
	Sepia     bool // #34 luma → warm brown-tone (keep alpha) — another per-pixel variant
	Posterize bool // #34 quantise each channel to 4 levels (poster / cel-shaded look)
	// Motion is a transmitted, looping movement PATH the sprite follows on the viewport
	// (#34) — none / orbit / bounce / sway / drift. A 3-bit enum (one path at a time, not a
	// flag each) packed into the top of flags2. The viewer's ReduceMotion drops it.
	Motion uint8
	// Brightness/Scale are percents with 0 = unset (= 100%). Rotation is a fixed
	// tilt: 0 = none, else degrees via Rotation*360/256 (full circle).
	Brightness uint8
	Scale      uint8
	Rotation   uint8
	// Outline / DropShadow (#8) are silhouette effects drawn BEHIND the sprite. The
	// flags byte above is full (bits 0-7), so these ride a SECOND flags byte appended to
	// the wire payload ONLY when one is set — so a normal style stays the original 9-byte
	// frame and an OLDER AsyncAO client (which reads only the first 9 bytes) still gets
	// the tint/glow/etc., just without the outline.
	Outline    bool // a white silhouette border around the sprite
	DropShadow bool // a soft dark silhouette offset down-right
	Glitch     bool // #13 chromatic-aberration + occasional jolt (a digital glitch look)
}

// Sprite-motion paths (#34): a 3-bit enum (0..7), so up to 8 named movements with room
// to grow. The render maps each to a clamped parametric offset; the UI cycles them.
const (
	MotionNone     uint8 = iota // no movement (default)
	MotionOrbit                 // circle around the spot
	MotionBounce                // bob up and down
	MotionSway                  // side to side
	MotionDrift                 // a slow figure-8 roam
	MotionShake                 // fast small jitter (vibrate)
	MotionSpiral                // orbit with a pulsing radius (spirals in/out)
	MotionPendulum              // pendulum swing, side to side with a lift at the ends
	MotionCount                 // number of motions, for cycling (fills the 3-bit enum, 0..7)
)

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
		s.Invert || s.Grayscale || s.Sepia || s.Posterize || s.Motion != 0 || s.Outline || s.DropShadow || s.Glitch ||
		(s.Opacity != 0 && s.Opacity != 100) ||
		(s.Brightness != 0 && s.Brightness != 100) ||
		(s.Scale != 0 && s.Scale != 100) ||
		s.Rotation != 0
}

// VariantEffect identifies the per-pixel transform a sprite needs (none / invert /
// grayscale) so the renderer can key + cache a transformed texture. Invert wins if
// both are set (one variant per layer).
type VariantEffect uint8

const (
	VariantNone VariantEffect = iota
	VariantInvert
	VariantGrayscale
	VariantSepia     // #34: luma → warm brown tone
	VariantPosterize // #34: quantise channels (poster look)
	// VariantSilhouette fills every non-transparent pixel white (keeping alpha). The
	// renderer ColorMod-tints + offset-blits it BEHIND the sprite to draw the #8 outline
	// (white) and drop-shadow (dark). It is NOT a main-sprite variant — Variant() never
	// returns it; the render asks for it directly when Outline/DropShadow is set.
	VariantSilhouette
)

// Variant returns the per-pixel variant this style needs (VariantNone when none).
func (s SpriteStyle) Variant() VariantEffect {
	switch {
	case s.Invert:
		return VariantInvert
	case s.Grayscale:
		return VariantGrayscale
	case s.Sepia:
		return VariantSepia
	case s.Posterize:
		return VariantPosterize
	default:
		return VariantNone
	}
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

	styleFlagTint      = 1 << 0
	styleFlagGlow      = 1 << 1
	styleFlagWobble    = 1 << 2
	styleFlagSpin      = 1 << 3
	styleFlagHueCycle  = 1 << 4
	styleFlagFlipH     = 1 << 5
	styleFlagInvert    = 1 << 6
	styleFlagGrayscale = 1 << 7 // flags (byte 1) is full (bits 0-7); new effects ride flags2 below

	// flags2 (byte 9) carries effects added after the first flags byte filled up. It is
	// appended to the payload ONLY when one of its bits is set, so a style without them
	// stays the original spriteStyleBytes-long frame (older clients + existing tests
	// unaffected). A decoder reads it only when the frame is long enough.
	styleFlag2Outline    = 1 << 0
	styleFlag2DropShadow = 1 << 1
	styleFlag2Glitch     = 1 << 2
	styleFlag2Sepia      = 1 << 3 // #34
	styleFlag2Posterize  = 1 << 4 // #34

	// spriteStyleVersion tags the payload so a later field change is detectable —
	// a decoder that doesn't recognise the version yields no style (benign). It stays 1:
	// flags2 is a backward-compatible APPEND, not a format change (an old decoder reads
	// the first spriteStyleBytes and ignores the extra byte).
	spriteStyleVersion = 1
	spriteStyleBytes   = 9  // version, flags, R, G, B, opacity, brightness, scale, rotation
	spriteStyleBytesV2 = 10 // …+ flags2 (only present when an outline/shadow is set)
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

// payloadBytes packs the style into its wire form (version-tagged). It returns the original
// spriteStyleBytes-long payload, plus a trailing flags2 byte ONLY when an outline/shadow is
// set — so the common case is byte-identical to before and an older client still decodes the
// first 9 bytes.
func (s SpriteStyle) payloadBytes() []byte {
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
	if s.Invert {
		flags |= styleFlagInvert
	}
	if s.Grayscale {
		flags |= styleFlagGrayscale
	}
	b := []byte{spriteStyleVersion, flags, s.R, s.G, s.B, s.Opacity, s.Brightness, s.Scale, s.Rotation}
	var flags2 byte
	if s.Outline {
		flags2 |= styleFlag2Outline
	}
	if s.DropShadow {
		flags2 |= styleFlag2DropShadow
	}
	if s.Glitch {
		flags2 |= styleFlag2Glitch
	}
	if s.Sepia {
		flags2 |= styleFlag2Sepia
	}
	if s.Posterize {
		flags2 |= styleFlag2Posterize
	}
	flags2 |= (s.Motion & 0x07) << 5 // motion path rides the top 3 bits of flags2 (#34)
	if flags2 != 0 {                 // append the second flags byte only when it carries something
		b = append(b, flags2)
	}
	return b
}

// styleFromBytes is the inverse of payloadBytes. b must be at least spriteStyleBytes long; a
// trailing flags2 byte (a v2 frame) is decoded when present, and ignored cleanly when absent
// (a v1 frame from an older client).
func styleFromBytes(b []byte) SpriteStyle {
	if len(b) < spriteStyleBytes || b[0] != spriteStyleVersion {
		return SpriteStyle{} // unknown version / truncated → no style
	}
	flags := b[1]
	s := SpriteStyle{
		Tint:       flags&styleFlagTint != 0,
		Glow:       flags&styleFlagGlow != 0,
		Wobble:     flags&styleFlagWobble != 0,
		Spin:       flags&styleFlagSpin != 0,
		HueCycle:   flags&styleFlagHueCycle != 0,
		FlipH:      flags&styleFlagFlipH != 0,
		Invert:     flags&styleFlagInvert != 0,
		Grayscale:  flags&styleFlagGrayscale != 0,
		R:          b[2],
		G:          b[3],
		B:          b[4],
		Opacity:    b[5],
		Brightness: b[6],
		Scale:      b[7],
		Rotation:   b[8],
	}
	if len(b) >= spriteStyleBytesV2 { // v2: the appended flags2 byte
		flags2 := b[9]
		s.Outline = flags2&styleFlag2Outline != 0
		s.DropShadow = flags2&styleFlag2DropShadow != 0
		s.Glitch = flags2&styleFlag2Glitch != 0
		s.Sepia = flags2&styleFlag2Sepia != 0
		s.Posterize = flags2&styleFlag2Posterize != 0
		if m := flags2 >> 5; m < MotionCount { // top 3 bits = motion path (#34); unknown ⇒ none
			s.Motion = m
		}
	}
	return s
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
	return packZeroWidth(s.payloadBytes())
}

// packZeroWidth emits one zero-width run: the sentinel (zwStart) followed by payload
// packed THREE bits per rune over octalSyms, MSB first. Shared low-level encoder for
// both the sprite-style and the profile (#101) codecs.
func packZeroWidth(payload []byte) string {
	var sb strings.Builder
	sb.Grow((len(payload)*8/3 + 2) * 3) // data runes + sentinel, 3 UTF-8 bytes each
	sb.WriteRune(zwStart)
	acc, nbits := 0, 0
	for _, by := range payload {
		acc = acc<<8 | int(by)
		nbits += 8
		for nbits >= 3 { // emit whole octal digits, MSB first
			nbits -= 3
			sb.WriteRune(octalSyms[(acc>>uint(nbits))&0x7])
		}
	}
	if nbits > 0 { // pad a trailing partial digit
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
	s, _ := findStyleFrame(text)
	return s, stripZeroWidth(text)
}

// HasStyleMarker reports whether text carries a sprite-STYLE frame specifically (not
// just any zero-width run). The sprite-style and profile (#101) codecs share this
// channel and sentinel, so a profile-only message also has a zwStart run; the receiver
// must NOT treat that as a style — a style "clear" frees the speaker's remembered style,
// so misreading a profile frame would wipe an active style. Frames are told apart by
// their leading payload byte (a style frame's is spriteStyleVersion).
func HasStyleMarker(text string) bool {
	if !hasMarker(text) {
		return false
	}
	_, ok := findStyleFrame(text)
	return ok
}

// StripSpriteStyle returns just the cleaned text (for callers that only need to
// drop the marker — callword matching, IC log storage). Zero-alloc when absent.
func StripSpriteStyle(text string) string {
	if !hasMarker(text) {
		return text
	}
	return stripZeroWidth(text)
}

// findStyleFrame returns the first zero-width frame whose payload is a sprite style
// (leading byte == spriteStyleVersion, full length) and whether one was found. Scanning
// every frame — not just the first sentinel run — keeps the decode correct when a profile
// frame shares the message. A short/corrupt run yields no style (benign).
func findStyleFrame(text string) (SpriteStyle, bool) {
	for _, fr := range scanZeroWidthFrames(text) {
		if len(fr) >= spriteStyleBytes && fr[0] == spriteStyleVersion {
			return styleFromBytes(fr), true // styleFromBytes reads the v2 flags2 byte if present
		}
	}
	return SpriteStyle{}, false
}

// scanZeroWidthFrames decodes every zero-width run in text into its payload bytes
// (MSB-first, 3 bits per symbol — the shared packing of the sprite-style and profile
// codecs). A sentinel (zwStart) opens a run; the next sentinel or any non-symbol rune
// closes it, so one message can carry more than one frame (a style AND a profile). Only
// reached when a sentinel is present; each codec picks the frame whose leading magic
// byte is its own.
func scanZeroWidthFrames(text string) [][]byte {
	var frames [][]byte
	var cur []byte
	inRun := false
	acc, nbits := 0, 0
	flush := func() {
		if inRun {
			frames = append(frames, cur)
		}
		inRun, cur, acc, nbits = false, nil, 0, 0
	}
	for _, r := range text {
		if r == zwStart {
			flush() // a fresh sentinel ends any prior run and opens a new one
			inRun = true
			continue
		}
		if !inRun {
			continue
		}
		idx := octalIndex(r)
		if idx < 0 {
			flush() // first non-symbol rune ends the run
			continue
		}
		acc = acc<<3 | idx
		nbits += 3
		if nbits >= 8 {
			nbits -= 8
			cur = append(cur, byte((acc>>uint(nbits))&0xFF))
		}
	}
	flush()
	return frames
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
