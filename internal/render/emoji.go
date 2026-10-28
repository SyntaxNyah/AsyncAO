package render

// Per-glyph emoji font fallback. SDL_ttf renders one face at a time and does no
// font substitution, so a message mixing text and emoji ("hi 😀") needs the runes
// split into runs — text runes drawn from the chat font, emoji runs from a color
// emoji face — and laid out side by side. This file holds the PURE routing: which
// runes belong to the emoji font. The rasterizer (text.go) turns runs into
// textures; the UI owns loading the emoji face.
// Routing is codepoint-based (no font metrics), so it's allocation-light and
// table-tested. The base signal is the supplementary plane (U+10000+) where the
// bulk of emoji, skin-tone modifiers and regional-indicator flags live; on top of
// that the BMP joiners/selectors that build COMPOUND emoji are absorbed into the
// adjacent emoji run so sequences render whole instead of fragmenting back to the
// text font mid-emoji.

import (
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

const (
	runeVS15   rune = 0xFE0E // variation selector-15: force TEXT style (stays on the text font)
	runeVS16   rune = 0xFE0F // variation selector-16: force EMOJI style (promotes the preceding BMP char)
	runeZWJ    rune = 0x200D // zero-width joiner: builds multi-person / family emoji
	runeKeycap rune = 0x20E3 // combining enclosing keycap: 1️⃣ #️⃣ etc.
)

// UTF-8 and Unicode boundaries the emoji routing tests against.
const (
	utf8FourByteLead   = 0xF0   // first byte of a 4-byte (astral plane) sequence
	utf8LeadU2000      = 0xE2   // first byte of U+2000..U+2FFF, where every BMP emoji-by-default lives
	firstBMPPictograph = 0x00A9 // the copyright sign, the lowest BMP pictograph
)

// needsEmojiFallback reports whether s contains anything the text font can't
// render as the user expects — so only these messages take the per-glyph fallback
// path. A tight byte scan that decodes only the 0xE2 sequences where the BMP
// emoji-by-default characters live, no allocation, so an ASCII message (the
// overwhelming common case) pays almost nothing and stays on the single-font fast
// path. Three signals: a 4-byte (supplementary-plane) sequence, VS16, and a BMP
// character drawn as emoji by default (bmpEmojiPresentation).
func needsEmojiFallback(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= utf8FourByteLead {
			return true
		}
		if b == 0xEF && i+2 < len(s) && s[i+1] == 0xB8 && s[i+2] == 0x8F {
			return true
		}
		if b == utf8LeadU2000 {
			if r, _ := utf8.DecodeRuneInString(s[i:]); bmpEmojiPresentation(r) {
				return true
			}
		}
	}
	return false
}

// NeedsEmojiFallback reports whether text contains emoji the chat font can't
// render, so the UI should take the per-glyph RasterizeFallback path instead of
// the single-font fast path. Exported gate over the cheap byte scan.
func NeedsEmojiFallback(s string) bool { return needsEmojiFallback(s) }

// emojiBlockLo / emojiBlockHi bound the supplementary-plane range where emoji
// actually live: U+1F000 (Mahjong Tiles) through U+1FAFF (Symbols and Pictographs
// Extended-A). That span holds the pictographs, the U+1F1E6.. regional indicators
// and the U+1F3FB.. skin-tone modifiers. Nothing outside it in the supplementary
// planes is emoji.
const (
	emojiBlockLo rune = 0x1F000
	emojiBlockHi rune = 0x1FAFF
)

// isEmojiBase reports a rune that on its own selects an emoji glyph: the
// supplementary emoji blocks, and the BMP characters Unicode presents as emoji by
// default. VS15 forces text presentation, so it never counts as a base.
func isEmojiBase(r rune) bool {
	return (r >= emojiBlockLo && r <= emojiBlockHi && r != runeVS15) || bmpEmojiPresentation(r)
}

