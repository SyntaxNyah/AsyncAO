package courtroom

import "strings"

// Animated chat text (#M5): a speaker can tag spans of their message with a motion/colour
// effect — shake, wave, rainbow. The spans ride the SAME invisible zero-width channel as
// sprite style (#103) / profile (#101) / status (#M1), told apart by a leading magic byte,
// so every AsyncAO viewer animates the text while AO2/webAO render the plain message: the
// [shake]…[/shake] markup is stripped before send and the spans are invisible on the wire.
//
// Unlike style/profile/status (per-speaker STATE, sent on change), effects are per-message
// CONTENT, so the frame rides every message that carries an effect (there is nothing to
// recall). The codec is SDL-free plain data; the UI translates a TextEffectSpan into the
// render package's EffectSpan.

// Text effect ids. These MUST match render.Effect* (the UI maps courtroom→render); courtroom
// is SDL-free and can't import render, so the values are duplicated here and pinned equal by
// a ui-package test (TestEffectIDsMatchRender).
const (
	TextEffectNone uint8 = iota
	TextEffectShake
	TextEffectWave
	TextEffectRainbow
	// Motion effects (glyph displacement):
	TextEffectBounce  // each glyph hops up in sequence
	TextEffectSway    // horizontal travelling sway (the x-cousin of wave)
	TextEffectShiver  // fast tiny horizontal tremor (nervous)
	TextEffectWobble  // slow circular per-glyph drift (floaty / ghostly)
	TextEffectTremble // fast tiny VERTICAL tremor (the y-cousin of shiver)
	TextEffectFloat   // gentle synchronised up-and-down drift (calm)
	// Colour effects (per-glyph colour):
	TextEffectPulse    // rhythmic brightness shimmer travelling along the word
	TextEffectGradient // a STATIC multicolour band — colourful but calm (readable + photosensitive-safe)
	TextEffectBlink    // hard brightness on/off (attention)
	TextEffectSparkle  // occasional twinkle toward white per glyph
	// TextEffectCount MUST stay last. The UI cycles modulo it, and the decoder drops any effect
	// id >= it — so a NEWER client's effect renders as plain text on an OLDER one (graceful).
	TextEffectCount
)

// TextEffectSpan tags Len consecutive runes (from Start — a rune index into the CLEAN
// display text, i.e. after BOTH the zero-width strip and the \cN chat-markup strip) with an
// effect. SDL-free; the UI converts it to render.EffectSpan at raster time.
type TextEffectSpan struct {
	Start  int
	Len    int
	Effect uint8
}

const (
	// effectsMarkerMagic is this frame's leading payload byte — distinct from spriteStyle
	// version 0x01, profile 0x70 and status 0x71, so the shared scanner tells the frames
	// apart on a message that carries several.
	effectsMarkerMagic = 0x72

	// maxEffectSpans bounds the spans per message (hard rule §17.4): each span is
	// effectSpanBytes wire bytes ≈ 11 invisible runes that other clients typewriter + blip
	// on, so a crafted stream must not be able to grow the tail without bound. A real
	// message uses 1–4.
	maxEffectSpans = 8

	// effectSpanBytes is the per-span wire size: effect(1) + start(2, big-endian) + len(1).
	effectSpanBytes = 4

	// Field ceilings — start is two bytes, len is one.
	maxEffectStart = 0xFFFF
	maxEffectLen   = 0xFF

	// maxTagLen caps how far matchTag scans for a closing ']' so a stray '[' can't make the
	// parser quadratic; the longest real tag is "[rainbow]" (9).
	maxTagLen = 12
)

// effectTags maps a recognised opening-tag name to its effect. SQUARE brackets delimit the
// markup, deliberately NOT braces: AO uses `{ }` for typewriter speed, so braces would be
// eaten by the pipeline. The tags are stripped before send and never reach the wire.
var effectTags = map[string]uint8{
	"shake":    TextEffectShake,
	"wave":     TextEffectWave,
	"rainbow":  TextEffectRainbow,
	"bounce":   TextEffectBounce,
	"sway":     TextEffectSway,
	"shiver":   TextEffectShiver,
	"wobble":   TextEffectWobble,
	"tremble":  TextEffectTremble,
	"float":    TextEffectFloat,
	"pulse":    TextEffectPulse,
	"gradient": TextEffectGradient,
	"blink":    TextEffectBlink,
	"sparkle":  TextEffectSparkle,
}

// ParseTextEffects splits a raw IC input into the WIRE text (effect tags removed; all other
// markup — \cN colour, { } speed, \b \i — left for the normal pipeline) and the
// display-indexed effect spans. The spans index the FINAL visible text the receiver shows:
// because the effect tags ([..]) and chat markup (\cN { } \b \i) use DISJOINT trigger
// characters, stripping one never shifts the other's matches, so spans measured against
// StripChatMarkup(raw) align exactly with the receiver's MessageText (= StripChatMarkup of
// the wire text). Pinned by TestParseTextEffectsAlignment.
func ParseTextEffects(raw string) (wire string, spans []TextEffectSpan) {
	if !strings.ContainsRune(raw, '[') {
		return raw, nil // no possible tag → nothing to strip or index (0-alloc common case)
	}
	wire, _ = parseEffectTags(raw)                   // drop tags, keep \cN etc. for the wire
	_, spans = parseEffectTags(StripChatMarkup(raw)) // spans over the receiver's display text
	return wire, spans
}

