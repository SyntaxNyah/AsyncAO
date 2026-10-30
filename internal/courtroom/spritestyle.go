package courtroom

import (
	"math"
	"strings"
)

// SpriteStyle is a speaker's transmitted sprite look, carried invisibly in message text; AO2 and webAO don't show it.
type SpriteStyle struct {
	Tint    bool  // multiply the sprite by (R,G,B) — recolour
	R, G, B uint8 // the multiply colour (only meaningful when Tint)
	// Opacity is a percent 1..100 (0 = unset = opaque), floored so a received style can't make a sprite invisible.
	Opacity  uint8
	Glow     bool // additive blend (neon glow)
	Wobble   bool // gentle continuous sway (viewer ReduceMotion can suppress it)
	Spin     bool // slow continuous rotation (viewer ReduceMotion can suppress it)
	HueCycle bool // transmitted rainbow: cycle the tint hue over time
	FlipH    bool // mirror horizontally
	// Invert and Grayscale are per-pixel (SetColorMod can't do them), so the renderer caches a variant texture.
	Invert    bool // negate RGB (keep alpha)
	Grayscale bool // luma-weighted desaturate (keep alpha)
	Sepia     bool // #34 luma → warm brown-tone (keep alpha) — another per-pixel variant
	Posterize bool // #34 quantise each channel to 4 levels (poster / cel-shaded look)
	// Restyle is 0 or a VariantEffect from firstRestyle; it overrides the Invert/Grayscale/Sepia/Posterize bools.
	Restyle uint8
	// Motion is a looping path enum (3 bits at the top of flags2); the viewer's ReduceMotion drops it.
	Motion uint8
	// Path is a drawn loop of 4-bit X/Y bytes on a 16x16 grid centred at 8,8; overrides Motion. ReduceMotion drops it.
	// Fixed array with zeroed unused entries keeps SpriteStyle comparable with ==.
	Path    [maxPathPoints]uint8
	PathLen uint8
	// Brightness and Scale are percents (0 = 100%); Rotation is a tilt in Rotation*360/256 degrees.
	Brightness uint8
	Scale      uint8
	Rotation   uint8
	// Outline and DropShadow ride a second flags byte only when set, so older clients reading 9 bytes get the rest.
	Outline    bool // a silhouette border around the sprite (white by default, or OutlineR/G/B)
	DropShadow bool // a soft dark silhouette offset down-right
	Glitch     bool // #13 chromatic-aberration + occasional jolt (a digital glitch look)
	// OutlineR/G/B colour the outline; 0,0,0 means white, so pure black isn't available. Others draw white.
	OutlineR, OutlineG, OutlineB uint8
	// PaintSplit (0 = off, else split row as percent from top) paints R/G/B above, Paint2R/G/B below; needs hue paint.
	// Rides the tail; older clients draw one colour.
	PaintSplit                uint8
	Paint2R, Paint2G, Paint2B uint8
	// GlitchMode picks the look; GlitchA/B recolour the ghosts (zero = classic). Older clients draw the classic glitch.
	GlitchMode                   uint8
	GlitchAR, GlitchAG, GlitchAB uint8
	GlitchBR, GlitchBG, GlitchBB uint8
}

// Glitch looks; an unknown value from a newer client decodes as Classic.
const (
	GlitchClassic   uint8 = iota // chromatic fringe + an occasional horizontal jolt (#13's original)
	GlitchHeavy                  // wider fringe, faster shimmer, harder + more frequent jolts
	GlitchTorn                   // horizontal slice tearing during the jolt windows
	GlitchStatic                 // signal loss: fringe jitters on both axes + the sprite flickers
	GlitchEcho                   // trailing ghosts: doubled fringe distance + a faint far echo
	GlitchModeCount              // MUST stay last (UI cycle + decode bound)
)

// maxPaintSplit keeps both bands non-empty; decoded values past it mean no split.
const maxPaintSplit = 99

// Sprite-motion paths: a 3-bit enum, so up to 8 named movements.
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

