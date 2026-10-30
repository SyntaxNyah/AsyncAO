package render

// Emoji routing: SDL_ttf has no font substitution, so mixed text splits into runs by codepoint.

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

const (
	utf8FourByteLead   = 0xF0   // first byte of a 4-byte (astral plane) sequence
	utf8LeadU2000      = 0xE2   // first byte of U+2000..U+2FFF, where every BMP emoji-by-default lives
	firstBMPPictograph = 0x00A9 // the copyright sign, the lowest BMP pictograph
)

// needsEmojiFallback reports whether s needs the per-glyph path: a 4-byte sequence, VS16 or a BMP default emoji.
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

// NeedsEmojiFallback reports whether text needs the per-glyph RasterizeFallback path.
func NeedsEmojiFallback(s string) bool { return needsEmojiFallback(s) }

// emojiBlockLo/Hi bound supplementary-plane emoji, including regional indicators and skin-tone modifiers.
const (
	emojiBlockLo rune = 0x1F000
	emojiBlockHi rune = 0x1FAFF
)

// isEmojiBase reports a rune that selects an emoji glyph alone; VS15 forces text presentation, so it never counts.
func isEmojiBase(r rune) bool {
	return (r >= emojiBlockLo && r <= emojiBlockHi && r != runeVS15) || bmpEmojiPresentation(r)
}

// bmpEmojiPresentationRanges are BMP Emoji_Presentation=Yes (emoji-data.txt); other pictographs need VS16.
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

// bmpPictographBlocks are BMP emoji blocks text faces often lack; with no VS16 they would draw as .notdef.
var bmpPictographBlocks = [...][2]rune{
	{0x2190, 0x21FF}, // Arrows — ↔ ↕ ↩ ↪ (emoji presentation forms)
	{0x2300, 0x23FF}, // Miscellaneous Technical — ⌚ ⌛ ⏰ ⏳ ⏩ ⏪
	{0x25A0, 0x25FF}, // Geometric Shapes — ▪ ▫ ◻ ◼ ◽ ◾
	{0x2600, 0x27BF}, // Misc Symbols + Dingbats — ☀ ☺ ♥ ★ ✨ ✅ ✂ ➡
	{0x2900, 0x297F}, // Supplemental Arrows-B — ⤴ ⤵
	{0x2B00, 0x2BFF}, // Misc Symbols and Arrows — ⭐ ⭕ ⬅ ⬆ ⬇
}

// bmpPictographRunes are strays outside those blocks, listed singly so nearby letters and digits stay out.
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

// BMPPictograph reports whether the colour face can draw r when no text face can; eligibility, not routing.
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

// assignEmoji marks emoji-font runes: supplementary plane, VS16 and base, keycaps and base, ZWJ next to an emoji.
func assignEmoji(runes []rune) []bool {
	out := make([]bool, len(runes))
	for i, r := range runes {
		if isEmojiBase(r) {
			out[i] = true
		}
	}
	// VS15 keeps the character before it on the text font, even an emoji by default.
	for i, r := range runes {
		if r == runeVS15 && i > 0 {
			out[i-1] = false
		}
	}
	for i, r := range runes {
		if r == runeVS16 {
			out[i] = true
			if i > 0 {
				out[i-1] = true
			}
		}
	}
	// Keycap: <base> [VS16] U+20E3 all render from the emoji font.
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
	// Absorb a ZWJ next to emoji so a sequence is one run, not emoji split by text-font joiners.
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

// AssignEmoji exposes assignEmoji so the animated path routes emoji the same way as RasterizeFallback.
func AssignEmoji(runes []rune) []bool { return assignEmoji(runes) }

// RasterizeFallback renders text and emoji as baseline-aligned spans; a nil emoji face uses the primary font.
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
	// textFonts is per-rune, so a mixed-script run no single face covers still renders.
	fonts := make([]*ttf.Font, len(runes))
	for i := range runes {
		if mask[i] && emoji != nil {
			fonts[i] = emoji
		} else {
			fonts[i] = textFonts[i]
		}
	}
	// Height and ascent over the faces used, as a CJK or emoji face is taller than the embedded one.
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

// rasterizeFallbackLine builds a line's spans, starting a new one when font or style changes.
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

// measureRuns sums SizeUTF8 per same-font sub-run; a nil face adds nothing, so a headless caller can't panic.
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

// MeasureFallbackWidth is the width RasterizeFallback would give, so callers wrap against real widths.
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

// WrapEmojiAware measures emoji with the emoji font, as the primary sizes them narrow and they overflow; 0 = uncapped.
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

// WrapMeasured wraps using the caller's measure of each line, for per-row face chains; maxLines 0 = uncapped.
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

// wrapMultiFont word-wraps runes to maxW under per-rune fonts; build-time, fallback path only.
func wrapMultiFont(fonts []*ttf.Font, runes []rune, maxW int32) []lineRange {
	return wrapMeasured(func(lo, hi int) int32 { return measureRuns(fonts, runes, lo, hi) }, runes, maxW)
}

// wrapMeasured is the shared greedy line breaker; measure(lo, hi) reports the width of runes[lo:hi].
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