// parseEffectTags removes recognised [tag]…[/tag] runs from s and returns the cleaned text
// plus the spans (rune indices into the cleaned text). Unknown brackets ("[OOC]") are left
// literal; an unclosed tag runs to the end; there is no nesting — a new opening tag closes
// the previous span and opens a new one (simple + predictable). Zero-alloc fast path when s
// has no '['.
func parseEffectTags(s string) (string, []TextEffectSpan) {
	if !strings.ContainsRune(s, '[') {
		return s, nil
	}
	rs := []rune(s)
	out := make([]rune, 0, len(rs))
	var spans []TextEffectSpan
	active := TextEffectNone
	start := 0
	for i := 0; i < len(rs); {
		if rs[i] == '[' {
			if name, isClose, n, ok := matchTag(rs, i); ok {
				if _, known := effectTags[name]; known {
					if isClose {
						if active != TextEffectNone {
							spans = appendEffectSpan(spans, start, len(out), active)
							active = TextEffectNone
						}
					} else {
						if active != TextEffectNone {
							spans = appendEffectSpan(spans, start, len(out), active)
						}
						active = effectTags[name]
						start = len(out)
					}
					i += n
					continue
				}
				out = append(out, rs[i:i+n]...) // a bracket group, but not our tag → literal
				i += n
				continue
			}
		}
		out = append(out, rs[i])
		i++
	}
	if active != TextEffectNone {
		spans = appendEffectSpan(spans, start, len(out), active)
	}
	return string(out), spans
}

// matchTag tries to read a "[name]" or "[/name]" group starting at rs[i] (== '['). It
// returns the lower-case tag name (without the slash), whether it was a closing tag, the
// rune length of the whole group, and whether a group was found within maxTagLen.
func matchTag(rs []rune, i int) (name string, isClose bool, n int, ok bool) {
	end := i + maxTagLen
	if end > len(rs) {
		end = len(rs)
	}
	for j := i + 1; j < end; j++ {
		if rs[j] == ']' {
			inner := rs[i+1 : j]
			if len(inner) > 0 && inner[0] == '/' {
				isClose = true
				inner = inner[1:]
			}
			return string(inner), isClose, j - i + 1, true
		}
		if rs[j] == '[' {
			return "", false, 0, false // a nested '[' means this one isn't a tag
		}
	}
	return "", false, 0, false
}

// appendEffectSpan adds a [start,end) span for eff, dropping empty spans and respecting the
// per-message cap and the field ceilings (a clamp keeps a malformed input benign).
func appendEffectSpan(spans []TextEffectSpan, start, end int, eff uint8) []TextEffectSpan {
	if end <= start || len(spans) >= maxEffectSpans || start > maxEffectStart {
		return spans
	}
	ln := end - start
	if ln > maxEffectLen {
		ln = maxEffectLen
	}
	return append(spans, TextEffectSpan{Start: start, Len: ln, Effect: eff})
}

// EncodeEffectsMarker returns the invisible zero-width run carrying these spans, to append
// to an outgoing message's text. "" when there are no spans (nothing to send).
func EncodeEffectsMarker(spans []TextEffectSpan) string {
	if len(spans) == 0 {
		return ""
	}
	n := len(spans)
	if n > maxEffectSpans {
		n = maxEffectSpans
	}
	payload := make([]byte, 0, 2+n*effectSpanBytes)
	payload = append(payload, effectsMarkerMagic, byte(n))
	for i := 0; i < n; i++ {
		sp := spans[i]
		st := sp.Start
		if st > maxEffectStart {
			st = maxEffectStart
		}
		ln := sp.Len
		if ln > maxEffectLen {
			ln = maxEffectLen
		}
		payload = append(payload, sp.Effect, byte(st>>8), byte(st), byte(ln))
	}
	return packZeroWidth(payload)
}

// DecodeEffectsMarker pulls the effect spans out of a message's text, if a well-formed
// effects frame is present. A short/corrupt/empty frame yields (nil, false) — benign, the
// message just renders un-animated. Only the spans with a known effect and a non-zero
// length survive.
func DecodeEffectsMarker(text string) ([]TextEffectSpan, bool) {
	if !hasMarker(text) {
		return nil, false
	}
	for _, fr := range scanZeroWidthFrames(text) {
		if len(fr) < 2 || fr[0] != effectsMarkerMagic {
			continue
		}
		n := int(fr[1])
		if n == 0 || n > maxEffectSpans || len(fr) < 2+n*effectSpanBytes {
			return nil, false
		}
		spans := make([]TextEffectSpan, 0, n)
		for i := 0; i < n; i++ {
			off := 2 + i*effectSpanBytes
			eff := fr[off]
			ln := int(fr[off+3])
			if eff == TextEffectNone || eff >= TextEffectCount || ln == 0 {
				continue // unknown effect or empty span → drop it
			}
			spans = append(spans, TextEffectSpan{
				Start:  int(fr[off+1])<<8 | int(fr[off+2]),
				Len:    ln,
				Effect: eff,
			})
		}
		if len(spans) == 0 {
			return nil, false
		}
		return spans, true
	}
	return nil, false
}