// maxPathPoints stays modest because webAO blips on the run; a path needs 2 points. Length-prefixed, so it can grow.
const maxPathPoints = 16

// minVisibleOpacity stops anyone posting a nearly invisible sprite at others; clamped at render time.
const minVisibleOpacity = 25

// Style field bounds (percent ranges; 0 means unset).
const (
	minStyleBrightness = 20
	maxStyleBrightness = 200
	minStyleScale      = 50
	maxStyleScale      = 150
)

// Active reports whether the style does anything, so the encoder skips it and the blit is unchanged.
func (s SpriteStyle) Active() bool {
	return s.Tint || s.Glow || s.Wobble || s.Spin || s.HueCycle || s.FlipH ||
		s.Invert || s.Grayscale || s.Sepia || s.Posterize || s.Restyle != 0 || s.Motion != 0 || s.PathLen >= 2 || s.Outline || s.DropShadow || s.Glitch ||
		(s.Opacity != 0 && s.Opacity != 100) ||
		(s.Brightness != 0 && s.Brightness != 100) ||
		(s.Scale != 0 && s.Scale != 100) ||
		s.Rotation != 0
}

// VariantEffect is the per-pixel transform the renderer caches a texture for; Invert wins if both are set.
type VariantEffect uint8

const (
	VariantNone VariantEffect = iota
	VariantInvert
	VariantGrayscale
	VariantSepia     // #34: luma → warm brown tone
	VariantPosterize // #34: quantise channels (poster look)
	// VariantSilhouette fills pixels white, tinted behind the sprite for outline and shadow; never from Variant().
	VariantSilhouette
	// Extra looks picked one at a time via SpriteStyle.Restyle; older AsyncAO clients draw the plain sprite.
	VariantRedscale // 6 — monochrome red (luma in the red channel)
	VariantGreenscale
	VariantBluescale
	VariantSolarize  // invert channels above mid — a psychedelic tone-flip
	VariantThreshold // 1-bit black & white
	VariantDuotone   // luma mapped across a fixed two-colour ramp
	VariantWarm      // push warm (more red, less blue)
	VariantCool      // push cool (more blue, less red)
	VariantNeon      // hard contrast punch (vivid)
	VariantInfrared  // false-colour channel rotate
	VariantPixelArt  // #77 mosaic (block-average) + palette quantise → an anime pixel-art look
	VariantCount     // MUST stay last (UI + decode bound)
)

// firstRestyle starts the extra-restyle range; only firstRestyle..VariantCount-1 travel on the wire.
const firstRestyle = VariantRedscale

// Variant returns the per-pixel variant needed; Restyle wins over the classic bools.
func (s SpriteStyle) Variant() VariantEffect {
	if s.Restyle >= uint8(firstRestyle) && s.Restyle < uint8(VariantCount) {
		return VariantEffect(s.Restyle)
	}
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

// AlphaMod resolves Opacity to 0..255, treating 0 as opaque and flooring at minVisibleOpacity.
func (s SpriteStyle) AlphaMod() uint8 {
	if s.Opacity == 0 || s.Opacity >= percentMax {
		return math.MaxUint8
	}
	pct := s.Opacity
	if pct < minVisibleOpacity {
		pct = minVisibleOpacity
	}
	return uint8(int(pct) * math.MaxUint8 / percentMax)
}

// BrightnessPct and ScalePct resolve 0-as-unset and clamp; RotationDeg maps the tilt byte to degrees.
func (s SpriteStyle) BrightnessPct() int {
	return resolvePct(s.Brightness, minStyleBrightness, maxStyleBrightness)
}
func (s SpriteStyle) ScalePct() int { return resolvePct(s.Scale, minStyleScale, maxStyleScale) }
func (s SpriteStyle) RotationDeg() float64 {
	return float64(int(s.Rotation)) * degreesPerTurn / rotationSteps
}

func resolvePct(v uint8, lo, hi int) int {
	if v == 0 {
		return percentMax
	}
	if int(v) < lo {
		return lo
	}
	if int(v) > hi {
		return hi
	}
	return int(v)
}

// Zero-width wire codec. Runes are hex code points, so an editor or codepage pipe can't mangle them.
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

	// flags2 (byte 9) is appended only when a bit is set; decoders read it only if the frame is long enough.
	styleFlag2Outline    = 1 << 0
	styleFlag2DropShadow = 1 << 1
	styleFlag2Glitch     = 1 << 2
	styleFlag2Sepia      = 1 << 3 // #34
	styleFlag2Posterize  = 1 << 4 // #34

	// spriteStyleVersion tags the payload, and an unknown one yields no style; flags2 is a compatible append.
	spriteStyleVersion = 1
	spriteStyleBytes   = 9  // version, flags, R, G, B, opacity, brightness, scale, rotation
	spriteStyleBytesV2 = 10 // …+ flags2 (only present when an outline/shadow is set)

	// The tail is one flags byte, then flagged groups in bit order; new groups append, so offsets stay fixed.
	// Some older clients read what follows restyle as outline colour, so write it first when Outline is on.
	tailFlagPaint2  = 1 << 0 // 4 bytes: split, Paint2R, Paint2G, Paint2B
	tailFlagGlitchX = 1 << 1 // 7 bytes: mode, GlitchA RGB, GlitchB RGB
)

