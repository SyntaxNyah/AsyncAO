package render

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// AO chat text colors (TEXT_COLOR field), AO2-Client palette.
var textColors = []sdl.Color{
	{R: 255, G: 255, B: 255, A: 255}, // 0 white
	{R: 0, G: 255, B: 0, A: 255},     // 1 green
	{R: 255, G: 0, B: 0, A: 255},     // 2 red
	{R: 255, G: 165, B: 0, A: 255},   // 3 orange
	{R: 45, G: 150, B: 255, A: 255},  // 4 blue
	{R: 255, G: 255, B: 0, A: 255},   // 5 yellow
	{R: 255, G: 192, B: 203, A: 255}, // 6 pink (legacy)
	{R: 0, G: 255, B: 255, A: 255},   // 7 cyan
	{R: 192, G: 192, B: 192, A: 255}, // 8 gray
}

// TextColorCount is the number of AO chat colors the palette defines —
// the IC color cycler wraps at this (MS text_color wire values 0..N-1).
var TextColorCount = len(textColors)

// TextColor maps an AO color index to RGBA (out of range → white).
func TextColor(index int) sdl.Color {
	if index < 0 || index >= len(textColors) {
		return textColors[0]
	}
	return textColors[index]
}

// textColorNames parallels textColors: the labels the IC color dropdown
// shows (AO2-Client's set_text_color_dropdown lists names, not swatches).
var textColorNames = []string{
	"White", "Green", "Red", "Orange", "Blue", "Yellow", "Pink", "Cyan", "Gray",
}

// TextColorNames exposes the dropdown option list. Callers treat it as
// read-only (shared backing array).
func TextColorNames() []string { return textColorNames }

// Extended AsyncAO chat colors (#98). These are NOT wire text_color values —
// the AO MS text_color field MUST stay 0..8 or strict clients (LemmyAO-style)
// drop the whole message. They travel ONLY as inline \c<Code> markup, exactly
// like the \cr rainbow: another AsyncAO client renders Color precisely, while a
// standard AO2 client drops the unknown escape and falls back to Wire (the
// nearest standard palette index we ship in the MS text_color field). Codes
// avoid the reserved markup letters r/b/i and the \c lead-in 'c', plus digits
// 0..8 — the gate set is mirrored by courtroom.ExtColorCodes (a test pins it).
type ExtColor struct {
	Name  string
	Code  byte      // inline letter: \c<Code>
	Color sdl.Color // exact render color
	Wire  int       // nearest standard palette index (0..8) for non-AsyncAO clients
}

var extColors = []ExtColor{
	{"Purple", 'p', sdl.Color{R: 150, G: 90, B: 245, A: 255}, 4},
	{"Magenta", 'm', sdl.Color{R: 255, G: 70, B: 230, A: 255}, 6},
	{"Teal", 't', sdl.Color{R: 40, G: 200, B: 190, A: 255}, 7},
	{"Lime", 'l', sdl.Color{R: 170, G: 240, B: 70, A: 255}, 1},
	{"Gold", 'g', sdl.Color{R: 255, G: 200, B: 40, A: 255}, 5},
	{"Coral", 'o', sdl.Color{R: 255, G: 120, B: 90, A: 255}, 3},
	{"Sky", 'k', sdl.Color{R: 110, G: 200, B: 255, A: 255}, 4},
	{"Lavender", 'v', sdl.Color{R: 205, G: 170, B: 255, A: 255}, 4},
}

// ExtColorCount / ExtColorAt expose the extended palette (read-only) to the IC
// colour dropdown and the send path.
func ExtColorCount() int        { return len(extColors) }
func ExtColorAt(i int) ExtColor { return extColors[i] }

// ExtColorByCode resolves an inline \c<code> letter to its render color; ok is
// false for an unknown code, and the caller renders the message default.
func ExtColorByCode(code byte) (sdl.Color, bool) {
	for i := range extColors {
		if extColors[i].Code == code {
			return extColors[i].Color, true
		}
	}
	return textColors[0], false
}

// rasterLine is one wrapped line: a texture plus per-rune prefix advances so
// the reveal is a pure src-rect width (spec §12: no per-character
// layout, no texture churn).
type rasterLine struct {
	tex      *sdl.Texture
	w, h     int32
	runes    int
	advances []int32 // advances[i] = pixel width of the first i runes
}