// bmpEmojiPresentationRanges are the BMP characters with Emoji_Presentation=Yes
// (Unicode emoji-data.txt): drawn as colour emoji with no variation selector, the
// way Qt, DirectWrite and every browser draw ⭐ ✨ ⌛ ✅ ❌ ⚡ ☕. The other BMP
// pictographs (♥ ★ ☀ ✓) default to text and turn into emoji only with VS16.
var bmpEmojiPresentationRanges = [...][2]rune{
	{0x231A, 0x231B}, // ⌚ ⌛
	{0x23E9, 0x23EC}, // ⏩ ⏪ ⏫ ⏬
	{0x23F0, 0x23F0}, // ⏰
	{0x23F3, 0x23F3}, // ⏳
	{0x25FD, 0x25FE}, // ◽ ◾
	{0x2614, 0x2615}, // ☔ ☕
	{0x2648, 0x2653}, // ♈ … ♓
	{0x267F, 0x267F}, // ♿
	{0x2693, 0x2693}, // ⚓
	{0x26A1, 0x26A1}, // ⚡
	{0x26AA, 0x26AB}, // ⚪ ⚫
	{0x26BD, 0x26BE}, // ⚽ ⚾
	{0x26C4, 0x26C5}, // ⛄ ⛅
	{0x26CE, 0x26CE}, // ⛎
	{0x26D4, 0x26D4}, // ⛔
	{0x26EA, 0x26EA}, // ⛪
	{0x26F2, 0x26F3}, // ⛲ ⛳
	{0x26F5, 0x26F5}, // ⛵
	{0x26FA, 0x26FA}, // ⛺
	{0x26FD, 0x26FD}, // ⛽
	{0x2705, 0x2705}, // ✅
	{0x270A, 0x270B}, // ✊ ✋
	{0x2728, 0x2728}, // ✨
	{0x274C, 0x274C}, // ❌
	{0x274E, 0x274E}, // ❎
	{0x2753, 0x2755}, // ❓ ❔ ❕
	{0x2757, 0x2757}, // ❗
	{0x2795, 0x2797}, // ➕ ➖ ➗
	{0x27B0, 0x27B0}, // ➰
	{0x27BF, 0x27BF}, // ➿
	{0x2B1B, 0x2B1C}, // ⬛ ⬜
	{0x2B50, 0x2B50}, // ⭐
	{0x2B55, 0x2B55}, // ⭕
}

// bmpEmojiPresentation reports whether r is a BMP character drawn as emoji by
// default (bmpEmojiPresentationRanges). A short scan of a fixed array, no
// allocation.
func bmpEmojiPresentation(r rune) bool {
	if r < bmpEmojiPresentationRanges[0][0] || r > bmpEmojiPresentationRanges[len(bmpEmojiPresentationRanges)-1][1] {
		return false
	}
	for _, b := range bmpEmojiPresentationRanges {
		if r >= b[0] && r <= b[1] {
			return true
		}
	}
	return false
}

// bmpPictographBlocks are the BASIC MULTILINGUAL PLANE blocks whose characters
// have an emoji presentation and are routinely absent from ordinary text faces:
// hearts, stars, sparkles, dingbats, the weather/zodiac symbols, arrows and the
// clock/technical pictographs. They are the F5 gap. Unlike the supplementary-plane
// emoji isEmojiBase routes, these are plain BMP codepoints — a bare ♥ (U+2665), ✨
// (U+2728) or ⭐ (U+2B50) carries no VS16 and no 4-byte lead, so needsEmojiFallback
// says "no emoji here", nothing ever asks for the colour face, and the character
// draws as the text font's .notdef box. That is exactly what the field report
// described: hearts and sparkles as boxes while CJK and Greek — which the TEXT
// tiers and the system-font census answer — came out fine. The v1.87 Unicode census
// covered SCRIPTS; symbol blocks were never in it.
// Ranges, not a rune set, because these are contiguous Unicode blocks; a sorted
// fixed array so the lookup is a short scan with no map and no allocation.
var bmpPictographBlocks = [...][2]rune{
	{0x2190, 0x21FF}, // Arrows — ↔ ↕ ↩ ↪ (emoji presentation forms)
	{0x2300, 0x23FF}, // Miscellaneous Technical — ⌚ ⌛ ⏰ ⏳ ⏩ ⏪
	{0x25A0, 0x25FF}, // Geometric Shapes — ▪ ▫ ◻ ◼ ◽ ◾
	{0x2600, 0x27BF}, // Misc Symbols + Dingbats — ☀ ☺ ♥ ★ ✨ ✅ ✂ ➡
	{0x2900, 0x297F}, // Supplemental Arrows-B — ⤴ ⤵
	{0x2B00, 0x2BFF}, // Misc Symbols and Arrows — ⭐ ⭕ ⬅ ⬆ ⬇
}

// bmpPictographRunes are the strays that sit OUTSIDE any of the blocks above but
// share their problem: an emoji presentation on a codepoint most text faces treat
// as ordinary punctuation (or don't have at all). Listed individually rather than
// widening a block, so the table never swallows the letters and digits around them.
var bmpPictographRunes = [...]rune{
	0x00A9, // © copyright
	0x00AE, // ® registered
	0x2049, // ⁉ exclamation question mark
	0x203C, // ‼ double exclamation mark
	0x2122, // ™ trade mark
	0x2139, // ℹ information source
	0x24C2, // Ⓜ circled M
	0x3030, // 〰 wavy dash
	0x303D, // 〽 part alternation mark
	0x3297, // ㊗ circled congratulation
	0x3299, // ㊙ circled secret
}