// Each invisible rune carries one octal digit and takes three UTF-8 bytes.
const (
	bitsPerOctal = 3
	bitsPerByte  = 8
	zwRuneBytes  = 3
)

const (
	percentMax     = 100 // a style percentage at its neutral value
	rotationSteps  = 256 // the tilt byte covers one full turn
	degreesPerTurn = 360
	motionShift    = 5 // the motion path rides the top 3 bits of flags2
	motionMask     = 0x07
)

// octalSyms maps 3 bits to invisible Cf runes that pass the wire; stripZeroWidth also cleans older 1-bit markers.
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

// octalIndex inverts octalSyms; -1 for a non-symbol, which ends a run.
func octalIndex(r rune) int {
	for i, s := range octalSyms {
		if r == s {
			return i
		}
	}
	return -1
}

// payloadBytes packs the style; flags2 is added only when needed, so older clients still decode the first 9 bytes.
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
	flags2 |= (s.Motion & motionMask) << motionShift // #34
	// Append flags2 if a path follows, since the path sits right after it and byte positions must be fixed.
	hasPath := s.PathLen >= 2 && s.PathLen <= maxPathPoints
	hasRestyle := s.Restyle != 0 // the "10 more restyles" picker
	hasPaint2 := s.PaintSplit >= 1 && s.PaintSplit <= maxPaintSplit
	hasGlitchX := s.Glitch && (s.GlitchMode != 0 ||
		s.GlitchAR != 0 || s.GlitchAG != 0 || s.GlitchAB != 0 ||
		s.GlitchBR != 0 || s.GlitchBG != 0 || s.GlitchBB != 0)
	hasTail := hasPaint2 || hasGlitchX
	// With a tail and Outline on, write the colour even at 0,0,0: decoders find bytes after restyle by position.
	hasOutlineCol := s.Outline && (s.OutlineR != 0 || s.OutlineG != 0 || s.OutlineB != 0 || hasTail)
	hasExt := hasRestyle || hasOutlineCol || hasTail // fields after the path share the restyle-byte slot for a deterministic offset
	if flags2 != 0 || hasPath || hasExt {
		b = append(b, flags2)
		switch {
		case hasPath: // #34 custom path: [len][len packed-XY bytes] — rides send-on-change only
			b = append(b, s.PathLen)
			b = append(b, s.Path[:s.PathLen]...)
		case hasExt:
			b = append(b, 0) // pathLen 0 (no path) so the extension bytes land at a deterministic offset
		}
		if hasExt {
			b = append(b, s.Restyle) // 0 when only an outline colour / tail follows
			if hasOutlineCol {
				b = append(b, s.OutlineR, s.OutlineG, s.OutlineB)
			}
			if hasTail { // presence-flagged tail: [tailFlags][groups in bit order]
				var tf byte
				if hasPaint2 {
					tf |= tailFlagPaint2
				}
				if hasGlitchX {
					tf |= tailFlagGlitchX
				}
				b = append(b, tf)
				if hasPaint2 {
					b = append(b, s.PaintSplit, s.Paint2R, s.Paint2G, s.Paint2B)
				}
				if hasGlitchX {
					b = append(b, s.GlitchMode, s.GlitchAR, s.GlitchAG, s.GlitchAB, s.GlitchBR, s.GlitchBG, s.GlitchBB)
				}
			}
		}
	}
	return b
}

