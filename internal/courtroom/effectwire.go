package courtroom

import (
	"encoding/binary"
	"strings"
)

// Animated text spans ride the zero-width channel on every message with an effect, as per-message content.

// Text effect ids must match render.Effect*; courtroom can't import render, so the values are duplicated.
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
	// TextEffectCount must stay last: the decoder drops ids >= it, so a newer effect is plain text on an older client.
	TextEffectCount
)

// TextEffectSpan tags Len runes from Start, an index into the clean text (after zero-width and \cN strips).
type TextEffectSpan struct {
	Start  int
	Len    int
	Effect uint8
}

const (
	// effectsMarkerMagic is the frame's first byte, distinct from sprite style (1), profile (0x70) and status (0x71).
	effectsMarkerMagic = 0x72

	// maxEffectSpans bounds a message's spans (hard rule 4), as other clients blip on each invisible rune.
	maxEffectSpans = 8

	// effectSpanBytes is the per-span wire size: effect(1) + start(2, big-endian) + len(1).
	effectSpanBytes = 4

	// Field ceilings: start is two bytes, len is one.
	maxEffectStart = 0xFFFF
	maxEffectLen   = 0xFF

	// maxTagLen stops a stray '[' from making the parser quadratic; the longest real tag is "[rainbow]".
	maxTagLen = 12
)

// effectTags uses square brackets, as AO's { } set typewriter speed and would be eaten by the pipeline.
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

// ParseTextEffects splits raw input into wire text and spans; StripChatMarkup keeps brackets, so spans stay aligned.
// One accepted hole: chat markup inside a bracket tag ("[sh~ake]") can misalign or re-form a tag.
func ParseTextEffects(raw string) (wire string, spans []TextEffectSpan) {
	if !strings.ContainsRune(raw, '[') {
		return raw, nil // no possible tag → nothing to strip or index (0-alloc common case)
	}
	wire, _ = parseEffectTags(raw)                   // drop tags, keep \cN etc. for the wire
	_, spans = parseEffectTags(StripChatMarkup(raw)) // spans over the receiver's display text
	return wire, spans
}

// parseEffectTags removes [tag] runs; unknown brackets stay literal, and a new tag closes the previous (no nesting).
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

// matchTag reads "[name]" or "[/name]" at rs[i], returning the lower-case name, closing flag, length and found flag.
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

// appendEffectSpan adds a [start,end) span, dropping empty ones and applying the per-message cap and field ceilings.
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

// EncodeEffectsMarker returns the invisible run for these spans, "" when there are none.
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
		payload = append(payload, sp.Effect)
		payload = binary.BigEndian.AppendUint16(payload, uint16(st))
		payload = append(payload, byte(ln))
	}
	return packZeroWidth(payload)
}

// DecodeEffectsMarker returns known non-empty spans; a corrupt or empty frame yields (nil, false).
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
				Start:  int(binary.BigEndian.Uint16(fr[off+1:])),
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