// BMPPictograph reports whether r is a BMP character with an emoji presentation —
// i.e. one the COLOUR-EMOJI face can draw when no text face on the machine can.
// See bmpPictographBlocks: this is an eligibility test, never a routing decision on
// its own. Pure; ASCII short-circuits, so a plain line pays one compare per rune.
func BMPPictograph(r rune) bool {
	if r < firstBMPPictograph {
		return false // ASCII and Latin-1 punctuation: never a pictograph
	}
	for _, b := range bmpPictographBlocks {
		if r >= b[0] && r <= b[1] {
			return true
		}
	}
	for _, p := range bmpPictographRunes {
		if r == p {
			return true
		}
	}
	return false
}

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
	// VS15 forces text presentation: the character before it stays on the text font
	// even when it is an emoji by default (⭐︎ draws as the text star).
	for i, r := range runes {
		if r == runeVS15 && i > 0 {
			out[i-1] = false
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

// AssignEmoji marks which runes render from the colour-emoji face (true) vs the
// text font (false). Exported wrapper over assignEmoji so the animated (#M5
// effects) path in package ui resolves its per-rune emoji mask through the SAME
// routing as the plain RasterizeFallback path — keeping an effects line's emoji
// runs byte-identical to a plain line's. Build/layout time only.
func AssignEmoji(runes []rune) []bool { return assignEmoji(runes) }

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
		if h := FontLineSpacing(f); h > lineH {
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
// with one font). A nil face contributes nothing: RasterizeFallback never builds
// one (it would fail at buildSpan), but a headless caller measuring a set whose
// last-resort face is nil must not panic.
func measureRuns(fonts []*ttf.Font, runes []rune, lo, hi int) int32 {
	var w int32
	for i := lo; i < hi; {
		j := i + 1
		for j < hi && fonts[j] == fonts[i] {
			j++
		}
		if fonts[i] != nil {
			ww, _, _ := fonts[i].SizeUTF8(string(runes[i:j]))
			w += int32(ww)
		}
		i = j
	}
	return w
}

// MeasureFallbackWidth is the pixel width of runes laid out EXACTLY the way
// RasterizeFallback lays them out: emoji runes (assignEmoji) on the colour-emoji
// face, every other rune on its own covering text face, summed run by run. It is
// the measuring twin of the per-glyph draw, so a caller that wraps text which
// will land on that path can break lines against the widths it will really get
// (#42 — the OOC/IC log wrap). textFonts is per-rune (the ui's coverRunes);
// emoji may be nil (no colour face yet), which routes every rune to its text
// face, exactly as RasterizeFallback degrades. Build-time only.
func MeasureFallbackWidth(textFonts []*ttf.Font, emoji *ttf.Font, runes []rune) int32 {
	if len(runes) == 0 || len(textFonts) < len(runes) {
		return 0
	}
	mask := assignEmoji(runes)
	fonts := make([]*ttf.Font, len(runes))
	for i := range runes {
		if mask[i] && emoji != nil {
			fonts[i] = emoji
		} else {
			fonts[i] = textFonts[i]
		}
	}
	return measureRuns(fonts, runes, 0, len(runes))
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

// WrapMeasured word-wraps text to maxW px asking measure for the width of each
// candidate line — the caller answers with the width that line will have once
// DRAWN, whatever face(s) the draw resolves for it. WrapEmojiAware can only
// answer that for one fixed primary face: under a multi-face chain (a custom
// font plus the embedded last resort) a paragraph containing one rune the custom
// font lacks picks the fallback face for the WHOLE paragraph, while the draw
// picks per row and puts the covered rows back in the wider custom face — they
// then overflowed the column (#42, the half of it the v1.81.2 wrapToWidth fix
// didn't reach). Same greedy line breaker as WrapEmojiAware (break at the last
// space, else cut at a rune boundary), so only the metric changes.
// Cost: one measure per rune step, each handed a fresh candidate string — the
// same order as WrapEmojiAware's per-step measureRuns, and build-time only (the
// log caches its wrap), never the render loop. maxLines 0 = uncapped.
func WrapMeasured(measure func(string) int32, text string, maxW int32, maxLines int) []string {
	runes := []rune(text)
	if len(runes) == 0 || measure == nil {
		return nil
	}
	ranges := wrapMeasured(func(lo, hi int) int32 { return measure(string(runes[lo:hi])) }, runes, maxW)
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
	return wrapMeasured(func(lo, hi int) int32 { return measureRuns(fonts, runes, lo, hi) }, runes, maxW)
}

// wrapMeasured is the shared greedy line breaker: measure(lo, hi) reports the
// pixel width of runes[lo:hi]. Splitting it out lets the per-rune-font wrap
// (wrapMultiFont) and the caller-measured wrap (WrapMeasured) share one set of
// break rules, so the two can never drift apart.
func wrapMeasured(measure func(lo, hi int) int32, runes []rune, maxW int32) []lineRange {
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
		if measure(lineStart, i+1) > maxW && i > lineStart {
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