// styleFromBytes inverts payloadBytes; b must be at least spriteStyleBytes long, and flags2 is optional.
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
		if m := flags2 >> motionShift; m < MotionCount { // top 3 bits = motion path (#34); unknown ⇒ none
			s.Motion = m
		}
		// Custom path after flags2: b[10] = point count, b[11:] = packed XY bytes.
		restyleOff := spriteStyleBytesV2 // if there's no path region at all, nothing follows flags2
		if len(b) >= spriteStyleBytesV2+1 {
			pl := b[spriteStyleBytesV2]
			restyleOff = spriteStyleBytesV2 + 1 // past the pathLen byte
			if pl >= 2 && pl <= maxPathPoints && len(b) >= spriteStyleBytesV2+1+int(pl) {
				copy(s.Path[:], b[spriteStyleBytesV2+1:spriteStyleBytesV2+1+int(pl)])
				s.PathLen = pl
				restyleOff = spriteStyleBytesV2 + 1 + int(pl) // past the path bytes
			}
		}
		// After the path: restyle, 3-byte colour (with Outline), then tail. A bad restyle draws the plain sprite.
		if len(b) > restyleOff {
			if r := b[restyleOff]; r >= uint8(firstRestyle) && r < uint8(VariantCount) {
				s.Restyle = r
			}
			off := restyleOff + 1
			// Colour needs the Outline flag and the bytes; the flag gives the tail a known start.
			if s.Outline && len(b) >= off+3 {
				s.OutlineR, s.OutlineG, s.OutlineB = b[off], b[off+1], b[off+2]
				off += 3
			}
			// Tail: unknown flag bits mean a newer client's groups follow ours (append-only order).
			if len(b) > off {
				tf := b[off]
				off++
				if tf&tailFlagPaint2 != 0 && len(b) >= off+4 {
					if sp := b[off]; sp >= 1 && sp <= maxPaintSplit { // out-of-range split → no split, style kept
						s.PaintSplit = sp
						s.Paint2R, s.Paint2G, s.Paint2B = b[off+1], b[off+2], b[off+3]
					}
					off += 4
				}
				if tf&tailFlagGlitchX != 0 && len(b) >= off+7 {
					if m := b[off]; m < GlitchModeCount { // a newer client's mode → Classic (benign)
						s.GlitchMode = m
					}
					s.GlitchAR, s.GlitchAG, s.GlitchAB = b[off+1], b[off+2], b[off+3]
					s.GlitchBR, s.GlitchBG, s.GlitchBB = b[off+4], b[off+5], b[off+6]
					// future groups parse from off+7
				}
			}
		}
	}
	return s
}

// EncodeMarker returns the invisible run for this style, "" when inactive.
func (s SpriteStyle) EncodeMarker() string {
	if !s.Active() {
		return ""
	}
	return s.encodeMarker()
}

// encodeMarker ignores Active() so send-on-change can transmit a clear: the default payload stops styling.
func (s SpriteStyle) encodeMarker() string {
	return packZeroWidth(s.payloadBytes())
}