// rasterSpan is one same-color run within a wrapped line (the multi-color
// sibling of rasterLine): its own texture, prefix advances, and the x offset
// where it sits on the line. Built only for styled messages.
type rasterSpan struct {
	tex      *sdl.Texture
	w, h     int32
	runes    int
	xOffset  int32
	yOff     int32 // baseline offset within the line (0 for same-font color spans; >0 aligns a shorter-ascent font in the emoji fallback)
	advances []int32
}

// MessageRaster pre-rasterizes a full message once; Draw reveals it by rune
// count with zero allocations per frame. A message is EITHER single-color
// (lines, from Rasterize) OR multi-color (styled, from RasterizeStyled) — the
// single-color path is untouched so plain messages can't regress.
type MessageRaster struct {
	lines  []rasterLine
	styled [][]rasterSpan // nil unless this is a multi-color message
	lineH  int32
	text   string
	// centerOff[i] horizontally centers line i within the chatbox width (webAO's
	// "~~" prefix). nil = left-aligned (the default / common case). Set once by
	// Center after rasterizing — never on the per-frame Draw path.
	centerOff []int32
	srcGet    sdl.Rect // scratch
	dstGet    sdl.Rect // scratch
}

// Center aligns every wrapped line to the centre of alignW px — the webAO "~~"
// convention. Call once after Rasterize / RasterizeStyled / RasterizeFallback; lines
// already at or past alignW stay flush left. Off the per-frame path, so the small
// per-line slice is fine.
func (m *MessageRaster) Center(alignW int32) {
	off := func(lineW int32) int32 {
		if d := (alignW - lineW) / 2; d > 0 {
			return d
		}
		return 0
	}
	if m.styled != nil {
		m.centerOff = make([]int32, len(m.styled))
		for i, spans := range m.styled {
			if n := len(spans); n > 0 {
				last := spans[n-1]
				m.centerOff[i] = off(last.xOffset + last.w) // spans tile L→R: last's right edge = line width
			}
		}
		return
	}
	m.centerOff = make([]int32, len(m.lines))
	for i := range m.lines {
		m.centerOff[i] = off(m.lines[i].w)
	}
}

// lineOffset returns the centering offset for line i (0 when left-aligned).
func (m *MessageRaster) lineOffset(i int) int32 {
	if i < len(m.centerOff) {
		return m.centerOff[i]
	}
	return 0
}

// Rasterize renders text wrapped to wrapW pixels in the given font/color.
// Call once per message (render thread).
func Rasterize(ren *sdl.Renderer, font *ttf.Font, text string, wrapW int32, color sdl.Color) (*MessageRaster, error) {
	m := &MessageRaster{text: text, lineH: int32(font.Height())}
	if strings.TrimSpace(text) == "" {
		return m, nil
	}
	for _, line := range wrapText(font, text, wrapW) {
		rl, err := rasterizeLine(ren, font, line, color)
		if err != nil {
			m.Destroy()
			return nil, err
		}
		m.lines = append(m.lines, rl)
	}
	return m, nil
}

func rasterizeLine(ren *sdl.Renderer, font *ttf.Font, line string, color sdl.Color) (rasterLine, error) {
	runes := []rune(line)
	rl := rasterLine{runes: len(runes), advances: make([]int32, len(runes)+1)}
	for i := 1; i <= len(runes); i++ {
		w, _, err := font.SizeUTF8(string(runes[:i]))
		if err != nil {
			return rl, err
		}
		rl.advances[i] = int32(w)
	}
	if line == "" {
		return rl, nil
	}
	surf, err := font.RenderUTF8Blended(line, color)
	if err != nil {
		return rl, err
	}
	defer surf.Free()
	tex, err := ren.CreateTextureFromSurface(surf)
	if err != nil {
		return rl, err
	}
	rl.tex = tex
	rl.w = surf.W
	rl.h = surf.H
	return rl, nil
}

// ColorSpan styles Len consecutive runes of a message. The UI builds these
// from the courtroom style runs — palette index / default already resolved to
// a concrete color, and rainbow pre-expanded into per-rune spans — so render
// needs no palette knowledge. Spans partition the message text in order.
type ColorSpan struct {
	Len    int
	Color  sdl.Color
	Bold   bool
	Italic bool
}

// spanStyle is the resolved per-rune style the raster groups consecutive runes
// by (a new texture each time it changes). Comparable, so grouping is `==`.
type spanStyle struct {
	color  sdl.Color
	bold   bool
	italic bool
}

