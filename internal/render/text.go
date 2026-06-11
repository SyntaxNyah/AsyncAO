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

// rasterLine is one wrapped line: a texture plus per-rune prefix advances so
// the reveal is a pure src-rect width (spec §12: no per-character
// layout, no texture churn).
type rasterLine struct {
	tex      *sdl.Texture
	w, h     int32
	runes    int
	advances []int32 // advances[i] = pixel width of the first i runes
}

// MessageRaster pre-rasterizes a full message once; Draw reveals it by rune
// count with zero allocations per frame.
type MessageRaster struct {
	lines  []rasterLine
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

// Draw blits the first visibleRunes runes at (x, y). Full lines blit whole;
// the partial line reveals via src-rect width — O(lines) per frame, zero
// allocations, zero texture churn.
func (m *MessageRaster) Draw(ren *sdl.Renderer, visibleRunes int, x, y int32) {
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

// TotalRunes returns the rasterized rune count (lines joined).
func (m *MessageRaster) TotalRunes() int {
	total := 0
	for _, l := range m.lines {
		total += l.runes
	}
	return total
}

// Text returns the rasterized source text.
func (m *MessageRaster) Text() string { return m.text }

// Destroy frees all line textures. Render thread only.
func (m *MessageRaster) Destroy() {
	for i := range m.lines {
		if m.lines[i].tex != nil {
			_ = m.lines[i].tex.Destroy()
			m.lines[i].tex = nil
		}
	}
	m.lines = nil
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
