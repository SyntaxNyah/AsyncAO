package render

// Per-glyph emoji font fallback. SDL_ttf renders one face at a time and does no
// font substitution, so a message mixing text and emoji ("hi 😀") needs the runes
// split into runs — text runes drawn from the chat font, emoji runs from a color
// emoji face — and laid out side by side. This file holds the PURE routing: which
// runes belong to the emoji font. The rasterizer (text.go) turns runs into
// textures; the UI owns loading the emoji face.
//
// Routing is codepoint-based (no font metrics), so it's allocation-light and
// table-tested. The base signal is the supplementary plane (U+10000+) where the
// bulk of emoji, skin-tone modifiers and regional-indicator flags live; on top of
// that the BMP joiners/selectors that build COMPOUND emoji are absorbed into the
// adjacent emoji run so sequences render whole instead of fragmenting back to the
// text font mid-emoji.

import (
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

const (
	runeVS15   rune = 0xFE0E // variation selector-15: force TEXT style (stays on the text font)
	runeVS16   rune = 0xFE0F // variation selector-16: force EMOJI style (promotes the preceding BMP char)
	runeZWJ    rune = 0x200D // zero-width joiner: builds multi-person / family emoji
	runeKeycap rune = 0x20E3 // combining enclosing keycap: 1️⃣ #️⃣ etc.
)

// needsEmojiFallback reports whether s contains anything the text font can't
// render as the user expects — so only these messages take the per-glyph fallback
// path. A tight byte scan, no rune decoding, no allocation, so an all-BMP/ASCII
// message (the overwhelming common case) pays almost nothing and stays on the
// single-font fast path. Two signals:
//   - a 4-byte UTF-8 sequence (lead byte >= 0xF0) = a supplementary-plane rune
//     (U+10000+), where most emoji, skin tones and flags live;
//   - VS16 (U+FE0F = EF B8 8F), which promotes a BMP char to a color emoji
//     (❤️ = U+2764 U+FE0F, ✌️, ☺️ …) — these have NO supplementary byte, so the
//     first signal alone would miss them.
func needsEmojiFallback(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 0xF0 {
			return true
		}
		if b == 0xEF && i+2 < len(s) && s[i+1] == 0xB8 && s[i+2] == 0x8F {
			return true
		}
	}
	return false
}

// NeedsEmojiFallback reports whether text contains emoji the chat font can't
// render, so the UI should take the per-glyph RasterizeFallback path instead of
// the single-font fast path. Exported gate over the cheap byte scan.
func NeedsEmojiFallback(s string) bool { return needsEmojiFallback(s) }

// isEmojiBase reports a rune that on its own selects an emoji glyph: the
// supplementary plane (pictographs, U+1F3FB.. skin tones, U+1F1E6.. regional
// indicators). VS15 forces text presentation, so it never counts as a base.
func isEmojiBase(r rune) bool { return r > 0xFFFF && r != runeVS15 }

// assignEmoji marks which runes render from the EMOJI font (true) vs the text font
// (false). Supplementary-plane runes are emoji; VS16 promotes the BMP char before
// it (so ❤️ = U+2764 U+FE0F renders as a color heart); a keycap pulls in its base
// (and any VS16); and a ZWJ adjacent to an emoji is absorbed so ZWJ sequences
// (families, professions) stay one run. Pure — table-tested.
func assignEmoji(runes []rune) []bool {
	out := make([]bool, len(runes))
	for i, r := range runes {
		if isEmojiBase(r) {
			out[i] = true
		}
	}
	// VS16 is emoji and promotes the immediately-preceding base into the emoji run.
	for i, r := range runes {
		if r == runeVS16 {
			out[i] = true
			if i > 0 {
				out[i-1] = true
			}
		}
	}
	// Keycap: <base> [VS16] U+20E3 — keycap, its optional VS16, and the base all
	// render from the emoji font.
	for i, r := range runes {
		if r != runeKeycap {
			continue
		}
		out[i] = true
		j := i - 1
		if j >= 0 && runes[j] == runeVS16 {
			out[j] = true
			j--
		}
		if j >= 0 {
			out[j] = true // the keycap base (digit / # / *)
		}
	}
	// ZWJ between/next to emoji joins them: absorb it (and keep VS16 selectors
	// inside the run) so a family/profession sequence is one emoji run, not three
	// emoji split by text-font joiners.
	for i, r := range runes {
		if r != runeZWJ {
			continue
		}
		leftEmoji := i > 0 && out[i-1]
		rightEmoji := i+1 < len(runes) && (out[i+1] || isEmojiBase(runes[i+1]))
		if leftEmoji || rightEmoji {
			out[i] = true
		}
	}
	return out
}