// RasterizeStyled renders a multi-color message: each wrapped line is split
// into same-color spans (one texture each), laid out left to right. Reveal
// stays a rune-count walk with zero per-frame allocations. Render thread, once
// per message.
func RasterizeStyled(ren *sdl.Renderer, font *ttf.Font, text string, spans []ColorSpan, wrapW int32) (*MessageRaster, error) {
	m := &MessageRaster{text: text, lineH: int32(font.Height()), styled: [][]rasterSpan{}}
	runes := []rune(text)
	if len(runes) == 0 {
		return m, nil
	}
	styles := perRuneStyles(runes, spans)
	for _, lr := range wrapStyled(font, runes, wrapW) {
		line, err := rasterizeStyledLine(ren, font, runes[lr.start:lr.end], styles[lr.start:lr.end])
		if err != nil {
			m.Destroy()
			return nil, err
		}
		m.styled = append(m.styled, line)
	}
	return m, nil
}

// perRuneStyles flattens the span partition into one style per rune (guarding a
// short partition by repeating the last span's style).
func perRuneStyles(runes []rune, spans []ColorSpan) []spanStyle {
	out := make([]spanStyle, len(runes))
	i := 0
	for _, sp := range spans {
		st := spanStyle{color: sp.Color, bold: sp.Bold, italic: sp.Italic}
		for k := 0; k < sp.Len && i < len(runes); k++ {
			out[i] = st
			i++
		}
	}
	tail := spanStyle{color: sdl.Color{R: 255, G: 255, B: 255, A: 255}}
	if len(spans) > 0 {
		s := spans[len(spans)-1]
		tail = spanStyle{color: s.Color, bold: s.Bold, italic: s.Italic}
	}
	for ; i < len(runes); i++ {
		out[i] = tail
	}
	return out
}

type lineRange struct{ start, end int }

// wrapStyled greedily word-wraps runes to maxW px, returning each line's
// [start,end) rune range so colors map by index. Breaks at the last space
// before the limit, hard-breaks an over-long word, and splits on '\n'. Mirrors
// wrapText's visuals but preserves rune positions (Fields would drop them).
func wrapStyled(font *ttf.Font, runes []rune, maxW int32) []lineRange {
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
		w, _, _ := font.SizeUTF8(string(runes[lineStart : i+1]))
		if int32(w) > maxW && i > lineStart {
			if lastSpace > lineStart {
				out = append(out, lineRange{lineStart, lastSpace}) // break at the space, drop it
				lineStart = lastSpace + 1
			} else {
				out = append(out, lineRange{lineStart, i}) // over-long word: hard break
				lineStart = i
			}
			lastSpace = -1
			continue // re-measure from the new line start
		}
		i++
	}
	out = append(out, lineRange{lineStart, n})
	return out
}

