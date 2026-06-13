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
	srcGet sdl.Rect // scratch
	dstGet sdl.Rect // scratch
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

// ColorSpan colors Len consecutive runes of a message. The UI builds these
// from the courtroom style runs — palette index / default already resolved to
// a concrete color, and rainbow pre-expanded into per-rune spans — so render
// needs no palette knowledge. Spans partition the message text in order.
type ColorSpan struct {
	Len   int
	Color sdl.Color
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
	colors := perRuneColors(runes, spans)
	for _, lr := range wrapStyled(font, runes, wrapW) {
		line, err := rasterizeStyledLine(ren, font, runes[lr.start:lr.end], colors[lr.start:lr.end])
		if err != nil {
			m.Destroy()
			return nil, err
		}
		m.styled = append(m.styled, line)
	}
	return m, nil
}

// perRuneColors flattens the span partition into one color per rune (guarding a
// short partition by repeating the last color).
func perRuneColors(runes []rune, spans []ColorSpan) []sdl.Color {
	colors := make([]sdl.Color, len(runes))
	i := 0
	for _, sp := range spans {
		for k := 0; k < sp.Len && i < len(runes); k++ {
			colors[i] = sp.Color
			i++
		}
	}
	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	for ; i < len(runes); i++ {
		if len(spans) > 0 {
			colors[i] = spans[len(spans)-1].Color
		} else {
			colors[i] = white
		}
	}
	return colors
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

// rasterizeStyledLine builds one wrapped line's colored spans — a new span each
// time the color changes, each with its own texture and prefix advances.
func rasterizeStyledLine(ren *sdl.Renderer, font *ttf.Font, runes []rune, colors []sdl.Color) ([]rasterSpan, error) {
	var spans []rasterSpan
	var x int32
	for i := 0; i < len(runes); {
		j := i + 1
		for j < len(runes) && colors[j] == colors[i] {
			j++
		}
		sp, err := buildSpan(ren, font, runes[i:j], colors[i], x)
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

// buildSpan rasterizes one same-color run at xOffset with prefix advances.
func buildSpan(ren *sdl.Renderer, font *ttf.Font, runes []rune, color sdl.Color, xOffset int32) (rasterSpan, error) {
	sp := rasterSpan{runes: len(runes), xOffset: xOffset, advances: make([]int32, len(runes)+1)}
	for i := 1; i <= len(runes); i++ {
		w, _, err := font.SizeUTF8(string(runes[:i]))
		if err != nil {
			return sp, err
		}
		sp.advances[i] = int32(w)
	}
	surf, err := font.RenderUTF8Blended(string(runes), color)
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
			m.dstGet = sdl.Rect{X: x, Y: lineY, W: width, H: line.h}
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
	for _, spans := range m.styled {
		if remaining <= 0 {
			return
		}
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
				m.dstGet = sdl.Rect{X: x + sp.xOffset, Y: lineY, W: width, H: sp.h}
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