// packZeroWidth emits zwStart then the payload at three bits per rune, MSB first; shared by the codecs.
func packZeroWidth(payload []byte) string {
	var sb strings.Builder
	sb.Grow((len(payload)*bitsPerByte/bitsPerOctal + 2) * zwRuneBytes) // data runes + sentinel
	sb.WriteRune(zwStart)
	acc, nbits := 0, 0
	for _, by := range payload {
		acc = acc<<bitsPerByte | int(by)
		nbits += bitsPerByte
		for nbits >= bitsPerOctal { // emit whole octal digits, MSB first
			nbits -= bitsPerOctal
			sb.WriteRune(octalSyms[(acc>>uint(nbits))&0x7])
		}
	}
	if nbits > 0 { // pad a trailing partial digit
		sb.WriteRune(octalSyms[(acc<<uint(bitsPerOctal-nbits))&0x7])
	}
	return sb.String()
}

// EncodeChangeMarker returns "" when unchanged, else the marker or a clear; others blip on each invisible rune.
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

// hasMarker lets the common no-style message skip the strip path without allocating.
func hasMarker(text string) bool {
	return strings.ContainsRune(text, zwStart)
}

// DecodeSpriteStyle returns the style and clean text; no marker gives the zero style with no allocation.
func DecodeSpriteStyle(text string) (SpriteStyle, string) {
	if !hasMarker(text) {
		return SpriteStyle{}, text
	}
	s, _ := findStyleFrame(text)
	return s, stripZeroWidth(text)
}

// HasStyleMarker checks for a style frame only: codecs share the sentinel, and a misread clear wipes a style.
func HasStyleMarker(text string) bool {
	if !hasMarker(text) {
		return false
	}
	_, ok := findStyleFrame(text)
	return ok
}

// StripSpriteStyle returns just the clean text; zero-alloc when absent.
func StripSpriteStyle(text string) string {
	if !hasMarker(text) {
		return text
	}
	return stripZeroWidth(text)
}

// findStyleFrame scans every frame, since a profile frame may share the message; a corrupt run yields no style.
func findStyleFrame(text string) (SpriteStyle, bool) {
	for _, fr := range scanZeroWidthFrames(text) {
		if len(fr) >= spriteStyleBytes && fr[0] == spriteStyleVersion {
			return styleFromBytes(fr), true // styleFromBytes reads the v2 flags2 byte if present
		}
	}
	return SpriteStyle{}, false
}

// scanZeroWidthFrames decodes every run; the next sentinel or non-symbol closes one, so frames can share a message.
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
		acc = acc<<bitsPerOctal | idx
		nbits += bitsPerOctal
		if nbits >= bitsPerByte {
			nbits -= bitsPerByte
			cur = append(cur, byte(acc>>uint(nbits))) // byte() keeps the low 8 bits
		}
	}
	flush()
	return frames
}

// stripZeroWidth removes every codec rune; only reached when a sentinel is present.
func stripZeroWidth(text string) string {
	return strings.Map(func(r rune) rune {
		if r == zwStart || octalIndex(r) >= 0 {
			return -1 // drop the sentinel + every codec data symbol
		}
		return r
	}, text)
}

// maxRememberedStyles bounds the per-speaker memory (hard rule 4); a clear frees its entry.
const maxRememberedStyles = 512

// rememberStyle records a transmitted style; an inactive style clears it, and charID < 0 is not kept.
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

// RecalledStyle returns a speaker's last style, for messages with no marker and for the App's recorder.
func (c *Courtroom) RecalledStyle(charID int) SpriteStyle {
	if charID < 0 {
		return SpriteStyle{}
	}
	return c.styleByChar[charID]
}

// filterStyleForViewer applies opt-outs; it must cover every other player's layer, including a pair partner.
func (c *Courtroom) filterStyleForViewer(style SpriteStyle) SpriteStyle {
	if c.HideSpriteStyles {
		return SpriteStyle{} // viewer opted out of others' styles entirely
	}
	if c.ReduceMotion {
		// Drop every animating style. A drawn Path overrides Motion, so zero it; Glitch is a photosensitivity hazard.
		style.Wobble, style.Spin, style.Motion = false, false, 0
		style.PathLen = 0
		style.Path = [maxPathPoints]uint8{} // clear the residual waypoints too: SpriteStyle is ==-comparable, so stale bytes could split an equality-keyed cache
		style.HueCycle = false
		style.Glitch, style.GlitchMode = false, 0
	}
	return style
}