// rasterizeStyledLine builds one wrapped line's spans — a new span each time
// the style changes, each with its own texture and prefix advances.
func rasterizeStyledLine(ren *sdl.Renderer, font *ttf.Font, runes []rune, styles []spanStyle) ([]rasterSpan, error) {
	var spans []rasterSpan
	var x int32
	for i := 0; i < len(runes); {
		j := i + 1
		for j < len(runes) && styles[j] == styles[i] {
			j++
		}
		sp, err := buildSpan(ren, font, runes[i:j], styles[i], x, 0) // same font across the line → no baseline offset
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

// buildSpan rasterizes one same-style run at xOffset with prefix advances.
// Bold/italic ride synthetic SDL_ttf font styles, restored on return (defer) so
// the shared cached font never leaks a style into other (plain) text.
func buildSpan(ren *sdl.Renderer, font *ttf.Font, runes []rune, st spanStyle, xOffset, yOff int32) (rasterSpan, error) {
	style := ttf.STYLE_NORMAL
	if st.bold {
		style |= ttf.STYLE_BOLD
	}
	if st.italic {
		style |= ttf.STYLE_ITALIC
	}
	if style != ttf.STYLE_NORMAL {
		font.SetStyle(style)
		defer font.SetStyle(ttf.STYLE_NORMAL) // always reset, even on error/panic
	}
	sp := rasterSpan{runes: len(runes), xOffset: xOffset, yOff: yOff, advances: make([]int32, len(runes)+1)}
	for i := 1; i <= len(runes); i++ {
		w, _, err := font.SizeUTF8(string(runes[:i]))
		if err != nil {
			return sp, err
		}
		sp.advances[i] = int32(w)
	}
	surf, err := font.RenderUTF8Blended(string(runes), st.color)
	if err != nil {
		return sp, err
	}
	defer surf.Free()
	tex, err := ren.CreateTextureFromSurface(surf)
	if err != nil {
		return sp, err
	}
	sp.tex = tex
	sp.w = surf.W
	sp.h = surf.H
	return sp, nil
}

// Draw blits the first visibleRunes runes at (x, y). Full lines blit whole;
// the partial line reveals via src-rect width — O(lines) per frame, zero
// allocations, zero texture churn.
func (m *MessageRaster) Draw(ren *sdl.Renderer, visibleRunes int, x, y int32) {
	if m.styled != nil {
		m.drawStyled(ren, visibleRunes, x, y)
		return
	}
	remaining := visibleRunes
	lineY := y
	for i := range m.lines {
		line := &m.lines[i]
		if remaining <= 0 {
			return
		}
		show := line.runes
		if remaining < show {
			show = remaining
		}
		if line.tex != nil && show > 0 {
			width := line.advances[show]
			m.srcGet = sdl.Rect{X: 0, Y: 0, W: width, H: line.h}
			m.dstGet = sdl.Rect{X: x + m.lineOffset(i), Y: lineY, W: width, H: line.h}
			_ = ren.Copy(line.tex, &m.srcGet, &m.dstGet)
		}
		remaining -= line.runes
		lineY += m.lineH
	}
}

// drawStyled is Draw for a multi-color message: walk lines, then the spans
// within each line, revealing by rune count (same zero-alloc src-rect trick).
func (m *MessageRaster) drawStyled(ren *sdl.Renderer, visibleRunes int, x, y int32) {
	remaining := visibleRunes
	lineY := y
	for li := range m.styled {
		spans := m.styled[li]
		if remaining <= 0 {
			return
		}
		lineX := x + m.lineOffset(li)
		for i := range spans {
			sp := &spans[i]
			if remaining <= 0 {
				break
			}
			show := sp.runes
			if remaining < show {
				show = remaining
			}
			if sp.tex != nil && show > 0 {
				width := sp.advances[show]
				m.srcGet = sdl.Rect{X: 0, Y: 0, W: width, H: sp.h}
				m.dstGet = sdl.Rect{X: lineX + sp.xOffset, Y: lineY + sp.yOff, W: width, H: sp.h} // yOff baseline-aligns the emoji fallback (0 for plain color spans)
				_ = ren.Copy(sp.tex, &m.srcGet, &m.dstGet)
			}
			remaining -= sp.runes
		}
		lineY += m.lineH
	}
}

// TotalRunes returns the rasterized rune count (lines joined).
func (m *MessageRaster) TotalRunes() int {
	if m.styled != nil {
		total := 0
		for _, spans := range m.styled {
			for i := range spans {
				total += spans[i].runes
			}
		}
		return total
	}
	total := 0
	for _, l := range m.lines {
		total += l.runes
	}
	return total
}

// Height returns the rasterized message's full pixel height (all wrapped lines
// stacked at lineH), so a caller can size a box to fit it. Zero for an empty
// message.
func (m *MessageRaster) Height() int32 {
	n := len(m.lines)
	if m.styled != nil {
		n = len(m.styled)
	}
	return int32(n) * m.lineH
}

// Text returns the rasterized source text.
func (m *MessageRaster) Text() string { return m.text }

// Destroy frees all line/span textures. Render thread only.
func (m *MessageRaster) Destroy() {
	for i := range m.lines {
		if m.lines[i].tex != nil {
			_ = m.lines[i].tex.Destroy()
			m.lines[i].tex = nil
		}
	}
	m.lines = nil
	for _, spans := range m.styled {
		for i := range spans {
			if spans[i].tex != nil {
				_ = spans[i].tex.Destroy()
				spans[i].tex = nil
			}
		}
	}
	m.styled = nil
}

// wrapText greedily word-wraps text to maxW pixels using real glyph metrics.
func wrapText(font *ttf.Font, text string, maxW int32) []string {
	if maxW <= 0 {
		return []string{text}
	}
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		current := words[0]
		for _, word := range words[1:] {
			candidate := current + " " + word
			w, _, err := font.SizeUTF8(candidate)
			if err == nil && int32(w) <= maxW {
				current = candidate
				continue
			}
			lines = append(lines, current)
			current = word
		}
		lines = append(lines, current)
	}
	return lines
}