// RasterizeFallback renders a message mixing text and emoji: each rune routes to
// the primary font or the emoji face (assignEmoji), maximal same-font same-style
// runs become spans laid out left to right, and runs are baseline-aligned (the two
// faces differ in ascent). It reuses the styled-span structure and its zero-alloc
// Draw — only the once-per-message BUILD does extra work, and the UI gates this on
// needsEmojiFallback so plain messages never reach here. emoji may be nil (face
// not loaded / unavailable): every run then uses the primary font, degrading to
// today's behaviour rather than failing.
// devScale (#77) is the device font scale the caller opened the faces at (100 =
// 1:1); wrapW is LOGICAL px and scales up to measure the wrap against the device
// glyphs, and Draw divides the device dst back to logical. Pass
// render.DefaultDevScale (100) for the pre-#77 behavior / exports.
func RasterizeFallback(ren *sdl.Renderer, textFonts []*ttf.Font, emoji *ttf.Font, text string, spans []ColorSpan, wrapW int32, devScale int32) (*MessageRaster, error) {
	if devScale <= 0 {
		devScale = DefaultDevScale
	}
	wrapW = wrapW * devScale / DefaultDevScale // measure the wrap against the DEVICE glyphs (see text.Rasterize)
	runes := []rune(text)
	m := &MessageRaster{text: text, styled: [][]rasterSpan{}, devScale: devScale}
	if len(runes) == 0 {
		return m, nil
	}
	mask := assignEmoji(runes)
	styles := perRuneStyles(runes, spans)
	// Per rune: the colour-emoji face for emoji runes, else this rune's covering text
	// face (textFonts is per-rune — the caller routes each glyph to a face that has it,
	// so a mixed-script run no single face covers still renders). For a pure-emoji or
	// single-script message textFonts is uniform, reproducing the old behaviour.
	fonts := make([]*ttf.Font, len(runes))
	for i := range runes {
		if mask[i] && emoji != nil {
			fonts[i] = emoji
		} else {
			fonts[i] = textFonts[i]
		}
	}
	// lineH / ascent over the faces ACTUALLY used (a CJK or emoji face is taller than
	// the embedded one); the per-run baseline-align below handles the differing ascents.
	var lineH, ascent int32
	for _, f := range fonts {
		if f == nil {
			continue
		}
		if h := int32(f.Height()); h > lineH {
			lineH = h
		}
		if a := int32(f.Ascent()); a > ascent {
			ascent = a
		}
	}
	m.lineH = lineH
	for _, lr := range wrapMultiFont(fonts, runes, wrapW) {
		line, err := rasterizeFallbackLine(ren, runes[lr.start:lr.end], fonts[lr.start:lr.end], styles[lr.start:lr.end], ascent)
		if err != nil {
			m.Destroy()
			return nil, err
		}
		m.styled = append(m.styled, line)
		m.lineRanges = append(m.lineRanges, lr)
	}
	return m, nil
}

// rasterizeFallbackLine builds one wrapped line's spans: a new span whenever the
// font OR the colour/style changes, each baseline-aligned to the line ascent.
func rasterizeFallbackLine(ren *sdl.Renderer, runes []rune, fonts []*ttf.Font, styles []spanStyle, ascent int32) ([]rasterSpan, error) {
	var spans []rasterSpan
	var x int32
	for i := 0; i < len(runes); {
		j := i + 1
		for j < len(runes) && fonts[j] == fonts[i] && styles[j] == styles[i] {
			j++
		}
		yOff := ascent - int32(fonts[i].Ascent())
		sp, err := buildSpan(ren, fonts[i], runes[i:j], styles[i], x, yOff)
		if err != nil {
			for k := range spans {
				if spans[k].tex != nil {
					_ = spans[k].tex.Destroy()
				}
			}
			return nil, err
		}
		spans = append(spans, sp)
		x += sp.w
		i = j
	}
	return spans, nil
}

// measureRuns is the pixel width of runes[lo:hi] under per-rune fonts — summing
// each maximal same-font sub-run's SizeUTF8 (a mixed-face line can't be measured
// with one font).
func measureRuns(fonts []*ttf.Font, runes []rune, lo, hi int) int32 {
	var w int32
	for i := lo; i < hi; {
		j := i + 1
		for j < hi && fonts[j] == fonts[i] {
			j++
		}
		ww, _, _ := fonts[i].SizeUTF8(string(runes[i:j]))
		w += int32(ww)
		i = j
	}
	return w
}

// WrapEmojiAware word-wraps text to maxW pixels, measuring emoji runes with the EMOJI
// font — the primary font sizes colour emoji as narrow tofu, so a line of wide emoji
// (a heart-laden showname) would otherwise never break and overflow the column. Returns
// the wrapped lines, capped at maxLines (0 = uncapped). emoji may be nil → a plain
// single-font wrap. Used by the IC/OOC log, whose rows render through this same
// per-rune fallback, so the wrap must measure the way the draw will. Build-time only
// (the log caches its wrap), so the per-rune allocation never touches the render loop.
func WrapEmojiAware(primary, emoji *ttf.Font, text string, maxW int32, maxLines int) []string {
	runes := []rune(text)
	if len(runes) == 0 || primary == nil {
		return nil
	}
	fonts := make([]*ttf.Font, len(runes))
	mask := assignEmoji(runes)
	for i := range runes {
		if mask[i] && emoji != nil {
			fonts[i] = emoji
		} else {
			fonts[i] = primary
		}
	}
	ranges := wrapMultiFont(fonts, runes, maxW)
	if maxLines > 0 && len(ranges) > maxLines {
		ranges = ranges[:maxLines]
	}
	out := make([]string, 0, len(ranges))
	for _, lr := range ranges {
		out = append(out, string(runes[lr.start:lr.end]))
	}
	return out
}

// wrapMultiFont greedily word-wraps runes to maxW px under per-rune fonts (mirrors
// wrapStyled's visuals, but measures mixed-face prefixes). Build-time, fallback
// path only.
func wrapMultiFont(fonts []*ttf.Font, runes []rune, maxW int32) []lineRange {
	n := len(runes)
	if maxW <= 0 {
		return []lineRange{{0, n}}
	}
	var out []lineRange
	lineStart, lastSpace := 0, -1
	for i := 0; i < n; {
		if runes[i] == '\n' {
			out = append(out, lineRange{lineStart, i})
			i++
			lineStart, lastSpace = i, -1
			continue
		}
		if runes[i] == ' ' {
			lastSpace = i
		}
		if measureRuns(fonts, runes, lineStart, i+1) > maxW && i > lineStart {
			if lastSpace > lineStart {
				out = append(out, lineRange{lineStart, lastSpace})
				lineStart = lastSpace + 1
			} else {
				out = append(out, lineRange{lineStart, i})
				lineStart = i
			}
			lastSpace = -1
			continue
		}
		i++
	}
	out = append(out, lineRange{lineStart, n})
	return out
}
